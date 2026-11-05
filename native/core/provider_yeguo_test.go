package core

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// 详情响应里 related_list 挂着别的剧（同样带 episodes），剧级对象必须锚定在
// 请求的那一部上，否则分集标题会串到别的剧。
func TestYeguoDramaObjectAnchorsOnRequestedDrama(t *testing.T) {
	root := map[string]any{
		"video_id": "3704",
		"title":    "目标剧",
		"episodes": []any{
			map[string]any{"id": "188395", "sort": float64(1), "title": "第1集"},
		},
		"related_list": []any{
			map[string]any{
				"video_id": "3429",
				"title":    "别的剧",
				"episodes": []any{
					map[string]any{"id": "186807", "sort": float64(1), "title": "如何成为鸭王"},
				},
			},
		},
	}
	meta := yeguoDramaObject(root, "3704")
	if meta == nil {
		t.Fatal("yeguoDramaObject returned nil")
	}
	if got := yeguoText(meta, "title"); got != "目标剧" {
		t.Fatalf("anchored on %q, want 目标剧", got)
	}
	rows := yeguoEpisodeRows(meta)
	if len(rows) != 1 || rows[0].id != "188395" {
		t.Fatalf("episodes = %#v, want only the requested drama's episode", rows)
	}
}

// 分集真实字段是 id（不是 episode_id），序号是 sort；缺 sort 时按数组顺序补。
func TestYeguoEpisodeRowsUseIDAndSort(t *testing.T) {
	drama := map[string]any{
		"episodes": []any{
			map[string]any{"id": "ep-2", "sort": float64(2), "title": "第二集"},
			map[string]any{"id": "ep-1", "sort": float64(1), "title": "第一集"},
			map[string]any{"id": "ep-3", "title": "第三集"},
			map[string]any{"id": "ep-2", "sort": float64(2), "title": "重复"},
			map[string]any{"id": "ep-ad", "is_adv": true, "title": "广告"},
		},
	}
	rows := yeguoEpisodeRows(drama)
	if len(rows) != 3 {
		t.Fatalf("got %d episodes, want 3 (dedup + skip ads): %#v", len(rows), rows)
	}
	wantIDs := []string{"ep-1", "ep-2", "ep-3"}
	for index, want := range wantIDs {
		if rows[index].id != want {
			t.Fatalf("episode %d = %q, want %q (rows=%#v)", index, rows[index].id, want, rows)
		}
	}
	if rows[2].index != 3 {
		t.Fatalf("episode without sort got index %d, want 3 (array position)", rows[2].index)
	}
}

// 站点把 content_access_type 分 free/coin/vip，但播放接口并不拦付费，
// 所以列表与详情都不该把剧标成 VIP（否则详情页会弹"这是一集 VIP 内容"）。
// 0.8.24：翻页翻到空是**正常收尾**，不是错误。
//
// 目录只有 52 页，第 53 页起服务端回空列表。以前这里也抛"野果暂未返回剧集"，
// catalogdump 于是把整站（1033 条）判成失败 → 快照只能沿用上一轮，永远停在
// 旧数据。搜索场景仍然要报错，否则 UI 会把站点故障显示成"没有这部剧"。
func TestYeguoCatalogEmptyPageIsNotAnError(t *testing.T) {
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{}}`), nil
		}
		// 任何路径都回空列表，模拟"翻到最后一页之后"。
		return sourceFixtureResponse(request, http.StatusOK, `{"items":[]}`), nil
	})
	items, more, err := downloader.fetchYeguoCatalogPage(context.Background(), 53, "")
	if err != nil {
		t.Fatalf("翻到空页不该报错：%v", err)
	}
	if len(items) != 0 {
		t.Fatalf("空页不该有数据，实际 %d 条", len(items))
	}
	if more {
		t.Fatal("空页之后不该还有更多")
	}
}

func TestYeguoCatalogDoesNotMarkPaidDramasAsVip(t *testing.T) {
	root := map[string]any{
		"items": []any{
			map[string]any{"video_id": "3704", "title": "免费剧", "content_access_type": "free"},
			map[string]any{"video_id": "3429", "title": "金币剧", "content_access_type": "coin"},
			map[string]any{"video_id": "3516", "title": "会员剧", "content_access_type": "vip"},
		},
	}
	items := yeguoDramas(root)
	if len(items) != 3 {
		t.Fatalf("got %d dramas, want 3", len(items))
	}
	for _, item := range items {
		if item.VIP == nil || *item.VIP {
			t.Fatalf("%q marked VIP, want playable: %#v", item.Title, item.VIP)
		}
	}
}

// 0.8.10：密钥发现必须跳过「源级并发门」。
//
// 真机现象：野果点播永远失败，报「站点当前不可用」，而单独跑发现只要 2 秒。
// 根因是 `yeguoAPICall` 读完响应、还没关 body 时就去解密，解密要触发密钥发现，
// 发现又要发请求 —— 但源级并发门还被这次请求占着，于是发现一直在等自己，
// 直到超时。这里把门手动占住，验证发现仍能完成。
func TestYeguoCryptoDiscoveryIgnoresSourceGate(t *testing.T) {
	resetProviderMirrorCaches()
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{}}`), nil
		}
		switch {
		case request.URL.Path == "/":
			return sourceFixtureResponse(request, http.StatusOK,
				`<html><head><script type="module" src="/_nuxt/config.js"></script></head><body></body></html>`), nil
		case request.URL.Path == "/_nuxt/config.js":
			return sourceFixtureResponse(request, http.StatusOK,
				"var n={mode:'CBC',key:t('2acf7e91e9864673'),iv:t('1c29882d3ddfcfd6'),sign_key:t('5589d41f92a597d016b037ac37db243d')};"), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})
	// 把野果源的门占住，并且把请求间隔调到很大：只要发现请求走了门，就一定超时。
	downloader.limiter.interval = time.Minute
	downloader.limiter.mu.Lock()
	downloader.limiter.gates[sourceYeguo] = &requestGate{busy: true}
	downloader.limiter.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config, err := downloader.discoverYeguoCrypto(ctx)
	if err != nil {
		t.Fatalf("discovery must bypass the source gate, got %v", err)
	}
	if config.Key != "2acf7e91e9864673" || config.IV != "1c29882d3ddfcfd6" {
		t.Fatalf("unexpected crypto config: %#v", config)
	}
}
