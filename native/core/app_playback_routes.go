package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

type nativePlaybackChoice struct {
	media         []providerMedia
	index         int
	qualities     []int
	streamSession string
	created       time.Time
	// 弹幕（0.8.11）：红果专属。播放会话建立时把剧集 id 和视频 id 记下来，
	// 前端按会话拉弹幕，就不必再传一遍这两个 id（也避免传错）。
	danmakuSeries string
	danmakuVideo  string
}

func nativePlaybackChoices(media providerMedia, quality int) nativePlaybackChoice {
	choice := nativePlaybackChoice{qualities: []int{}}
	seen := map[string]bool{}
	qualities := map[int]bool{}
	for _, option := range append([]providerMedia{media}, media.Variants...) {
		if !isProviderHTTPMediaURL(option.URL) {
			continue
		}
		identity := option.URL + "\x00" + option.Referer + "\x00" + hex.EncodeToString(option.CENCKey) + "\x00" + hex.EncodeToString(option.HLSKey)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		option.Variants = nil
		choice.media = append(choice.media, option)
		if option.Quality > 0 {
			qualities[option.Quality] = true
		}
	}
	sort.SliceStable(choice.media, func(i, j int) bool { return choice.media[i].Quality > choice.media[j].Quality })
	for value := range qualities {
		choice.qualities = append(choice.qualities, value)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(choice.qualities)))
	if quality > 0 && qualities[quality] {
		filtered := make([]providerMedia, 0, len(choice.media))
		for _, option := range choice.media {
			if option.Quality == quality {
				filtered = append(filtered, option)
			}
		}
		choice.media = filtered
	}
	return choice
}

func (engine *nativeEngine) nativeOpenPlayback(ctx context.Context, choice nativePlaybackChoice) (nativePlan, error) {
	if err := ctx.Err(); err != nil {
		return nativePlan{}, err
	}
	if choice.index < 0 || choice.index >= len(choice.media) {
		return nativePlan{}, errors.New("该集没有其他可用的播放线路")
	}
	media := choice.media[choice.index]
	var err error
	if !isProviderHTTPMediaURL(media.URL) {
		return nativePlan{}, errors.New("站源未返回有效的播放地址")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nativePlan{}, errors.New("无法初始化播放会话")
	}
	plan := nativePlan{
		URL: media.URL, Headers: map[string]string{"User-Agent": userAgent, "Referer": media.Referer},
		Key: hex.EncodeToString(media.CENCKey), Quality: media.Quality, Qualities: choice.qualities,
		RouteIndex: choice.index, RouteCount: len(choice.media), Session: hex.EncodeToString(tokenBytes),
		DanmakuID: choice.danmakuVideo,
	}
	if media.credentials != nil && !media.credentials.expires.IsZero() {
		if !time.Now().Before(media.credentials.expires) {
			return nativePlan{}, errors.New("播放凭证已过期，请重新解析播放")
		}
		plan.ExpiresAt = media.credentials.expires.UnixMilli()
	}
	choice.streamSession = ""
	var stream *nativeStreamServer
	{
		// 先拿缓存再进锁：`mediaCache()` 自己会加 `engine.mu`，不能嵌套。
		cache := engine.mediaCache()
		engine.mu.Lock()
		if engine.stream == nil {
			engine.stream, err = newNativeStreamServer(engine.downloader, cache)
		}
		stream = engine.stream
		engine.mu.Unlock()
		if err != nil {
			return nativePlan{}, err
		}
		plan.URL, choice.streamSession = stream.nativeOpen(media)
	}
	choice.created = time.Now()
	engine.mu.Lock()
	if engine.playbacks == nil {
		engine.playbacks = map[string]nativePlaybackChoice{}
	}
	var expired []string
	for token, old := range engine.playbacks {
		if time.Since(old.created) > 12*time.Hour {
			expired = append(expired, old.streamSession)
			delete(engine.playbacks, token)
		}
	}
	// 与 `nativeOpen` 的会话上限同理（0.8.13）：上限只用来防无限增长，
	// **不能淘汰正在播的那一路**。首版按 `created` 挑最老的第 9 个淘汰，
	// 预缓存多开几个会话就可能顶到上限，把用户正在看的播放当场释放
	// （`nativeRelease` 会 cancel 掉 stream 会话，播放器随即收到 410）。
	//
	// 现在跳过"正在被播放器使用"的会话（判定见 `nativeHot`）；
	// 全都热就允许临时超一点。注意锁序：这里已持有 `engine.mu`，
	// 再取 `stream.mu` —— 反方向（持 `stream.mu` 取 `engine.mu`）的路径不存在。
	if len(engine.playbacks) >= 8 {
		oldest := ""
		for token, old := range engine.playbacks {
			if stream.nativeHot(old.streamSession, 2*time.Minute) {
				continue
			}
			if oldest == "" || old.created.Before(engine.playbacks[oldest].created) {
				oldest = token
			}
		}
		if oldest != "" {
			expired = append(expired, engine.playbacks[oldest].streamSession)
			delete(engine.playbacks, oldest)
		}
	}
	engine.playbacks[plan.Session] = choice
	engine.mu.Unlock()
	if stream != nil {
		for _, token := range expired {
			stream.nativeRelease(token)
		}
	}
	if err := ctx.Err(); err != nil {
		engine.nativeReleasePlayback(plan.Session)
		return nativePlan{}, err
	}
	return plan, nil
}

func (engine *nativeEngine) nativeNextPlayback(ctx context.Context, session string) (nativePlan, error) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	engine.mu.Unlock()
	if !exists {
		return nativePlan{}, errors.New("播放线路已失效，请重新解析播放")
	}
	choice.index++
	return engine.nativeOpenPlayback(ctx, choice)
}

func (engine *nativeEngine) nativeReleasePlayback(session string) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	delete(engine.playbacks, session)
	stream := engine.stream
	engine.mu.Unlock()
	if exists && stream != nil && choice.streamSession != "" {
		stream.nativeRelease(choice.streamSession)
	}
}
