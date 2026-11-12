package core

import (
	"encoding/json"
	"testing"
)

// 0.8.30：失败重试的「穿透缓存」是一条独立通道，不能和 force 混在一起。
// force 是 resolveOnline / 目录刷新用的，普通 resolve 永远不该带 bypassCache。
func TestNativeInputBypassCacheDecoding(t *testing.T) {
	var fresh nativeInput
	if err := json.Unmarshal([]byte(`{"action":"resolve","force":false,"bypassCache":true}`), &fresh); err != nil {
		t.Fatal(err)
	}
	if !fresh.BypassCache || fresh.Force {
		t.Fatalf("bypassCache 与 force 必须互相独立：%+v", fresh)
	}

	var normal nativeInput
	if err := json.Unmarshal([]byte(`{"action":"resolve"}`), &normal); err != nil {
		t.Fatal(err)
	}
	if normal.BypassCache {
		t.Fatal("普通 resolve 不该带 bypassCache，正常切集必须继续命中缓存")
	}
}

func TestNativeForgetPlaybackCache(t *testing.T) {
	const (
		seriesID = "7703870387817303064"
		videoID  = "7703870387817303065"
		otherID  = "7703870387817303066"
	)
	task := Task{
		DramaID: "hongguo:" + seriesID,
		Chapter: Chapter{
			Source:   sourceHongguo,
			VideoURL: "hongguo-cenc://" + videoID,
		},
	}
	downloader := &Downloader{}
	seed := func() {
		downloader.hongguoMediaCachePut(videoID, providerMedia{URL: "https://example.invalid/stale.mp4"})
		downloader.hongguoMediaCachePut(otherID, providerMedia{URL: "https://example.invalid/other.mp4"})
	}

	// 正常播放路径：短缓存照旧命中，新字段不能误伤连播的零请求优化。
	seed()
	nativeForgetPlaybackCache(downloader, task, false)
	if cached, ok := downloader.hongguoMediaCacheGet(videoID); !ok || cached.URL == "" {
		t.Fatal("正常取流不该清掉短缓存")
	}

	// 失败重试：只作废本集，别的集不受影响。
	nativeForgetPlaybackCache(downloader, task, true)
	if _, ok := downloader.hongguoMediaCacheGet(videoID); ok {
		t.Fatal("重试没有作废本集缓存，页面内会原样拿回坏地址")
	}
	if _, ok := downloader.hongguoMediaCacheGet(otherID); !ok {
		t.Fatal("重试误伤了其它集的缓存")
	}

	// 非红果站源没有 hongguo-cenc:// 的 videoID，必须是纯 no-op。
	seed()
	foreign := Task{
		DramaID: "huangdou:123",
		Chapter: Chapter{Source: "huangdou", VideoURL: "https://example.invalid/a.mp4"},
	}
	nativeForgetPlaybackCache(downloader, foreign, true)
	if _, ok := downloader.hongguoMediaCacheGet(videoID); !ok {
		t.Fatal("非红果任务不该动红果缓存")
	}

	// 极端情况下 downloader 为空也不能 panic（重试路径不该变成崩溃）。
	nativeForgetPlaybackCache(nil, task, true)
}
