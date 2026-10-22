package core

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestEZDMWCatalogParsesAnimeCards(t *testing.T) {
	body := "<section id=\"some_drama\"><div class=\"item\"><a href=\"/show/123.html\"><img data-src=\"/cover/123.jpg\" /></a><p>番剧甲</p><span>更新到12</span></div></section>"
	items := parseEZDMWCatalogItems(body, ezdmwBaseURL+"/Index/search.html?searchText=x")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].ID != "ezdmw:123" || items[0].Title != "番剧甲" {
		t.Fatalf("unexpected item: %+v", items[0])
	}
	if items[0].Cover != "https://www.ezdmw.org/cover/123.jpg" {
		t.Fatalf("unexpected cover URL: %q", items[0].Cover)
	}
	if nativeText(items[0].Episodes) != "12" {
		t.Fatalf("unexpected episode count: %v", items[0].Episodes)
	}
}

func TestParseEZDMWEpisodeCountHandlesMissingCount(t *testing.T) {
	if got := parseEZDMWEpisodeCount("暂无集数信息"); got != 0 {
		t.Fatalf("missing episode count = %d, want 0", got)
	}
	if got := parseEZDMWEpisodeCount("更新到12"); got != 12 {
		t.Fatalf("episode count = %d, want 12", got)
	}
}

func TestEZDMWNearbyTitleIgnoresHTMLComments(t *testing.T) {
	raw := "<div><a href=\"/Index/bangumi/123.html\"><img src=\"/cover.jpg\" /></a><p><span><!-- 1.7万 --> </span>&nbsp;番剧甲</p></div>"
	start := strings.Index(raw, "<a")
	end := strings.Index(raw, "</a>") + len("</a>")
	if got := ezdmwNearbyTitle(raw, start, end); got != "番剧甲" {
		t.Fatalf("nearby title = %q, want 番剧甲", got)
	}
}

func TestEZDMWDetailParsesAndDeduplicatesEpisodes(t *testing.T) {
	body := "<section class=\"anthology\"><div class=\"line_button_ban\"></div><a class=\"circuit_switch_ban\" href=\"/Index/bangumi/101.html\">第1集</a><a class=\"circuit_switch_ban\" href=\"/Index/bangumi/201.html\">第1集</a><a class=\"circuit_switch_ban\" href=\"/Index/bangumi/102.html\">第2集</a><a class=\"circuit_switch_ban\" href=\"/v/103.html\">第3集</a></section>"
	chapters := parseEZDMWEpisodes(body, ezdmwBaseURL+"/show/123.html", "123")
	if len(chapters) != 3 {
		t.Fatalf("got %d chapters, want 3: %+v", len(chapters), chapters)
	}
	if chapters[0].ID != "ezdmw:123:101" || chapters[0].PageURL != ezdmwBaseURL+"/Index/video/101.html" {
		t.Fatalf("unexpected first chapter: %+v", chapters[0])
	}
	if chapters[2].PageURL != ezdmwBaseURL+"/Index/video/103.html" {
		t.Fatalf("unexpected canonical episode URL: %q", chapters[2].PageURL)
	}
	if chapters[1].Title != "第2集" || chapters[1].VideoURL != "ezdmw-play://102" {
		t.Fatalf("unexpected second chapter: %+v", chapters[1])
	}
}

func TestEZDMWMediaFromPlayerFrameURL(t *testing.T) {
	direct := "https://player.example.test/danmuku/?src=" + url.QueryEscape("https://cdn.example.test/episode.m3u8?token=abc")
	if got := ezdmwMediaFromPlayerFrameURL(direct); got != "https://cdn.example.test/episode.m3u8?token=abc" {
		t.Fatalf("direct player frame media = %q", got)
	}
	encodedMedia := base64.RawURLEncoding.EncodeToString([]byte("https://cdn.example.test/episode.mp4"))
	encoded := "https://player.example.test/danmuku/?src=" + url.QueryEscape(encodedMedia)
	if got := ezdmwMediaFromPlayerFrameURL(encoded); got != "https://cdn.example.test/episode.mp4" {
		t.Fatalf("encoded player frame media = %q", got)
	}
}

func TestEZDMWListResURLIsRecognizedAsPlaylist(t *testing.T) {
	if !ezdmwPlaylistURL("https://player.ezdmw.com/index/listres/resource_name/test/key/test/sign/abc.webp?lineOne=true") {
		t.Fatal("E站 listres URL with webp suffix should be treated as HLS")
	}
	if ezdmwPlaylistURL("https://cdn.example.test/cover.webp") {
		t.Fatal("ordinary webp image should not be treated as HLS")
	}
}

func TestEZDMWPlaybackParserHandlesDirectMediaAndIframe(t *testing.T) {
	pageURL := ezdmwBaseURL + "/Index/bangumi/101.html"
	raw := "<video><source src=\"https://cdn.example.test/episode.m3u8?token=one&amp;part=two\"></video>"
	if got := ezdmwDirectMedia(raw, pageURL); got != "https://cdn.example.test/episode.m3u8?token=one&part=two" {
		t.Fatalf("direct media URL = %q", got)
	}
	if got := parseEZDMWPlayerFrame("<iframe src=\"//player.example.test/embed/101\"></iframe>", pageURL); got != "https://player.example.test/embed/101" {
		t.Fatalf("iframe URL = %q", got)
	}
}

func TestEZDMWRejectsUnsafePlayerPageAddresses(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1/player", "http://192.168.1.2/player", "https://localhost/player"} {
		if ezdmwSafePageURL(address) {
			t.Fatalf("unsafe address accepted: %s", address)
		}
	}
	if !ezdmwSafePageURL("https://player.example.test/embed/101") {
		t.Fatal("public HTTPS player address rejected")
	}
	if !strings.Contains(ezdmwCleanTitle("番剧甲第1集在线观看&下载-E站弹幕网"), "番剧甲") {
		t.Fatal("episode title cleanup lost the anime title")
	}
}
