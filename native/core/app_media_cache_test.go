package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mediaTestAsset(address, body string) nativeStreamAsset {
	return nativeStreamAsset{
		address: address, contentType: "video/mp2t",
		data: []byte(body), total: int64(len(body)),
		etag: `"abc"`,
	}
}

func TestNativeMediaCacheStoresAndReloadsFromDisk(t *testing.T) {
	directory := t.TempDir()
	cache := newNativeMediaCache(directory)
	if !cache.enabled() {
		t.Fatal("cache should be enabled with a data directory")
	}
	asset := mediaTestAsset("https://cdn.test/a.ts", "segment-body")
	if !cache.store(asset) {
		t.Fatal("store rejected a plain segment")
	}
	loaded, ok := cache.load(asset.address)
	if !ok || string(loaded.data) != "segment-body" {
		t.Fatalf("cache miss after store: %v %q", ok, loaded.data)
	}
	if loaded.contentType != "video/mp2t" || loaded.etag != `"abc"` || loaded.total != int64(len("segment-body")) {
		t.Fatalf("metadata lost: %+v", loaded)
	}
	// 重启后（新建一份索引）仍应命中，否则预缓存跨会话就没意义了。
	restarted := newNativeMediaCache(directory)
	if reloaded, ok := restarted.load(asset.address); !ok || string(reloaded.data) != "segment-body" {
		t.Fatal("cache did not survive a restart")
	}
}

func TestNativeMediaCacheSkipsPrefixPlaylistAndOversized(t *testing.T) {
	cache := newNativeMediaCache(t.TempDir())
	prefix := mediaTestAsset("https://cdn.test/prefix.ts", "head")
	prefix.total = 4096 // 只拿到前缀（206）
	if cache.store(prefix) {
		t.Fatal("partial body must not be cached")
	}
	playlist := nativeStreamAsset{
		address: "https://cdn.test/index.m3u8", contentType: "application/vnd.apple.mpegurl",
		data: []byte("#EXTM3U\n#EXT-X-ENDLIST\n"), total: 25,
	}
	if cache.store(playlist) {
		t.Fatal("playlists must not be cached on disk")
	}
	oversized := mediaTestAsset("https://cdn.test/huge.mp4", strings.Repeat("x", nativeMediaAssetMaxBytes+1))
	if cache.store(oversized) {
		t.Fatal("oversized body must not be cached")
	}
	if bytes := cache.stats()["bytes"].(int64); bytes != 0 {
		t.Fatalf("rejected assets still occupied %d bytes", bytes)
	}
}

func TestNativeMediaCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newNativeMediaCache(t.TempDir())
	// 走 `setLimit` 会被下限（128MB）夹住，这里直接压上限来验证 LRU。
	cache.limit = 20
	first := mediaTestAsset("https://cdn.test/1.ts", strings.Repeat("a", 10))
	if !cache.store(first) {
		t.Fatal("store failed")
	}
	// 让第一条成为"最久未使用"。
	cache.mu.Lock()
	entry := cache.entries[nativeMediaKey(first.address)]
	entry.usedAt = time.Now().Add(-time.Hour)
	cache.entries[nativeMediaKey(first.address)] = entry
	cache.mu.Unlock()

	if !cache.store(mediaTestAsset("https://cdn.test/2.ts", strings.Repeat("b", 10))) {
		t.Fatal("store failed")
	}
	if !cache.store(mediaTestAsset("https://cdn.test/3.ts", strings.Repeat("c", 10))) {
		t.Fatal("store failed")
	}
	if _, ok := cache.load(first.address); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if stats := cache.stats(); stats["bytes"].(int64) > 20 {
		t.Fatalf("cache exceeded its limit: %d", stats["bytes"])
	}
}

func TestNativeMediaCacheDropsExpiredEntries(t *testing.T) {
	cache := newNativeMediaCache(t.TempDir())
	asset := mediaTestAsset("https://cdn.test/old.ts", "stale")
	if !cache.store(asset) {
		t.Fatal("store failed")
	}
	cache.mu.Lock()
	entry := cache.entries[nativeMediaKey(asset.address)]
	entry.usedAt = time.Now().Add(-nativeMediaCacheTTL - time.Hour)
	cache.entries[nativeMediaKey(asset.address)] = entry
	cache.pruneLocked("")
	cache.mu.Unlock()
	if _, ok := cache.load(asset.address); ok {
		t.Fatal("expired entry was still readable")
	}
}

func TestNativeMediaCacheClearAndStats(t *testing.T) {
	cache := newNativeMediaCache(t.TempDir())
	cache.store(mediaTestAsset("https://cdn.test/a.ts", "aaaa"))
	cache.store(mediaTestAsset("https://cdn.test/b.ts", "bbbb"))
	stats := cache.stats()
	if stats["files"].(int) != 2 || stats["bytes"].(int64) != 8 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	cache.clear()
	if stats := cache.stats(); stats["files"].(int) != 0 || stats["bytes"].(int64) != 0 {
		t.Fatalf("clear left data behind: %+v", stats)
	}
	if _, ok := cache.load("https://cdn.test/a.ts"); ok {
		t.Fatal("entry readable after clear")
	}
}

func TestNativeMediaCacheSetLimitClamps(t *testing.T) {
	cache := newNativeMediaCache(t.TempDir())
	cache.store(mediaTestAsset("https://cdn.test/a.ts", strings.Repeat("a", 64)))
	cache.setLimit(1) // 低于下限
	if limit := cache.limitBytes(); limit != nativeMediaCacheMinimum {
		t.Fatalf("limit not clamped to minimum: %d", limit)
	}
	cache.setLimit(1 << 40) // 高于上限
	if limit := cache.limitBytes(); limit != nativeMediaCacheMaximum {
		t.Fatalf("limit not clamped to maximum: %d", limit)
	}
}

// 端到端：整集预缓存写盘后，即使上游挂掉，新的播放会话也能从磁盘读出分片。
func TestNativePrecacheWarmsWholeEpisodeToDisk(t *testing.T) {
	const playlist = "#EXTM3U\n#EXT-X-TARGETDURATION:2\n" +
		"#EXTINF:2,\nseg1.ts\n#EXTINF:2,\nseg2.ts\n#EXT-X-ENDLIST\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(writer, playlist)
		case "/seg1.ts":
			io.WriteString(writer, "first-segment")
		case "/seg2.ts":
			io.WriteString(writer, "second-segment")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := newNativeStreamServer(engine.downloader, engine.mediaCache())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{URL: upstream.URL + "/index.m3u8"})
	defer stream.nativeRelease(token)
	written := stream.nativePrecacheStream(context.Background(), token, address, 0, 1<<20)
	if written <= 0 {
		t.Fatalf("precache wrote nothing: %d", written)
	}
	if stats := engine.mediaCache().stats(); stats["files"].(int) != 2 {
		t.Fatalf("expected two cached segments, got %+v", stats)
	}

	// 上游下线，模拟"切集时网络正好抽风"。
	upstream.Close()

	// 新会话（不同 token）拿到同一份播放列表，分片地址相同 → 应命中磁盘。
	second, secondToken := stream.nativeOpen(providerMedia{
		URL: upstream.URL + "/index.m3u8", Playlist: playlist,
	})
	defer stream.nativeRelease(secondToken)
	response, err := http.Get(second)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var segments []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "http://") {
			segments = append(segments, line)
		}
	}
	if len(segments) != 2 {
		t.Fatalf("expected two segment urls, got %v", segments)
	}
	for index, want := range []string{"first-segment", "second-segment"} {
		got, err := http.Get(segments[index])
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(got.Body)
		got.Body.Close()
		if string(payload) != want {
			t.Fatalf("segment %d not served from disk cache: %q", index, payload)
		}
	}
}

// 预缓存只往磁盘写，**不把分片正文塞回播放会话内存**。
//
// 0.8.12 首版把整集分片都写进 `session.assets`，而这段会话往往正是
// "马上要播的那一集"（`precache` 优先复用 `preload` 的会话）：
// 30 段 × 2MB ≈ 60MB 常驻到换集为止，电视盒子上很容易被系统回收 ——
// 表现就是"看两集之后开始卡，退出再进才好"。
func TestNativePrecacheLeavesSegmentBodiesOutOfTheSession(t *testing.T) {
	const playlist = "#EXTM3U\n#EXT-X-TARGETDURATION:2\n" +
		"#EXTINF:2,\nseg1.ts\n#EXTINF:2,\nseg2.ts\n#EXT-X-ENDLIST\n"
	segment := strings.Repeat("x", 4096)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(writer, playlist)
		case "/seg1.ts", "/seg2.ts":
			_, _ = io.WriteString(writer, segment)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := newNativeStreamServer(engine.downloader, engine.mediaCache())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{URL: upstream.URL + "/index.m3u8"})
	defer stream.nativeRelease(token)
	if written := stream.nativePrecacheStream(context.Background(), token, address, 0, 1<<20); written <= 0 {
		t.Fatalf("precache wrote nothing: %d", written)
	}

	session := stream.sessions[token]
	if session == nil {
		t.Fatal("stream session missing")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	for id, asset := range session.assets {
		// 播放列表要留在会话里（播放时按会话重写分片地址），分片正文不该留。
		if len(asset.data) == 0 || strings.Contains(asset.contentType, "mpegurl") {
			continue
		}
		t.Fatalf("session kept %d bytes of segment body (%s)", len(asset.data), id)
	}
}

// `preload` 预热进会话的只是**前缀**（`total > len(data)`），
// 预缓存必须重新拉完整正文再落盘，否则开头几段永远进不了磁盘缓存。
func TestNativePrecacheFillsInPrefetchedPrefix(t *testing.T) {
	const playlist = "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n"
	full := strings.Repeat("y", 8192)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(writer, playlist)
		case "/seg1.ts":
			writer.Header().Set("Content-Type", "video/mp2t")
			_, _ = io.WriteString(writer, full)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := newNativeStreamServer(engine.downloader, engine.mediaCache())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{URL: upstream.URL + "/index.m3u8"})
	defer stream.nativeRelease(token)
	// 让代理把播放列表重写出来，才能拿到分片在会话里的 id。
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, _ := io.ReadAll(response.Body)
	response.Body.Close()
	segmentURL := ""
	for _, line := range strings.Split(string(rewritten), "\n") {
		if strings.HasPrefix(line, "http://") {
			segmentURL = strings.TrimSpace(line)
			break
		}
	}
	if segmentURL == "" {
		t.Fatalf("playlist was not rewritten: %q", rewritten)
	}
	id := segmentURL[strings.LastIndex(segmentURL, "/")+1:]

	// 模拟 preload 预热：会话里只有前 6 字节，但 total 是完整长度。
	session := stream.sessions[token]
	session.mu.Lock()
	asset := session.assets[id]
	asset.data = []byte("prefix")
	asset.total = int64(len(full))
	session.assets[id] = asset
	session.mu.Unlock()

	if written := stream.nativePrecacheStream(context.Background(), token, address, 0, 1<<20); written != int64(len(full)) {
		t.Fatalf("precache should fetch the full segment, wrote %d of %d", written, len(full))
	}
	cached, hit := stream.media.load(upstream.URL + "/seg1.ts")
	if !hit || string(cached.data) != full {
		t.Fatalf("segment not cached completely: hit=%v len=%d", hit, len(cached.data))
	}

	// 播放路径：会话里只有前缀，但磁盘有完整正文 → 应直接走磁盘，不再找上游。
	served, err := http.Get(segmentURL)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(served.Body)
	served.Body.Close()
	if string(payload) != full {
		t.Fatalf("playback did not use the disk cache: got %d bytes", len(payload))
	}
}
