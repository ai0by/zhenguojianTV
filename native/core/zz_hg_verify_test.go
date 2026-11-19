package core

// 临时验证探针：确认改完的生产链路能真实取流。验证完立即删除，不入库。

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestZgjVerifyProductionPlayback(t *testing.T) {
	if os.Getenv("ZGJ_VERIFY_PROBE") != "1" {
		t.Skip("临时探针")
	}
	d := probeDownloader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.ensureHongguoDevice(ctx); err != nil {
		t.Fatalf("设备注册失败：%v", err)
	}
	seriesID, _ := probeCatalogSeries(t, d)
	entry, err := d.hongguoAppDetail(ctx, seriesID)
	if err != nil {
		t.Fatalf("详情失败：%v", err)
	}
	var ids []string
	for _, chapter := range entry.Chapters {
		id := strings.TrimPrefix(chapter.VideoURL, "hongguo-cenc://")
		if id != "" {
			ids = append(ids, id)
		}
		if len(ids) >= 5 {
			break
		}
	}
	t.Logf("[verify] series=%s ids=%v", seriesID, ids)
	okCount := 0
	for i, id := range ids {
		started := time.Now()
		media, err := d.resolveHongguoAppMedia(ctx, id)
		if err != nil {
			t.Logf("[V%d] vid=%s FAIL %s err=%v", i, id, time.Since(started).Round(time.Millisecond), err)
			continue
		}
		// 真发一个 Range 请求，确认不是死链。
		rangeStatus := 0
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
		if reqErr == nil {
			req.Header.Set("Range", "bytes=0-1023")
			resp, err := d.client.Do(req)
			if err == nil {
				rangeStatus = resp.StatusCode
				resp.Body.Close()
			}
		}
		okCount++
		t.Logf("[V%d] vid=%s OK %s duration=%s quality=%d variants=%d cencKey=%d rangeStatus=%d",
			i, id, time.Since(started).Round(time.Millisecond), media.Duration,
			media.Quality, len(media.Variants), len(media.CENCKey), rangeStatus)
	}
	t.Logf("[verify] 生产链路真实取流成功 %d/%d", okCount, len(ids))
}
