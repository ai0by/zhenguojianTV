package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestXiangjiaoJSONRetriesForbiddenPrimaryMirror(t *testing.T) {
	resetProviderMirrorCaches()
	var primaryCalls atomic.Int32
	var mirrorCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{"xiangjiao":["https://mirror.example.test"]}}`), nil
		}
		if request.URL.Hostname() == "xiangjiaoai.ai" {
			primaryCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusForbidden, `{"code":"EDGE_BLOCKED","message":"upstream denied"}`), nil
		}
		if request.URL.Hostname() == "mirror.example.test" {
			mirrorCalls.Add(1)
			if got := request.Header.Get("Origin"); got != "https://mirror.example.test" {
				t.Errorf("Origin = %q, want mirror origin", got)
			}
			if got := request.Header.Get("Referer"); got != "https://mirror.example.test/" {
				t.Errorf("Referer = %q, want mirror referer", got)
			}
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"access_token":"fixture-token"}}`), nil
		}
		return nil, fmt.Errorf("unexpected host %q", request.URL.Hostname())
	})
	downloader.providerMu.Lock()
	if downloader.providerHosts == nil {
		downloader.providerHosts = map[string]string{}
	}
	downloader.providerHosts[sourceXiangjiao] = "https://xiangjiaoai.ai"
	downloader.providerMu.Unlock()

	decoded, err := downloader.xiangjiaoJSON(context.Background(), http.MethodPost, "/api/guest-sessions", map[string]any{"device_id": "fixture-device"}, "")
	if err != nil {
		t.Fatalf("xiangjiaoJSON: %v", err)
	}
	data, _ := xiangjiaoPayloadData(decoded).(map[string]any)
	if xiangjiaoText(data, "access_token") != "fixture-token" {
		t.Fatalf("unexpected mirror response: %#v", decoded)
	}
	if primaryCalls.Load() == 0 || mirrorCalls.Load() == 0 {
		t.Fatalf("expected both primary and remote mirror to be tried; primary=%d mirror=%d", primaryCalls.Load(), mirrorCalls.Load())
	}
}

func TestXiangjiaoCatalogKeepsCoinLockedDramasVisible(t *testing.T) {
	resetProviderMirrorCaches()
	var playbackCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{}}`), nil
		}
		if request.Method == http.MethodGet && request.URL.Path == "/api/home/hot" {
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"title":"热播","items":[{"id":"coin-drama-fixture","content_type":"drama","title":"付费短剧","image_url":"https://media.example.test/cover.jpg","drama":{"id":"coin-drama-fixture","title":"付费短剧","cover_url":"https://media.example.test/cover.jpg","commerce":{"charge_type":"coin"},"episode_count":2}}]}}`), nil
		}
		if request.URL.Path == "/api/playback/sessions" {
			playbackCalls.Add(1)
		}
		return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
	})

	items, more, err := downloader.fetchXiangjiaoCatalogPage(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("fetchXiangjiaoCatalogPage: %v", err)
	}
	if more || len(items) != 1 {
		t.Fatalf("catalog returned more=%v, items=%d; want one visible drama", more, len(items))
	}
	if items[0].Title != "付费短剧" || items[0].VIP == nil || !*items[0].VIP {
		t.Fatalf("coin commerce metadata was not preserved: %#v", items[0])
	}
	if playbackCalls.Load() != 0 {
		t.Fatalf("catalog loading must not probe paid playback sessions, got %d", playbackCalls.Load())
	}
}

func TestXiangjiaoDetailMarksCoinEpisodesLocked(t *testing.T) {
	resetProviderMirrorCaches()
	const sourceID = "detail-fixture"
	var playbackCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{}}`), nil
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/dramas/"+sourceID:
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"id":"detail-fixture","title":"香蕉测试剧","commerce":{"charge_type":"coin"}}}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/api/dramas/"+sourceID+"/episodes":
			return sourceFixtureResponse(request, http.StatusOK, `{"data":[
				{"id":"ep-locked","episode_number":1,"drama_id":"detail-fixture","title":"第1集","commerce":{"charge_type":"coin","can_play":false,"owned":false,"reason":"purchase_required"}},
				{"id":"ep-free","episode_number":2,"drama_id":"detail-fixture","title":"第2集","commerce":{"charge_type":"free","can_play":true,"owned":false}}
			]}`), nil
		case request.URL.Path == "/api/playback/sessions":
			playbackCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusForbidden, ""), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})

	drama, chapters, err := downloader.fetchXiangjiaoDetail(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("fetchXiangjiaoDetail: %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("got %d chapters, want 2", len(chapters))
	}
	if !chapters[0].VIP || chapters[1].VIP {
		t.Fatalf("episode lock flags incorrect: first=%v second=%v", chapters[0].VIP, chapters[1].VIP)
	}
	if drama.VIP == nil || *drama.VIP {
		t.Fatalf("a drama with at least one free episode should not be marked entirely VIP: %#v", drama.VIP)
	}
	if playbackCalls.Load() != 0 {
		t.Fatalf("detail loading must not call playback sessions for locked episodes, got %d", playbackCalls.Load())
	}
}

func TestXiangjiaoPlayback403IsTreatedAsPaymentDenial(t *testing.T) {
	resetProviderMirrorCaches()
	const sourceID = "payment-fixture"
	var playbackCalls atomic.Int32
	var mirrorPlaybackCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == providerMirrorConfigURL {
			return sourceFixtureResponse(request, http.StatusOK, `{"version":1,"ttl_seconds":3600,"sources":{}}`), nil
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/guest-sessions":
			return sourceFixtureResponse(request, http.StatusCreated, `{"data":{"access_token":"fixture-token","expires_at":"2099-01-01T00:00:00Z"}}`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/playback/sessions" && request.URL.Hostname() == "xiangjiaoai.ai":
			playbackCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusForbidden, ""), nil
		case request.URL.Path == "/api/playback/sessions":
			mirrorPlaybackCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"play_mode":"full","media":{"manifest_url":"https://media.example.test/ep.m3u8"}}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})

	_, err := downloader.xiangjiaoFullPlaybackManifest(context.Background(), sourceID, "ep-locked")
	if err == nil || !strings.Contains(err.Error(), "会员") {
		t.Fatalf("expected a clear payment-denial error, got %v", err)
	}
	if playbackCalls.Load() != 1 || mirrorPlaybackCalls.Load() != 0 {
		t.Fatalf("403 must not retry playback on another host; primary=%d mirror=%d", playbackCalls.Load(), mirrorPlaybackCalls.Load())
	}
}
