package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestXiangjiaoJSONRetriesForbiddenPrimaryMirror(t *testing.T) {
	var primaryCalls atomic.Int32
	var mirrorCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "xiangjiaoai.ai" {
			primaryCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusForbidden, `{"code":"EDGE_BLOCKED","message":"upstream denied"}`), nil
		}
		mirrorCalls.Add(1)
		if got := request.Header.Get("Origin"); got != "https://"+request.URL.Hostname() {
			t.Errorf("Origin = %q, want request origin", got)
		}
		if got := request.Header.Get("Referer"); got != "https://"+request.URL.Hostname()+"/" {
			t.Errorf("Referer = %q, want request referer", got)
		}
		return sourceFixtureResponse(request, http.StatusOK, `{"data":{"access_token":"fixture-token"}}`), nil
	})

	decoded, err := downloader.xiangjiaoJSON(
		context.Background(),
		http.MethodPost,
		"/api/guest-sessions",
		map[string]any{"device_id": "fixture-device"},
		"",
	)
	if err != nil {
		t.Fatalf("xiangjiaoJSON: %v", err)
	}
	data, _ := xiangjiaoPayloadData(decoded).(map[string]any)
	if xiangjiaoText(data, "access_token") != "fixture-token" {
		t.Fatalf("unexpected mirror response: %#v", decoded)
	}
	if primaryCalls.Load() == 0 || mirrorCalls.Load() == 0 {
		t.Fatalf("expected both primary and mirror to be tried; primary=%d mirror=%d", primaryCalls.Load(), mirrorCalls.Load())
	}
}

func TestXiangjiaoFullPlaybackProbeUsesConcurrency(t *testing.T) {
	const sourceID = "parallel-probe-fixture-20261022"
	xiangjiaoPlayableCache.Delete(sourceID)
	t.Cleanup(func() { xiangjiaoPlayableCache.Delete(sourceID) })

	var active atomic.Int32
	var maxActive atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/dramas/"+sourceID+"/episodes":
			return sourceFixtureResponse(request, http.StatusOK, `{"data":[
				{"id":"ep-1","episode_number":1,"drama_id":"parallel-probe-fixture-20261022"},
				{"id":"ep-2","episode_number":2,"drama_id":"parallel-probe-fixture-20261022"},
				{"id":"ep-3","episode_number":3,"drama_id":"parallel-probe-fixture-20261022"},
				{"id":"ep-4","episode_number":4,"drama_id":"parallel-probe-fixture-20261022"},
				{"id":"ep-5","episode_number":5,"drama_id":"parallel-probe-fixture-20261022"},
				{"id":"ep-6","episode_number":6,"drama_id":"parallel-probe-fixture-20261022"}
			]}`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/guest-sessions":
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"access_token":"fixture-token","expires_at":"2099-01-01T00:00:00Z"}}`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/playback/sessions":
			current := active.Add(1)
			for {
				observed := maxActive.Load()
				if current <= observed || maxActive.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
			active.Add(-1)
			var payload struct {
				EpisodeID string `json:"episode_id"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			return sourceFixtureResponse(request, http.StatusOK, fmt.Sprintf(
				`{"data":{"play_mode":"full","media":{"manifest_url":"https://media.example.test/%s.m3u8"}}}`,
				payload.EpisodeID,
			)), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})

	if !downloader.xiangjiaoDramaFullyPlayable(context.Background(), sourceID) {
		t.Fatal("all-full-play fixture should pass")
	}
	if maxActive.Load() < 2 {
		t.Fatalf("playback probes were serial, max concurrency=%d", maxActive.Load())
	}
}

func TestXiangjiaoFullPlaybackRejectsLockedEpisodes(t *testing.T) {
	const sourceID = "locked-probe-fixture-20261022"
	xiangjiaoPlayableCache.Delete(sourceID)
	t.Cleanup(func() { xiangjiaoPlayableCache.Delete(sourceID) })
	var playbackCalls atomic.Int32
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/dramas/"+sourceID+"/episodes":
			return sourceFixtureResponse(request, http.StatusOK, `{"data":[
				{"id":"ep-locked","episode_number":1,"drama_id":"locked-probe-fixture-20261022","commerce":{"charge_type":"vip"}}
			]}`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/playback/sessions":
			playbackCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusOK, `{"data":{"play_mode":"full","media":{"manifest_url":"https://media.example.test/ep.m3u8"}}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})

	if downloader.xiangjiaoDramaFullyPlayable(context.Background(), sourceID) {
		t.Fatal("VIP-locked episode must not pass full-play validation")
	}
	if playbackCalls.Load() != 0 {
		t.Fatalf("locked episodes should be rejected from metadata without playback requests, got %d", playbackCalls.Load())
	}
}
