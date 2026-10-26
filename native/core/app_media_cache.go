package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 播放预缓存的磁盘缓存（0.8.12）。
//
// 背景：0.8.11 之前的"预加载"只是**提前解析出下一集的播放地址**，并把开头
// 4MB 预热进当前播放会话的内存里（见 `app_preload.go`）。一旦切集，播放会话
// 换掉，那 4MB 就没了 —— 用户体感仍是"切下一集要等一下"。
//
// 这里补上真正的一层：把分片（.ts / .m4s / mp4 小块）按**上游地址**落到磁盘，
// 跨会话共享。这样：
//   - 预缓存过的集，切过去时播放器直接从本地磁盘读分片，不再等网络；
//   - 缓存与播放会话解耦，切集 / 退出播放页都不会丢；
//   - 用 LRU + 容量上限 + TTL 三重约束，避免越积越大。
//
// 只缓存**小块**（分片、密钥、init 段）。整段 mp4 走 Range 请求，
// 单文件几十 MB，缓存下来命中率低又占地方，这里直接跳过。
const (
	// 单条缓存上限。短剧分片通常 0.3~3MB，12MB 足够；
	// 超过这个量级的基本是整段 mp4，不做缓存。
	nativeMediaAssetMaxBytes = 12 << 20
	// 条目数上限，防止小分片把目录塞满。
	nativeMediaCacheEntries = 4000
	// 分片内容的 TTL。视频数据本身不会过期，但上游地址会换，
	// 留 7 天既能复用又能自然淘汰掉不再被访问的旧剧。
	nativeMediaCacheTTL = 7 * 24 * time.Hour
	// 默认 / 可配置的容量上限。
	nativeMediaCacheDefault = 1 << 30
	nativeMediaCacheMinimum = 128 << 20
	nativeMediaCacheMaximum = 8 << 30
	// 淘汰水位：超过上限后回收到 80%（4/5），避免每写一条就淘汰一次。
	nativeMediaCacheWatermark = 5
)

type nativeMediaMeta struct {
	Address     string `json:"address"`
	ContentType string `json:"contentType"`
	ETag        string `json:"etag,omitempty"`
	Modified    string `json:"modified,omitempty"`
	Total       int64  `json:"total"`
}

type nativeMediaEntry struct {
	size      int64
	updatedAt time.Time
	usedAt    time.Time
}

type nativeMediaCache struct {
	directory string
	mu        sync.Mutex
	entries   map[string]nativeMediaEntry
	size      int64
	limit     int64
}

// nativeMediaKey 用上游地址做键。
//
// 不掺会话 / 画质：同一段分片无论哪个会话、哪次播放，内容都一样。
// 掺进去只会让缓存命中不了。
func nativeMediaKey(address string) string {
	digest := sha256.Sum256([]byte(address))
	return hex.EncodeToString(digest[:])
}

func newNativeMediaCache(directory string) *nativeMediaCache {
	cache := &nativeMediaCache{
		entries: map[string]nativeMediaEntry{},
		limit:   nativeMediaCacheDefault,
	}
	if strings.TrimSpace(directory) == "" {
		return cache
	}
	cache.directory = filepath.Join(directory, "media-v1")
	if err := os.MkdirAll(cache.directory, 0o755); err != nil {
		cache.directory = ""
		return cache
	}
	files, _ := os.ReadDir(cache.directory)
	for _, file := range files {
		name := file.Name()
		if file.IsDir() {
			continue
		}
		// 上次进程被中断留下的半成品，直接丢掉。
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(cache.directory, name))
			continue
		}
		key, valid := strings.CutSuffix(name, ".bin")
		if !valid || len(key) != 64 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		info, err := file.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > nativeMediaAssetMaxBytes {
			_ = os.Remove(filepath.Join(cache.directory, name))
			continue
		}
		if _, err := os.Stat(filepath.Join(cache.directory, key+".meta")); err != nil {
			// 没有元数据就没法校验地址归属，留着也读不出来。
			_ = os.Remove(filepath.Join(cache.directory, name))
			continue
		}
		// 重启后用文件时间恢复 LRU 顺序。
		cache.entries[key] = nativeMediaEntry{size: info.Size(), updatedAt: info.ModTime(), usedAt: info.ModTime()}
		cache.size += info.Size()
	}
	cache.pruneLocked("")
	return cache
}

func (cache *nativeMediaCache) enabled() bool {
	return cache != nil && cache.directory != ""
}

func (cache *nativeMediaCache) limitBytes() int64 {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.limit
}

// setLimit 调整容量上限（字节），随后立即按新上限回收。
func (cache *nativeMediaCache) setLimit(bytes int64) {
	if cache == nil {
		return
	}
	if bytes < nativeMediaCacheMinimum {
		bytes = nativeMediaCacheMinimum
	}
	if bytes > nativeMediaCacheMaximum {
		bytes = nativeMediaCacheMaximum
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.limit = bytes
	cache.pruneLocked("")
}

// stats 返回当前占用、条目数、上限与目录，供设置页展示。
func (cache *nativeMediaCache) stats() map[string]any {
	if cache == nil {
		return map[string]any{"bytes": 0, "files": 0, "limit": 0, "directory": ""}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return map[string]any{
		"bytes":     cache.size,
		"files":     len(cache.entries),
		"limit":     cache.limit,
		"directory": cache.directory,
	}
}

// clear 清空整份缓存（设置页的「清理」按钮）。
func (cache *nativeMediaCache) clear() {
	if !cache.enabled() {
		return
	}
	cache.mu.Lock()
	keys := make([]string, 0, len(cache.entries))
	for key := range cache.entries {
		keys = append(keys, key)
	}
	cache.entries = map[string]nativeMediaEntry{}
	cache.size = 0
	cache.mu.Unlock()
	for _, key := range keys {
		cache.removeFiles(key)
	}
}

func (cache *nativeMediaCache) removeFiles(key string) {
	_ = os.Remove(filepath.Join(cache.directory, key+".bin"))
	_ = os.Remove(filepath.Join(cache.directory, key+".meta"))
}

// load 读一条缓存。命中时返回可直接交给播放器的 asset（含正文）。
//
// 未命中是**热路径上的常态**（没预缓存过的分片每次都要走这里），
// 所以只在"索引里有、但已过期"时才去删文件，纯未命中直接返回。
func (cache *nativeMediaCache) load(address string) (nativeStreamAsset, bool) {
	if !cache.enabled() {
		return nativeStreamAsset{}, false
	}
	key := nativeMediaKey(address)
	cache.mu.Lock()
	entry, found := cache.entries[key]
	if !found {
		cache.mu.Unlock()
		return nativeStreamAsset{}, false
	}
	if time.Since(entry.usedAt) > nativeMediaCacheTTL {
		delete(cache.entries, key)
		cache.size -= entry.size
		cache.mu.Unlock()
		cache.removeFiles(key)
		return nativeStreamAsset{}, false
	}
	entry.usedAt = time.Now()
	cache.entries[key] = entry
	cache.mu.Unlock()
	metaBytes, err := os.ReadFile(filepath.Join(cache.directory, key+".meta"))
	if err != nil {
		cache.drop(key)
		return nativeStreamAsset{}, false
	}
	var meta nativeMediaMeta
	if json.Unmarshal(metaBytes, &meta) != nil || meta.Address != address || meta.ContentType == "" {
		cache.drop(key)
		return nativeStreamAsset{}, false
	}
	body, err := os.ReadFile(filepath.Join(cache.directory, key+".bin"))
	if err != nil || len(body) == 0 {
		cache.drop(key)
		return nativeStreamAsset{}, false
	}
	// 刷新 mtime，让 LRU 顺序在重启后仍然有效。
	now := time.Now()
	_ = os.Chtimes(filepath.Join(cache.directory, key+".bin"), now, now)
	total := meta.Total
	if total <= 0 {
		total = int64(len(body))
	}
	return nativeStreamAsset{
		address:     meta.Address,
		data:        body,
		contentType: meta.ContentType,
		total:       total,
		etag:        meta.ETag,
		modified:    meta.Modified,
	}, true
}

// store 写一条缓存。正文过大、为空、或只是**前缀**时直接放弃（不算错误）。
func (cache *nativeMediaCache) store(asset nativeStreamAsset) bool {
	if !cache.enabled() || len(asset.data) == 0 || len(asset.data) > nativeMediaAssetMaxBytes {
		return false
	}
	// 只收完整正文。预取（`nativePrefetch`）是按 `Range: bytes=0-N` 拉的前缀，
	// 存下来还要靠上游续读，收益低风险高，交给整集预缓存去做完整下载。
	if asset.total > int64(len(asset.data)) {
		return false
	}
	// 播放列表按会话重写，缓存正文没有意义；这里只存分片类媒体。
	if strings.Contains(asset.contentType, "mpegurl") || looksLikePlaylist(asset.data) {
		return false
	}
	key := nativeMediaKey(asset.address)
	meta := nativeMediaMeta{
		Address: asset.address, ContentType: asset.contentType,
		ETag: asset.etag, Modified: asset.modified, Total: asset.total,
	}
	if meta.Total <= 0 {
		meta.Total = int64(len(asset.data))
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return false
	}
	cache.mu.Lock()
	if _, found := cache.entries[key]; found {
		// 已有同一条，没必要重复写盘。
		cache.mu.Unlock()
		return true
	}
	cache.mu.Unlock()
	if err := writeNativeMediaFile(filepath.Join(cache.directory, key+".bin"), asset.data); err != nil {
		return false
	}
	if err := writeNativeMediaFile(filepath.Join(cache.directory, key+".meta"), metaBytes); err != nil {
		cache.removeFiles(key)
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if previous, found := cache.entries[key]; found {
		cache.size -= previous.size
	}
	now := time.Now()
	cache.entries[key] = nativeMediaEntry{size: int64(len(asset.data)), updatedAt: now, usedAt: now}
	cache.size += int64(len(asset.data))
	cache.pruneLocked(key)
	return true
}

func writeNativeMediaFile(path string, body []byte) error {
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, path)
}

func (cache *nativeMediaCache) drop(key string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	entry, found := cache.entries[key]
	if found {
		delete(cache.entries, key)
		cache.size -= entry.size
	}
	cache.mu.Unlock()
	if found {
		cache.removeFiles(key)
	}
}

// pruneLocked 按 LRU 回收。protect 是刚写入的键，最后才考虑淘汰它。
// 调用方必须已持有 cache.mu。
func (cache *nativeMediaCache) pruneLocked(protect string) {
	if !cache.enabled() {
		return
	}
	if len(cache.entries) > nativeMediaCacheEntries || cache.size > cache.limit {
		keys := make([]string, 0, len(cache.entries))
		for key := range cache.entries {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return cache.entries[keys[i]].usedAt.Before(cache.entries[keys[j]].usedAt)
		})
		target := cache.limit / nativeMediaCacheWatermark * (nativeMediaCacheWatermark - 1)
		for _, key := range keys {
			if len(cache.entries) <= nativeMediaCacheEntries && cache.size <= target {
				break
			}
			if key == protect {
				continue
			}
			entry := cache.entries[key]
			delete(cache.entries, key)
			cache.size -= entry.size
			go cache.removeFiles(key)
		}
	}
	// TTL 淘汰：过期条目无论容量如何都清掉。
	now := time.Now()
	for key, entry := range cache.entries {
		if now.Sub(entry.usedAt) <= nativeMediaCacheTTL {
			continue
		}
		delete(cache.entries, key)
		cache.size -= entry.size
		go cache.removeFiles(key)
	}
}
