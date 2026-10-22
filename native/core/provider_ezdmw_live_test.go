package core

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestEZDMWLiveCatalogDetailPlayback is an opt-in end-to-end smoke test against
// the real E站 website. It is intentionally excluded from normal deterministic
// regression runs because the upstream site and playback lines can change.
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
		searchURL := strings.TrimRight(d.providerBaseURL(sourceEZDMW), "/") + "/Index/search.html?searchText=" + url.QueryEscape("学生会也有洞")
		if body, fetchErr := d.fetchProviderText(ctx, searchURL, ezdmwBaseURL+"/"); fetchErr == nil {
			sectionPattern := regexp.MustCompile(`(?is)<(?:section|div)\b[^>]*id=["']some_drama["'][^>]*>`)
			sectionAt := sectionPattern.FindStringIndex(body)
			if sectionAt == nil {
				t.Logf("live search diagnostics: bytes=%d title=%q; no some_drama container; occurrences=%d", len(body), extractPageTitle(body), strings.Count(strings.ToLower(body), "some_drama"))
			} else {
				start := sectionAt[0]
				end := start + 4200
				if end > len(body) {
					end = len(body)
				}
				t.Logf("live search diagnostics: bytes=%d title=%q; container_at=%d excerpt=%q", len(body), extractPageTitle(body), start, body[start:end])
			}
		}
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
		t.Fatalf("live E站 search returned %d items but not the expected current title", len(items))
	}
	t.Logf("catalog parsed: %d results; selected %q", len(items), selected.Title)

	drama, chapters, err := d.fetchEZDMWDetail(ctx, selected.SourceID)
	if err != nil {
		t.Fatalf("live E站 detail parse failed for source ID %s: %v", selected.SourceID, err)
	}
	if len(chapters) == 0 {
		t.Fatalf("live E站 detail %q returned no episodes", drama.DisplayTitle())
	}
	t.Logf("detail parsed: %q; %d episodes", drama.DisplayTitle(), len(chapters))

	task := Task{DramaID: drama.ID, DramaTitle: drama.DisplayTitle(), Chapter: chapters[0], Index: 0}
	media, err := d.resolveProviderMedia(ctx, task)
	if err != nil {
		t.Fatalf("live E站 playback resolution failed: %v", err)
	}
	parsed, err := url.Parse(media.URL)
	if err != nil || parsed.Host == "" {
		t.Fatal("live E站 playback resolver returned an invalid media URL")
	}
	t.Logf("playback URL resolved: host=%s path=%s", parsed.Host, parsed.Path)

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
	t.Logf("playback probe passed: HTTP %d and readable response body", resp.StatusCode)
}
