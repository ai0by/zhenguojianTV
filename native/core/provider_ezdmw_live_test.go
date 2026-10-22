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
	t.Logf("live E站 smoke passed: %d search results, %d episodes, media host=%s, HTTP %d", len(items), len(chapters), parsed.Host, resp.StatusCode)
}
