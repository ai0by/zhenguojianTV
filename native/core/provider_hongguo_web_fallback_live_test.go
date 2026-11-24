package core

// 官网网页公开片源通道的真网验收（2026-11-23）。
// 默认跳过；要跑：
//
//	HONGGUO_LIVE_PROBE=1 go test ./core -run TestHongguoWebFallback -v -count=1

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestHongguoWebFallbackMedia 验证网页通道单独能拿到完整明文片源。
func TestHongguoWebFallbackMedia(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 2026-11-23 真网验证有完整 360P 公开片源的一集。
	const videoID = "7697465535398677529"
	media, err := d.resolveHongguoWebFallbackMedia(ctx, videoID)
	if err != nil {
		t.Fatalf("网页公开片源取流失败：%v", err)
	}
	if !strings.HasPrefix(media.URL, "https://") {
		t.Fatalf("片源地址异常：%q", media.URL)
	}
	if media.SourceKind != hongguoSourceWeb {
		t.Fatalf("来源标记应为 web，实际 %q", media.SourceKind)
	}
	if len(media.CENCKey) != 0 || len(media.HLSKey) != 0 {
		t.Fatal("网页片源应为明文（无 CENC/HLS key）")
	}
	if media.Quality != hongguoWebFallbackQuality {
		t.Fatalf("画质应为 %d，实际 %d", hongguoWebFallbackQuality, media.Quality)
	}
	if media.Duration < 30*time.Second {
		t.Fatalf("片源时长过短（%.1fs），疑似预告而非完整集", media.Duration.Seconds())
	}
	t.Logf("OK url_host=%s duration=%.1fs quality=%d source=%s",
		strings.Split(strings.TrimPrefix(media.URL, "https://"), "/")[0],
		media.Duration.Seconds(), media.Quality, media.SourceKind)
}

// TestHongguoWebFallbackUnavailableForPaid 验证付费/未公开集返回"无此线路"
// 而不是崩溃（接口 403）。
func TestHongguoWebFallbackUnavailableForPaid(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const paidVideoID = "7704577082471014042" // 11-23 实测接口 403
	_, err := d.resolveHongguoWebFallbackMedia(ctx, paidVideoID)
	if err == nil {
		t.Fatal("付费/未公开集本应无网页片源，却返回成功")
	}
	t.Logf("作为预期：%v", err)
}

// TestHongguoResolveMediaWithWebFallback 走完整生产入口，验证 App 通道全挂时
// 网页通道能顶上（二者均可用）。
func TestHongguoResolveMediaWithWebFallback(t *testing.T) {
	requireProbe(t)
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const videoID = "7697465535398677529"
	task := Task{
		DramaID: "hongguo:1",
		Chapter: Chapter{VideoURL: "hongguo-cenc://" + videoID},
	}
	// 红果上游间歇性风控：App 通道偶尔会返回时长异常的片源。这里验证
	// "入口能拿到完整片源"，最多两次（与前端 media_transient 自动重试对齐）。
	var media providerMedia
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		media, err = d.resolveHongguoMedia(ctx, task)
		if err == nil && strings.HasPrefix(media.URL, "https://") && media.Duration >= 30*time.Second {
			break
		}
		if attempt == 2 {
			if err != nil {
				t.Fatalf("生产入口取流失败：%v", err)
			}
			t.Fatalf("片源异常：url=%q duration=%.1fs source=%s", media.URL, media.Duration.Seconds(), media.SourceKind)
		}
	}
	t.Logf("OK source=%s duration=%.1fs variants=%d quality=%d",
		media.SourceKind, media.Duration.Seconds(), len(media.Variants), media.Quality)
}
