package core

import (
	"context"
	"errors"
	"time"
)

const (
	// 弹幕坐标保留时长。红果的剧集 id / 视频 id 基本不会变，留长一点没关系，
	// 只是防止无限增长。
	danmakuTargetTTL = 12 * time.Hour
	// 登记表上限。用户翻几十集也够用，超了就丢最老的。
	danmakuTargetLimit = 64
)

// nativeDanmakuTarget 是一集红果短剧的弹幕坐标（0.8.19）。
//
// 以前这两个 id 只存在播放会话里（`nativePlaybackChoice.danmakuSeries/Video`），
// 而播放会话会被三件事干掉：
//  1. `nativeOpenPlayback` 的 8 个会话上限（预载 / 预缓存都会占名额）；
//  2. `nativeReleasePlayback`（切集、退出播放页时前端会 release）；
//  3. 12 小时过期清理。
//
// 一旦会话没了，`nativeDanmaku` 就报"播放会话已过期"，前端静默退避 15 秒，
// 用户看到的就是"这一集没有弹幕"。现在坐标单独登记，与会话解耦。
type nativeDanmakuTarget struct {
	series  string
	video   string
	created time.Time
}

// rememberDanmakuTarget 登记一集的弹幕坐标（0.8.19）。
//
// 由 `nativeOpenPlayback`（真正播放）和 `nativePreload`（预载邻居集）共同调用，
// 所以切到还没播过的下一集时，弹幕坐标也已经在表里了。
func (engine *nativeEngine) rememberDanmakuTarget(seriesID, videoID string) {
	if seriesID == "" || videoID == "" {
		return
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.danmakuTargets == nil {
		engine.danmakuTargets = map[string]nativeDanmakuTarget{}
	}
	now := time.Now()
	// 先清过期项，再按上限淘汰最老的。
	for key, target := range engine.danmakuTargets {
		if now.Sub(target.created) > danmakuTargetTTL {
			delete(engine.danmakuTargets, key)
		}
	}
	if _, exists := engine.danmakuTargets[videoID]; !exists && len(engine.danmakuTargets) >= danmakuTargetLimit {
		oldestKey, oldest := "", now
		for key, target := range engine.danmakuTargets {
			if oldestKey == "" || target.created.Before(oldest) {
				oldestKey, oldest = key, target.created
			}
		}
		delete(engine.danmakuTargets, oldestKey)
	}
	engine.danmakuTargets[videoID] = nativeDanmakuTarget{series: seriesID, video: videoID, created: now}
}

// lookupDanmakuTarget 按视频 id 找弹幕坐标。
func (engine *nativeEngine) lookupDanmakuTarget(videoID string) (nativeDanmakuTarget, bool) {
	if videoID == "" {
		return nativeDanmakuTarget{}, false
	}
	engine.mu.Lock()
	target, found := engine.danmakuTargets[videoID]
	engine.mu.Unlock()
	if !found || time.Since(target.created) > danmakuTargetTTL {
		return nativeDanmakuTarget{}, false
	}
	return target, true
}

func (engine *nativeEngine) nativeDanmaku(ctx context.Context, input nativeInput) (hongguoDanmakuPage, error) {
	if input.StartMS < 0 || input.DurationMS <= input.StartMS || input.DurationMS > danmakuMaxDurationMS {
		return hongguoDanmakuPage{}, errors.New("弹幕时间范围无效")
	}
	// 优先按视频 id 找（0.8.19）：与会话无关，切集 / 预载顶掉会话都不影响。
	target, found := engine.lookupDanmakuTarget(input.DanmakuID)
	if !found {
		// 回落到播放会话（老前端不传 danmakuId 时的兼容路径）。
		engine.mu.Lock()
		choice, exists := engine.playbacks[input.PlaybackSession]
		engine.mu.Unlock()
		if !exists || time.Since(choice.created) > danmakuTargetTTL {
			return hongguoDanmakuPage{}, errors.New("弹幕信息已过期，重新播放本集即可")
		}
		target = nativeDanmakuTarget{series: choice.danmakuSeries, video: choice.danmakuVideo}
	}
	if target.series == "" || target.video == "" {
		return hongguoDanmakuPage{}, errors.New("本集暂不支持弹幕")
	}
	page, err := engine.downloader.hongguoDanmaku(ctx, target.series, target.video, input.StartMS, input.DurationMS)
	if err != nil {
		return hongguoDanmakuPage{}, err
	}
	// 0.8.19：**不再**在取回后再检查一次播放会话。
	// 以前这里要求"取弹幕期间会话还在"，而弹幕请求本身要 1~3 秒，
	// 这期间正好赶上切集 / 预载释放会话，就把已经拿到的数据丢掉了 ——
	// 又一个"有时候就是不出来"的来源。
	return page, ctx.Err()
}

// prefetchDanmaku 预热一集的弹幕第一屏（0.8.19）。
//
// 预载下一集时顺手把"开头那一窗"弹幕拉回来塞进缓存，切过去时弹幕和画面
// 一起就位，不会出现"画面已经动了、弹幕过两秒才飘出来"。
//
// 刻意**不返回错误**：弹幕只是辅助，预取失败绝不能影响预载 / 播放。
func (engine *nativeEngine) prefetchDanmaku(ctx context.Context, seriesID, videoID string, durationMS int64) {
	if seriesID == "" || videoID == "" {
		return
	}
	window := int64(danmakuWindowMS)
	if durationMS > 0 && durationMS < window {
		window = durationMS
	}
	if window <= 0 {
		return
	}
	// 独立的短超时：预载本身有时限，不能把时间耗在弹幕上。
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	// 标记成预取（0.8.22）：失败不写缓存，避免刚切过去就吃到一次瞬时失败。
	ctx = context.WithValue(ctx, danmakuPrefetchKey{}, true)
	_, _ = engine.downloader.hongguoDanmaku(ctx, seriesID, videoID, 0, window)
}
