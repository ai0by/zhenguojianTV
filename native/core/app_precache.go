package core

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
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
)

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
		playlist := strings.Contains(asset.contentType, "mpegurl")
		if len(asset.data) == 0 {
			if cached, hit := stream.media.load(asset.address); hit {
				asset = cached
				session.mu.Lock()
				session.assets[id] = asset
				session.mu.Unlock()
			} else {
				fetched, finalURL, ok := stream.nativeFetchFull(ctx, session, asset)
				if !ok {
					return false
				}
				asset, base = fetched, finalURL
				session.mu.Lock()
				session.assets[id] = asset
				session.mu.Unlock()
				if stream.media.store(asset) {
					written += int64(len(asset.data))
					segments++
				}
			}
			playlist = playlist || strings.Contains(strings.ToLower(asset.contentType), "mpegurl") || looksLikePlaylist(asset.data)
		}
		if !playlist {
			return true
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
