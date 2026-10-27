package core

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 整集预缓存（0.8.12）。
//
// 与 `nativePreload` 的分工：
//   - `nativePreload`：**只解析地址**，顺手把开头 4MB 预热进当前播放会话的内存，
//     目标是"切过去第一帧快"。会话一换就没了。
//   - `nativePrecache`：把这一集的**全部分片**落到磁盘（`app_media_cache.go`），
//     目标是"切过去整集都不用等"。与播放会话解耦，切集、退出播放页都不丢。
//
// 预算（`Budget`）是硬约束：短剧一集 40~120 秒，分片加起来常见 10~40MB，
// 这里默认 48MB、上限 256MB，避免一集特别长的剧把整份缓存吃满。
const (
	nativePrecacheBudgetDefault = 48 << 20
	nativePrecacheBudgetMax     = 256 << 20
	// 单集最多拉多少个分片，防止恶意/异常播放列表把队列拖死。
	nativePrecacheSegmentMax = 400
	// 预缓存的**目标带宽**（字节/秒）。
	//
	// 0.8.12 首版是全速下载：一集 20~40MB 几分钟内拉完，而它和正在播放的
	// 那一路共用同一条出口 —— 播放器缓冲被打空，用户看到的就是
	// "看一会儿卡一下，退出再进又好了"。这里按 1.2MB/s 让路：
	// 短剧一集 40~120 秒，40MB 的集大约 33 秒下完，仍能在看下一集前准备好。
	nativePrecacheRate = 1200 << 10
	// 段间最小间隔，避免小分片把上游打成连发请求（部分站源会因此限流）。
	nativePrecacheGap = 60 * time.Millisecond
)

// nativePrecachePacer 按目标带宽给预缓存"踩刹车"。
//
// 做法是最朴素的匀速模型：已写 N 字节，就应该已经花掉 N/rate 秒；
// 实际花得更少，就补睡差额。不用令牌桶是因为这里没有并发，
// 单线程顺序消费，匀速模型就够，而且行为可预测、便于单测。
type nativePrecachePacer struct {
	started time.Time
	written int64
}

// wait 让路到"该花的时间"。返回 false 表示等待期间被取消。
func (pacer *nativePrecachePacer) wait(ctx context.Context) bool {
	if pacer == nil || pacer.written <= 0 {
		return ctx.Err() == nil
	}
	due := time.Duration(float64(pacer.written) / float64(nativePrecacheRate) * float64(time.Second))
	gap := due - time.Since(pacer.started)
	if gap < nativePrecacheGap {
		gap = nativePrecacheGap
	}
	// 单个分片很大时（例如 8MB 的 m4s）差额会到好几秒，
	// 截断到 1 秒即可：这种分片本身已经是"一大段"，不需要再严格匀速。
	if gap > time.Second {
		gap = time.Second
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (engine *nativeEngine) nativePrecache(ctx context.Context, input nativeInput) (map[string]any, error) {
	ctx = context.WithValue(ctx, backgroundCatalogKey{}, true)
	ctx = context.WithValue(ctx, nativePreloadKey{}, true)
	budget := input.Budget
	if budget <= 0 {
		budget = nativePrecacheBudgetDefault
	}
	if budget > nativePrecacheBudgetMax {
		budget = nativePrecacheBudgetMax
	}
	session, entry, owned, err := engine.nativePrecacheTarget(ctx, input)
	if err != nil {
		return nil, err
	}
	if session == "" {
		// 本地已有文件（下载过）或没有可用会话：无需缓存。
		return map[string]any{"bytes": 0}, nil
	}
	if owned {
		defer engine.nativeReleasePlayback(session)
	}
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	stream := engine.stream
	engine.mu.Unlock()
	if !exists || stream == nil || choice.streamSession == "" {
		return map[string]any{"bytes": 0}, nil
	}
	return map[string]any{"bytes": stream.nativePrecacheStream(ctx, choice.streamSession, entry, input.Quality, budget)}, nil
}

// nativePrecacheTarget 决定"缓存哪个播放会话"。
//
// 优先复用调用方已经预载好的会话（同一集已经在 `preload` 里解析过），
// 省掉一次解析请求；会话失效或没给，就自己解析一次，用完立即释放。
func (engine *nativeEngine) nativePrecacheTarget(ctx context.Context, input nativeInput) (session, entry string, owned bool, err error) {
	if input.ReuseSession != "" && input.Entry != "" {
		engine.mu.Lock()
		_, exists := engine.playbacks[input.ReuseSession]
		engine.mu.Unlock()
		if exists {
			return input.ReuseSession, input.Entry, false, nil
		}
	}
	plan, err := engine.nativeResolve(ctx, input)
	if err != nil {
		return "", "", false, err
	}
	if plan.Local || plan.Session == "" || plan.URL == "" {
		return "", "", false, nil
	}
	return plan.Session, plan.URL, true, nil
}

// nativePrecacheStream 走完一集的全部分片，逐块写入磁盘缓存。
//
// 返回实际写入的字节数。已经缓存过的分片会跳过（`load` 命中即视为已就绪），
// 所以重复调用不会重复下载。
func (stream *nativeStreamServer) nativePrecacheStream(ctx context.Context, token, entry string, quality int, budget int64) int64 {
	if budget <= 0 || token == "" || entry == "" {
		return 0
	}
	stream.mu.Lock()
	session := stream.sessions[token]
	stream.mu.Unlock()
	if session == nil {
		return 0
	}
	ctx, cancel := context.WithCancel(providerMediaContext(ctx, session.credentials))
	defer cancel()
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()

	written, segments := int64(0), 0
	pacer := &nativePrecachePacer{started: time.Now()}
	visited := map[string]bool{}
	var walk func(string, int) bool
	walk = func(local string, depth int) bool {
		if ctx.Err() != nil || written >= budget || segments >= nativePrecacheSegmentMax ||
			depth > 2 || visited[local] {
			return false
		}
		visited[local] = true
		parsed, err := url.Parse(local)
		if err != nil || !strings.HasPrefix(local, stream.address+"/"+token+"/") {
			return false
		}
		id := strings.TrimPrefix(parsed.Path, "/"+token+"/")
		session.mu.Lock()
		asset, exists := session.assets[id]
		session.mu.Unlock()
		if !exists {
			return false
		}
		// 播放列表的相对地址要以**跳转后的**地址为基准，否则相对分片会解析错。
		base := asset.address
		// 只认**完整正文**。
		//
		// `preload` 预热进来的是前 4MB 前缀（`total > len(data)`），
		// 若把它当成"已缓存"，开头几段就永远进不了磁盘缓存 ——
		// 播放时前几段仍要等网络，正是"切过去第一眼还是卡"的来源。
		complete := len(asset.data) > 0 && asset.total <= int64(len(asset.data))
		if !complete {
			if cached, hit := stream.media.load(asset.address); hit {
				asset, complete = cached, true
			}
		}
		if !complete {
			fetched, finalURL, ok := stream.nativeFetchFull(ctx, session, asset)
			if !ok {
				return false
			}
			asset, base = fetched, finalURL
			pacer.written += int64(len(asset.data))
			if stream.media.store(asset) {
				written += int64(len(asset.data))
				segments++
			}
		}
		playlist := strings.Contains(strings.ToLower(asset.contentType), "mpegurl") || looksLikePlaylist(asset.data)
		// **分片正文绝不回填 `session.assets`**。
		//
		// 这一段会话往往正是"马上要播的那一集"（`precache` 优先复用
		// `preload` 解析好的会话），把整集 30 段 × 2MB ≈ 60MB 塞进去，
		// 会一直常驻到换集为止；电视盒子上很容易被系统回收，
		// 表现就是"看两集之后开始卡，退出再进才好"。
		// 播放路径自己会从磁盘缓存读（见 `nativeServe`），不需要这份内存副本。
		//
		// 只有播放列表这类小正文值得留下：播放时要按会话重写分片地址。
		if playlist {
			session.mu.Lock()
			session.assets[id] = asset
			session.mu.Unlock()
		}
		if !playlist {
			return pacer.wait(ctx)
		}
		lines, _, _ := nativeHLSSelect(strings.Split(string(asset.data), "\n"), quality)
		rewritten, err := stream.nativeRewrite(token, session, strings.Join(lines, "\n"), base)
		if err != nil {
			return false
		}
		for _, raw := range strings.Split(rewritten, "\n") {
			line := strings.TrimSpace(raw)
			// 分片是同一个文件的不同字节区间，逐段缓存收益很低，直接停。
			if strings.HasPrefix(line, "#EXT-X-BYTERANGE:") {
				break
			}
			if strings.HasPrefix(line, "#EXT-X-KEY:") || strings.HasPrefix(line, "#EXT-X-MAP:") {
				for _, match := range nativePlaylistURI.FindAllStringSubmatch(line, -1) {
					if !strings.HasPrefix(match[1], "data:") {
						walk(match[1], depth+1)
					}
				}
			}
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !walk(line, depth+1) {
				break
			}
			if ctx.Err() != nil || written >= budget || segments >= nativePrecacheSegmentMax {
				break
			}
		}
		return true
	}
	walk(entry, 0)
	return written
}

// nativeFetchFull 拉一个完整正文（不带 Range）。
//
// 只接受 200：206 说明只拿到片段，存下来没有意义。返回值里第二个是
// **跳转后的最终地址**，仅用于给播放列表做相对地址基准。
func (stream *nativeStreamServer) nativeFetchFull(ctx context.Context, session *nativeStreamSession, asset nativeStreamAsset) (nativeStreamAsset, string, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.address, nil)
	if err != nil {
		return asset, asset.address, false
	}
	if session.referer != "" {
		request.Header.Set("Referer", session.referer)
	}
	response, err := stream.nativeRequest(request)
	if err != nil {
		return asset, asset.address, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return asset, asset.address, false
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, nativeMediaAssetMaxBytes+1))
	if readErr != nil || len(body) == 0 || len(body) > nativeMediaAssetMaxBytes {
		return asset, asset.address, false
	}
	finalURL := asset.address
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	asset.contentType = response.Header.Get("Content-Type")
	asset.etag, asset.modified = response.Header.Get("ETag"), response.Header.Get("Last-Modified")
	asset.data = body
	asset.total = int64(len(body))
	if strings.Contains(strings.ToLower(asset.contentType), "mpegurl") || looksLikePlaylist(body) {
		asset.contentType = "application/vnd.apple.mpegurl"
	}
	return asset, finalURL, true
}

// nativeMediaCacheStats / Clear / Limit 供设置页使用。
func (engine *nativeEngine) nativeMediaCacheStats() map[string]any {
	return engine.mediaCache().stats()
}

func (engine *nativeEngine) nativeClearMediaCache() map[string]any {
	cache := engine.mediaCache()
	cache.clear()
	return cache.stats()
}

func (engine *nativeEngine) nativeSetMediaCacheLimit(limit int64) map[string]any {
	cache := engine.mediaCache()
	cache.setLimit(limit)
	return cache.stats()
}
