package core

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// resolveEZDMWLiveTestURL resolves playlist references without applying the
// production helper's HTTP-to-HTTPS upgrade. The native playback gateway is
// intentionally served over plain HTTP on loopback.
func resolveEZDMWLiveTestURL(base, reference string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return reference
	}
	referenceURL, err := url.Parse(reference)
	if err != nil {
		return reference
	}
	return baseURL.ResolveReference(referenceURL).String()
}

func firstEZDMWHLSResource(playlist string) string {
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// TestEZDMWLiveCatalogDetailPlayback is an opt-in end-to-end smoke test against
// the real E站 site. Set EZDMW_LIVE_TEST=1 to run it; normal unit tests stay deterministic.
func TestEZDMWLiveCatalogDetailPlayback(t *testing.T) {
	if os.Getenv("EZDMW_LIVE_TEST") != "1" {
		t.Skip("set EZDMW_LIVE_TEST=1 to test the live E站 catalog and playback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatalf("initialize native engine: %v", err)
	}
	d := engine.downloader

	items, _, err := d.fetchEZDMWCatalogPage(ctx, 1, "", "学生会也有洞")
	if err != nil {
		t.Fatalf("live E站 search/catalog failed: %v", err)
	}
	var selected Drama
	for _, item := range items {
		if strings.Contains(item.Title, "学生会也有洞") {
			selected = item
			break
		}
	}
	if selected.ID == "" {
		t.Fatalf("live E站 search returned %d results but not the expected title", len(items))
	}

	drama, chapters, err := d.fetchEZDMWDetail(ctx, selected.SourceID)
	if err != nil {
		t.Fatalf("live E站 detail parse failed: %v", err)
	}
	if !strings.Contains(drama.DisplayTitle(), "学生会也有洞") {
		t.Fatalf("live E站 detail title mismatch: %q", drama.DisplayTitle())
	}
	if len(chapters) == 0 {
		t.Fatalf("live E站 detail %q returned no episodes", drama.DisplayTitle())
	}

	task := Task{DramaID: drama.ID, DramaTitle: drama.DisplayTitle(), Chapter: chapters[0], Index: 0}
	media, err := d.resolveProviderMedia(ctx, task)
	if err != nil {
		t.Fatalf("live E站 playback resolution failed: %v", err)
	}
	parsed, err := url.Parse(media.URL)
	if err != nil || parsed.Host == "" {
		t.Fatal("live E站 playback resolver returned an invalid media URL")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
	if err != nil {
		t.Fatalf("create playback probe: %v", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", media.Referer)
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := d.client.Do(req)
	if err != nil {
		t.Fatalf("live E站 media endpoint request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("live E站 media endpoint returned HTTP %d", resp.StatusCode)
	}
	n, readErr := io.CopyN(io.Discard, resp.Body, 1)
	if n == 0 || (readErr != nil && readErr != io.EOF) {
		t.Fatalf("live E站 media endpoint returned no readable bytes (read=%d, err=%v)", n, readErr)
	}
	plan, err := engine.nativeOpenPlayback(ctx, nativePlaybackChoices(media, 0))
	if err != nil {
		t.Fatalf("open E站 through the native playback gateway: %v", err)
	}
	localClient := &http.Client{Timeout: 15 * time.Second}
	localReq, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.URL, nil)
	if err != nil {
		t.Fatalf("create local playback request: %v", err)
	}
	localResp, err := localClient.Do(localReq)
	if err != nil {
		t.Fatalf("native playback gateway request failed: %v", err)
	}
	playlistBytes, readErr := io.ReadAll(io.LimitReader(localResp.Body, 1<<20))
	localResp.Body.Close()
	if readErr != nil {
		t.Fatalf("read native HLS playlist: %v", readErr)
	}
	if localResp.StatusCode < 200 || localResp.StatusCode >= 300 || !strings.HasPrefix(strings.TrimSpace(string(playlistBytes)), "#EXTM3U") {
		t.Fatalf("native playback gateway did not return a valid HLS playlist (HTTP %d)", localResp.StatusCode)
	}
	resource := firstEZDMWHLSResource(string(playlistBytes))
	if resource == "" {
		t.Fatal("native HLS playlist contains no variant or segment URL")
	}
	resourceURL := resolveEZDMWLiveTestURL(plan.URL, resource)
	resourceReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		t.Fatalf("create native HLS resource request: %v", err)
	}
	resourceResp, err := localClient.Do(resourceReq)
	if err != nil {
		t.Fatalf("native HLS resource request failed: %v", err)
	}
	resourceBytes, resourceReadErr := io.ReadAll(io.LimitReader(resourceResp.Body, 1<<20))
	resourceResp.Body.Close()
	if resourceReadErr != nil {
		t.Fatalf("read native HLS resource: %v", resourceReadErr)
	}
	if resourceResp.StatusCode < 200 || resourceResp.StatusCode >= 300 || len(resourceBytes) == 0 {
		t.Fatalf("native HLS resource unavailable (HTTP %d, bytes=%d)", resourceResp.StatusCode, len(resourceBytes))
	}
	if strings.HasPrefix(strings.TrimSpace(string(resourceBytes)), "#EXTM3U") {
		segment := firstEZDMWHLSResource(string(resourceBytes))
		if segment == "" {
			t.Fatal("variant HLS playlist contains no media segment")
		}
		segmentURL := resolveEZDMWLiveTestURL(resourceURL, segment)
		segmentReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, segmentURL, nil)
		if reqErr != nil {
			t.Fatalf("create segment request: %v", reqErr)
		}
		segmentResp, reqErr := localClient.Do(segmentReq)
		if reqErr != nil {
			t.Fatalf("native media segment request failed: %v", reqErr)
		}
		n, _ := io.CopyN(io.Discard, segmentResp.Body, 1)
		segmentResp.Body.Close()
		if segmentResp.StatusCode < 200 || segmentResp.StatusCode >= 300 || n == 0 {
			t.Fatalf("native media segment unavailable (HTTP %d, bytes=%d)", segmentResp.StatusCode, n)
		}
	}
	t.Logf("live E站 playback passed: %d search results, %d episodes, upstream=%s, native HLS gateway and first resource verified", len(items), len(chapters), parsed.Host)
}
