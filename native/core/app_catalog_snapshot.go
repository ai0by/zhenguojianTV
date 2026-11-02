package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 剧集快照（0.8.19）。
//
// 背景：每台设备都按 2 小时一轮自己去各站源抓目录，冷启动尤其难受 ——
// 第一次进首页要等一轮抓取。用户提的方案是"仓库单独建一个剧集缓存分支，
// 加密存剧集信息，定期更新，App 优先拉这个分支（还能走 CDN）"。
//
// 结构（仓库 `catalog` 分支）：
//
//	index.json                    明文：版本 / 生成时间 / 每个站源的文件名与 sha256
//	sources/<站源>.json.enc       AES-256-GCM 加密的剧集数组
//
// 加密只是为了"别让人随手把目录拖走"，密钥随 APK 分发 —— **这是混淆，
// 不是安全边界**。真正的价值在于：一份快照由 CI 统一生成，所有客户端共享，
// 不必各自去踩站源的限流。
const (
	// 快照索引。jsDelivr 走分支引用（边缘缓存 12 小时），够用且国内可达。
	catalogSnapshotIndexURL = "https://cdn.jsdelivr.net/gh/ai0by/zhenguojianTV@catalog/index.json"
	// 兜底：加速站代理 raw.githubusercontent（同样指向 catalog 分支）。
	catalogSnapshotMirrorBase = "https://gh-proxy.org/https://raw.githubusercontent.com/ai0by/zhenguojianTV/catalog/"

	// 单次下载超时（单个文件）。
	catalogSnapshotTimeout = 20 * time.Second
	// 整轮同步超时。15 个站源各几 MB，逐个下载，20 秒根本不够；
	// 但这个动作是后台静默跑的，宁可慢一点也别把用户的流量一次吃光。
	catalogSnapshotTotalTimeout = 150 * time.Second

	catalogSnapshotMaxIndex  = 256 << 10
	catalogSnapshotMaxFile   = 24 << 20
	catalogSnapshotMaxBytes  = catalogSnapshotMaxFile + 64
	catalogSnapshotStateName = "catalog-snapshot.json"

	// 密钥材料。与 `scripts/build_catalog_snapshot.py` 里的 SECRET 必须一致。
	catalogSnapshotSecret = "zhenguojianTV/catalog-snapshot/v1"
)

type catalogSnapshotAsset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
}

type catalogSnapshotIndex struct {
	Version     int                             `json:"version"`
	GeneratedAt string                          `json:"generatedAt"`
	Algorithm   string                          `json:"algorithm"`
	Sources     map[string]catalogSnapshotAsset `json:"sources"`
}

type catalogSnapshotPayload struct {
	Source      string        `json:"source"`
	GeneratedAt string        `json:"generatedAt"`
	Items       []nativeDrama `json:"items"`
}

// catalogSnapshotState 记录"已经灌过哪一版快照"，避免每次启动都重复下载。
type catalogSnapshotState struct {
	GeneratedAt string    `json:"generatedAt"`
	AppliedAt   time.Time `json:"appliedAt"`
	Sources     []string  `json:"sources"`
}

func catalogSnapshotKey() [32]byte {
	return sha256.Sum256([]byte(catalogSnapshotSecret))
}

// CatalogSnapshotSources 返回"当前构建包含、且值得进快照"的站源 id（0.8.19）。
//
// 只给 CI 的导出工具（`native/cmd/catalogdump`）用：它需要知道该抓哪些站源。
// 用 `nativeSourceAvailable` 过滤，所以 `-ldflags -X ...buildAllSources=true`
// 构建出来的工具会覆盖全部站源，正式 APK 则只有红果。
func CatalogSnapshotSources() []string {
	candidates := []string{
		sourceHongguo,
		sourceHuangdou,
		sourceHuangju,
		sourceHuangguoAI,
		sourceHuangguoVideo,
		sourceCloudFront,
		sourceDuanju51,
		sourceYeguo,
		sourceEZDMW,
		sourceSorani,
		sourceHema,
		sourceHuaguo,
		sourceWangguo,
		sourceFaguo,
		sourceWuguo,
	}
	sources := make([]string, 0, len(candidates))
	for _, source := range candidates {
		if nativeSourceAvailable(source) {
			sources = append(sources, source)
		}
	}
	return sources
}

// catalogSnapshotURL 把 index 里的相对路径拼成可下载地址。
func catalogSnapshotURL(file string, mirror bool) string {
	file = strings.TrimPrefix(strings.TrimSpace(file), "/")
	if !mirror {
		return "https://cdn.jsdelivr.net/gh/ai0by/zhenguojianTV@catalog/" + file
	}
	return catalogSnapshotMirrorBase + file
}

func (engine *nativeEngine) catalogSnapshotPath(name string) string {
	return filepath.Join(engine.directory, name)
}

func (engine *nativeEngine) loadCatalogSnapshotState() catalogSnapshotState {
	var state catalogSnapshotState
	body, err := os.ReadFile(engine.catalogSnapshotPath(catalogSnapshotStateName))
	if err != nil {
		return state
	}
	_ = json.Unmarshal(body, &state)
	return state
}

func (engine *nativeEngine) saveCatalogSnapshotState(state catalogSnapshotState) {
	body, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = writeNativeCacheFile(engine.catalogSnapshotPath(catalogSnapshotStateName), body)
}

// fetchCatalogSnapshot 拉一段快照内容（先 jsDelivr，失败再走加速站）。
func (engine *nativeEngine) fetchCatalogSnapshot(ctx context.Context, file string, limit int64) ([]byte, error) {
	var lastErr error
	for _, mirror := range []bool{false, true} {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 每个请求单独限时：整轮预算 150 秒，但单个文件不该拖满整轮。
		reqCtx, cancel := context.WithTimeout(ctx, catalogSnapshotTimeout)
		body, err := engine.fetchCatalogSnapshotOnce(reqCtx, catalogSnapshotURL(file, mirror), limit)
		cancel()
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("剧集快照下载失败")
	}
	return nil, lastErr
}

func (engine *nativeEngine) fetchCatalogSnapshotOnce(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json, */*")
	client := engine.downloader.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	// 先读完并关掉 body，再解密：与源级并发门"先 Close 再处理"是同一条纪律。
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("剧集快照下载失败：HTTP " + response.Status)
	}
	if int64(len(body)) > limit {
		return nil, errors.New("剧集快照文件过大")
	}
	return body, nil
}

// decryptCatalogSnapshot 解开 `[12 字节 nonce][密文]` 的 AES-256-GCM 数据。
func decryptCatalogSnapshot(data []byte) ([]byte, error) {
	key := catalogSnapshotKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	size := aead.NonceSize()
	if len(data) <= size {
		return nil, errors.New("剧集快照内容无效")
	}
	plain, err := aead.Open(nil, data[:size], data[size:], nil)
	if err != nil {
		return nil, errors.New("剧集快照解密失败")
	}
	return plain, nil
}

// nativeSyncCatalogSnapshot 同步一次剧集快照（0.8.19）。
//
// [allowed] 是调用方（Dart 首页）当前启用的站源：只下载用户真的会看到的那些。
// 整份快照 12 个站源约 8 MB，而只开红果的用户只需要其中 1.9 MB —— 不按
// 启用列表过滤的话，最省流量的用法反而要付最贵的账单。传空表示"全都要"。
//
// 返回是否真的灌进了新数据。整个过程**不阻塞任何 UI**：调用方在后台发起，
// 失败就当作没这回事，现场抓取照旧。
func (engine *nativeEngine) nativeSyncCatalogSnapshot(ctx context.Context, allowed []string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, catalogSnapshotTotalTimeout)
	defer cancel()
	body, err := engine.fetchCatalogSnapshot(ctx, "index.json", catalogSnapshotMaxIndex)
	if err != nil {
		return false, err
	}
	var index catalogSnapshotIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return false, errors.New("剧集快照索引无效")
	}
	if index.Version < 1 || index.GeneratedAt == "" || len(index.Sources) == 0 {
		return false, errors.New("剧集快照索引不完整")
	}

	// 把"用户启用的"与"这份构建里可用的"求交集，得到这一轮真正要灌的站源。
	available := make(map[string]bool, len(index.Sources))
	for source := range index.Sources {
		if source = canonicalProviderSource(source); source != "" && nativeSourceAvailable(source) {
			available[source] = true
		}
	}
	wanted := make([]string, 0, len(available))
	if len(allowed) == 0 {
		for source := range available {
			wanted = append(wanted, source)
		}
	} else {
		seen := make(map[string]bool, len(allowed))
		for _, source := range allowed {
			source = canonicalProviderSource(source)
			if source == "" || !available[source] || seen[source] {
				continue
			}
			seen[source] = true
			wanted = append(wanted, source)
		}
	}
	if len(wanted) == 0 {
		return false, nil
	}

	applied := engine.loadCatalogSnapshotState()
	sameVersion := applied.GeneratedAt == index.GeneratedAt
	if sameVersion && coveredBy(applied.Sources, wanted) {
		return false, nil
	}
	seeded := make([]string, 0, len(wanted)+len(applied.Sources))
	if sameVersion {
		// 同一版快照，只是用户又开了几个站源：保留已灌过的，补新的。
		seeded = append(seeded, applied.Sources...)
	}
	for _, source := range wanted {
		if ctx.Err() != nil {
			break
		}
		if sameVersion && containsSource(applied.Sources, source) {
			continue
		}
		asset, ok := index.Sources[source]
		if !ok {
			// 索引里可能是别名（例如 huangguo 的其它入口）。
			for key, candidate := range index.Sources {
				if canonicalProviderSource(key) == source {
					asset, ok = candidate, true
					break
				}
			}
		}
		if !ok || asset.File == "" {
			continue
		}
		if err := engine.seedCatalogSnapshot(ctx, source, asset); err != nil {
			// 单个站源失败不影响其他站源，也不影响现场抓取。
			continue
		}
		if !containsSource(seeded, source) {
			seeded = append(seeded, source)
		}
	}
	if len(seeded) == 0 {
		return false, nil
	}
	engine.saveCatalogSnapshotState(catalogSnapshotState{
		GeneratedAt: index.GeneratedAt,
		AppliedAt:   time.Now(),
		Sources:     seeded,
	})
	return true, nil
}

func containsSource(sources []string, target string) bool {
	for _, source := range sources {
		if source == target {
			return true
		}
	}
	return false
}

func coveredBy(have, wanted []string) bool {
	for _, source := range wanted {
		if !containsSource(have, source) {
			return false
		}
	}
	return true
}

func (engine *nativeEngine) seedCatalogSnapshot(ctx context.Context, source string, asset catalogSnapshotAsset) error {
	raw, err := engine.fetchCatalogSnapshot(ctx, asset.File, catalogSnapshotMaxFile)
	if err != nil {
		return err
	}
	if asset.SHA256 != "" {
		sum := sha256.Sum256(raw)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), asset.SHA256) {
			return errors.New("剧集快照校验失败")
		}
	}
	plain, err := decryptCatalogSnapshot(raw)
	if err != nil {
		return err
	}
	var payload catalogSnapshotPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return errors.New("剧集快照内容无法解析")
	}
	if len(payload.Items) == 0 {
		return errors.New("剧集快照为空")
	}
	items := make([]nativeDrama, 0, len(payload.Items))
	for _, item := range payload.Items {
		item = migrateNativeDrama(item)
		if item.ID == "" {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return errors.New("剧集快照没有有效剧集")
	}
	engine.mu.Lock()
	if engine.catalogs == nil {
		engine.catalogs = map[string][]nativeDrama{}
	}
	if engine.catalogStates == nil {
		engine.catalogStates = map[string]nativeCatalogState{}
	}
	// 快照打底、本地已有数据覆盖：本地可能有详情页补齐的封面 / 简介 / 集数。
	engine.catalogs[source] = mergeNativeCatalog(items, engine.catalogs[source])
	state := engine.catalogStates[source]
	state.Page = max(1, state.Page)
	state.HasMore = true
	// 视为"刚刚更新过"：15 分钟内首页直接吃缓存，不必再等一轮抓取。
	// 之后照常走原有的现场抓取 / 定时更新，快照只是把冷启动那一次省掉。
	state.UpdatedAt = time.Now()
	engine.catalogStates[source] = state
	// 灌完立刻落盘，进程被杀也不会白下这一份。
	err = engine.writeCatalogDiskLocked()
	engine.mu.Unlock()
	return err
}
