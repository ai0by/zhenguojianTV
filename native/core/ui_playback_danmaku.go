package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const danmakuWindowMS = 30_000
const danmakuMaxDurationMS = 24 * 60 * 60 * 1000

type hongguoDanmakuItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	TimeMS int64  `json:"timeMs"`
}

type hongguoDanmakuPage struct {
	EpisodeID string               `json:"episodeId"`
	Items     []hongguoDanmakuItem `json:"items"`
	StartMS   int64                `json:"startMs"`
	NextMS    int64                `json:"nextMs"`
	Total     int64                `json:"total"`
}

type hongguoDanmakuCacheEntry struct {
	page    hongguoDanmakuPage
	err     error
	expires time.Time
}

type hongguoDanmakuCall struct {
	done  chan struct{}
	entry hongguoDanmakuCacheEntry
}

// danmakuPrefetchKey 标记"这一发是预取，不是用户当下要的数据"（0.8.22）。
//
// 预取发生在切到这一集**之前**，那时服务端就算偶发降级也没人在等。如果把
// 预取的失败也写进失败缓存（1.5 秒），用户紧接着切到这一集就会直接吃到那份
// 失败 —— 明明重新问一次就有数据，却先给了一个错误。所以预取只在成功时
// 写缓存，失败时静默丢弃。
type danmakuPrefetchKey struct{}

func hongguoPlaybackIDs(task Task) (string, string, bool) {
	source, seriesID, valid := splitProviderDramaID(task.DramaID)
	if !valid || source != sourceHongguo || task.Chapter.Source != sourceHongguo || !strings.HasPrefix(task.Chapter.VideoURL, "hongguo-cenc://") {
		return "", "", false
	}
	videoID := strings.TrimPrefix(task.Chapter.VideoURL, "hongguo-cenc://")
	return seriesID, videoID, hongguoNumericID.MatchString(seriesID) && hongguoNumericID.MatchString(videoID)
}

func (d *Downloader) hongguoDanmaku(ctx context.Context, seriesID, videoID string, start, duration int64) (hongguoDanmakuPage, error) {
	if err := ctx.Err(); err != nil {
		return hongguoDanmakuPage{}, err
	}
	if !hongguoNumericID.MatchString(seriesID) || !hongguoNumericID.MatchString(videoID) || start < 0 || duration <= start || duration > danmakuMaxDurationMS {
		return hongguoDanmakuPage{}, errors.New("弹幕请求参数无效")
	}
	// 0.8.21：缓存键不再包含"整集时长"。
	//
	// 预取（`prefetchDanmaku`）发生在切到这一集之前，那时还不知道真实时长，
	// 只能按 30 秒窗口请求；而播放时的请求带的是真实时长。键里带时长，两份
	// 数据就永远对不上 —— 预取等于白做，用户切集后照样要等一次网络往返。
	// 现在按"集 + 视频 + 起点"缓存，命中时再按本次时长裁剪窗口。
	key := fmt.Sprintf("%s:%s:%d", seriesID, videoID, start)
	client := d.hongguoClient()
	client.mu.Lock()
	entry, found := client.danmaku[key]
	client.mu.Unlock()
	if found && time.Now().Before(entry.expires) {
		if entry.err != nil {
			return cloneDanmakuPage(entry.page), entry.err
		}
		if page, ok := fitDanmakuPage(entry.page, start, duration); ok {
			return page, nil
		}
		// 缓存的这一页装不进本次时长（极少见：片长短于 30 秒），重新取。
	}
	return d.hongguoDanmakuFetch(ctx, seriesID, videoID, start, duration, key)
}

// fitDanmakuPage 把缓存页适配到本次请求的时长上。
//
// 命中缓存的前提是"起点一致、窗口有长度、且不超过本次时长"。缓存的窗口比
// 本次时长更长时（预取不知道真实时长），裁掉超出部分即可，不需要重发请求。
func fitDanmakuPage(page hongguoDanmakuPage, start, duration int64) (hongguoDanmakuPage, bool) {
	if page.StartMS != start || page.NextMS <= start || start >= duration {
		return hongguoDanmakuPage{}, false
	}
	cloned := cloneDanmakuPage(page)
	if cloned.NextMS > duration {
		cloned.NextMS = duration
		kept := cloned.Items[:0]
		for _, item := range cloned.Items {
			if item.TimeMS < cloned.NextMS {
				kept = append(kept, item)
			}
		}
		cloned.Items = kept
	}
	return cloned, true
}

// hongguoDanmakuFetch 是真正的取弹幕流程（同键去重 + 写缓存）。
//
// 缓存命中由 `hongguoDanmaku` 负责判断（还要按本次时长裁剪），这里只看
// "同键是否已有请求在跑"，避免同一窗口被连点打出好几发。
func (d *Downloader) hongguoDanmakuFetch(ctx context.Context, seriesID, videoID string, start, duration int64, key string) (hongguoDanmakuPage, error) {
	client := d.hongguoClient()
	client.mu.Lock()
	if pending := client.danmakuPending[key]; pending != nil {
		client.mu.Unlock()
		select {
		case <-ctx.Done():
			return hongguoDanmakuPage{}, ctx.Err()
		case <-pending.done:
			if ctx.Err() == nil && (errors.Is(pending.entry.err, context.Canceled) || errors.Is(pending.entry.err, context.DeadlineExceeded)) {
				return d.hongguoDanmaku(ctx, seriesID, videoID, start, duration)
			}
			if pending.entry.err == nil {
				// 等到的这一发可能是别的调用方按不同时长发起的，窗口要按
				// 本次时长适配；装不下就自己重新取一发，别把越界的窗口
				// 交给前端（前端会判成"时间范围无效"并一直重试）。
				if page, ok := fitDanmakuPage(pending.entry.page, start, duration); ok {
					return page, nil
				}
				return d.hongguoDanmaku(ctx, seriesID, videoID, start, duration)
			}
			return cloneDanmakuPage(pending.entry.page), pending.entry.err
		}
	}
	if client.danmakuPending == nil {
		client.danmakuPending = map[string]*hongguoDanmakuCall{}
	}
	if len(client.danmakuPending) >= 16 {
		client.mu.Unlock()
		return hongguoDanmakuPage{}, errors.New("弹幕请求繁忙")
	}
	pending := &hongguoDanmakuCall{done: make(chan struct{})}
	client.danmakuPending[key] = pending
	client.mu.Unlock()
	ctx = context.WithValue(ctx, backgroundCatalogKey{}, true)
	ctx = context.WithValue(ctx, hongguoCommentKey{}, true)
	body := map[string]any{
		"comment_source": 601, "server_channel": 1000, "group_id": videoID, "group_type": 30,
		"comment_type": 20, "sort": 1, "count": 90, "cursor": "", "aid": 8662, "compliance_status": 0,
		"business_param": map[string]any{"book_id": seriesID, "start_offset_time": start, "playlet_item_duration": duration, "need_danmaku_guide_type": []int{1, 3, 4, 2}},
	}
	result, err := d.hongguoAppRequest(ctx, http.MethodPost, "/novel/commentapi/comment/list/"+videoID+"/v1/", nil, body)
	var page hongguoDanmakuPage
	if err == nil {
		page, err = parseHongguoDanmaku(result, videoID, start, duration)
	}
	entry := hongguoDanmakuCacheEntry{page: page, err: err, expires: time.Now().Add(5 * time.Minute)}
	if err != nil {
		// 0.8.21：失败缓存 3s → 1.5s。
		//
		// 前端第一次失败 2 秒后重试，3 秒的错误缓存会让这次重试**必然**拿到
		// 同一个错误、白白把失败计数 +1（退避直接翻到 4 秒）。1.5 秒短于前端
		// 的 2 秒，重试才真的会重新打一次网络。
		entry.expires = time.Now().Add(1500 * time.Millisecond)
	}
	client.mu.Lock()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		// 预取失败不写缓存（0.8.22）：用户马上就要切过来，让正式请求重新问
		// 一次比把这份瞬时失败递给他更有用。
		prefetch, _ := ctx.Value(danmakuPrefetchKey{}).(bool)
		if !prefetch || err == nil {
			// 0.8.19：128 → 256 条。弹幕按"集 + 时间窗"缓存，一集 24 分钟
			// 就是 48 个窗口；预载预热 + 来回切集很容易把 128 挤爆。
			if client.danmaku == nil || len(client.danmaku) >= 256 {
				client.danmaku = map[string]hongguoDanmakuCacheEntry{}
			}
			client.danmaku[key] = entry
		}
	}
	pending.entry = entry
	delete(client.danmakuPending, key)
	close(pending.done)
	client.mu.Unlock()
	return cloneDanmakuPage(page), err
}

func cloneDanmakuPage(page hongguoDanmakuPage) hongguoDanmakuPage {
	page.Items = append([]hongguoDanmakuItem{}, page.Items...)
	return page
}

// emptyDanmakuWindow 造一个"这一窗确实没有弹幕"的合法结果（0.8.22）。
//
// 服务端对"这一段没人发弹幕"有好几种表达：空数组、缺字段、干脆整块 `data`
// 都不给。它们都不该被当成失败 —— 一旦判成失败，前端会标红并反复退避重试
// 一个永远不会成功的请求，用户看到的正是"这一集弹幕加载失败"。
func emptyDanmakuWindow(videoID string, start, duration int64) (hongguoDanmakuPage, error) {
	next := start + danmakuWindowMS
	if next > duration {
		next = duration
	}
	if next <= start {
		return hongguoDanmakuPage{}, errors.New("红果弹幕时间段格式无效")
	}
	return hongguoDanmakuPage{
		EpisodeID: videoID,
		Items:     []hongguoDanmakuItem{},
		StartMS:   start,
		NextMS:    next,
	}, nil
}

func parseHongguoDanmaku(result map[string]any, videoID string, start, duration int64) (hongguoDanmakuPage, error) {
	data := nestedMap(result, "data")
	rows, hasRows := data["data_list"].([]any)
	if !hasRows {
		// 0.8.22：整集都没有弹幕数据时，接口可能连 `data` / `data_list`
		// 都不下发（实测冷门剧就是这样）。这种"根本没有这个字段"的情况
		// 与"这一段没人发"是同一件事，按空窗口处理；只有**字段在、内容
		// 不是数组**才是真的格式异常。
		if _, present := data["data_list"]; !present {
			return emptyDanmakuWindow(videoID, start, duration)
		}
		return hongguoDanmakuPage{}, errors.New("红果弹幕时间段格式无效")
	}
	next, err := strconv.ParseInt(mapString(nestedMap(data, "extra"), "next_query_danmaku_list_time"), 10, 64)
	if err != nil || next <= start || next > danmakuMaxDurationMS {
		// 0.8.21：空窗口不是错误。
		//
		// 片头片尾、冷门片段经常一条弹幕都没有，这时服务端可能干脆不下发
		// next_query_danmaku_list_time。以前直接判"格式无效"，前端就会把
		// 这一窗标成"弹幕暂不可用"并按失败退避反复重试 —— 用户看到的是
		// "这一集弹幕没加载上"，其实只是这一段没人发。
		if len(rows) > 0 {
			return hongguoDanmakuPage{}, errors.New("红果弹幕时间段格式无效")
		}
		return emptyDanmakuWindow(videoID, start, duration)
	}
	page := hongguoDanmakuPage{EpisodeID: videoID, Items: []hongguoDanmakuItem{}, StartMS: start, NextMS: min(next, duration)}
	var cursor struct {
		Total int64 `json:"danmaku_count"`
	}
	if raw := mapString(nestedMap(data, "common_list_info"), "cursor"); len(raw) <= 8192 && json.Unmarshal([]byte(raw), &cursor) == nil && cursor.Total >= 0 {
		page.Total = cursor.Total
	}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		comment := nestedMap(row, "comment")
		common := nestedMap(comment, "common")
		if mapString(common, "group_id") != videoID || mapString(common, "status") != "1" {
			continue
		}
		position, err := strconv.ParseInt(mapString(nestedMap(comment, "expand"), "offset_time"), 10, 64)
		text := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return ' '
			}
			return r
		}, expandDanmakuEmoji(mapString(nestedMap(common, "content"), "text"))))
		id := mapString(comment, "comment_id")
		if err != nil || position < start || position >= page.NextMS || text == "" || id == "" || len(id) > 120 || seen[id] {
			continue
		}
		if runes := []rune(text); len(runes) > 180 {
			text = trimDanmakuText(text, 180) + "…"
		}
		seen[id] = true
		page.Items = append(page.Items, hongguoDanmakuItem{ID: id, Text: text, TimeMS: position})
		if len(page.Items) == 90 {
			break
		}
	}
	sort.SliceStable(page.Items, func(i, j int) bool { return page.Items[i].TimeMS < page.Items[j].TimeMS })
	return page, nil
}
