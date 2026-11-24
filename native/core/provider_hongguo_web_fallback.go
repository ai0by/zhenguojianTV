package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 官网网页播放器公开片源通道（2026-11-23）。
//
// 红果 App 取流接口在 11-23 又一轮全链路空响应（200 + 0 字节）时，官网网页
// 播放器仍能通过 /api/video/protected/fallback-play-info 拿到当前公开分集的
// **完整** 360P H.264 明文 MP4（不是 SEO 页的 10 秒预览，实测 88~99 秒、
// 8.8MB、avc1、无 cenc/sinf 加密标记、支持 Range）。
//
// 这是一条与 App 取流**独立并列**的通道：App 通道返回加密高清片源（带 CENCKey），
// web 通道返回明文标清片源（无 CENCKey）。两条都可用，前端用 `source` 字段区分。
// 2026-11-23 采纳依据：jackotom/hongguo-drama 8.0（commit ba1fd443）真机验收。
const hongguoWebFallbackPath = "/api/video/protected/fallback-play-info"

const (
	hongguoWebFallbackQuality      = 360 // 网页公开片源固定 360P（360x640 竖屏）
	hongguoWebFallbackTimeout      = 6 * time.Second
	hongguoWebFallbackProbeTimeout = 5 * time.Second
	hongguoWebFallbackMaxBody      = 64 << 10
)

// 网页播放器 UA 与 Referer：接口层要求 Referer 指向官网，否则拒绝。
const hongguoWebFallbackUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

var errHongguoWebFallbackUnavailable = errors.New("官网公开片源暂不可用")

// resolveHongguoWebFallbackMedia 调官网网页播放器的公开兜底接口取完整明文片源。
// 仅公开分集可用（付费/未公开集接口回 403），调用方据此当作"这一集没有网页线路"。
func (downloader *Downloader) resolveHongguoWebFallbackMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	requestURL := hongguoBaseURL + hongguoWebFallbackPath
	payload := fmt.Sprintf(`{"item_id":%q}`, videoID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(payload))
	if err != nil {
		return providerMedia{}, err
	}
	request.Header.Set("User-Agent", hongguoWebFallbackUA)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Referer", hongguoBaseURL+"/")

	response, err := downloader.doCatalogRequestWithTimeout(request, hongguoWebFallbackTimeout)
	if err != nil {
		return providerMedia{}, err
	}
	defer response.Body.Close()
	// 403 = 该集不公开（付费/未上架），是"没有这条线路"而非网络故障。
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return providerMedia{}, errHongguoWebFallbackUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return providerMedia{}, fmt.Errorf("官网公开片源接口 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, hongguoWebFallbackMaxBody))
	if err != nil {
		return providerMedia{}, err
	}
	var result struct {
		MainURL   string `json:"main_url"`
		URLExpire int64  `json:"url_expire"`
		BaseResp  struct {
			StatusCode int `json:"StatusCode"`
		} `json:"base_resp"`
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return providerMedia{}, fmt.Errorf("官网公开片源响应无法解析：%v", err)
	}
	if result.BaseResp.StatusCode != 0 || result.Code != 0 {
		return providerMedia{}, errHongguoWebFallbackUnavailable
	}
	mediaURL, ok := hongguoValidWebFallbackURL(result.MainURL, result.URLExpire)
	if !ok {
		return providerMedia{}, errHongguoWebFallbackUnavailable
	}

	// 时长校验：必须确认是完整正片，不能把 10 秒预告当片源交给播放器。
	// 网页接口给的是完整片源，但保留这道闸，防止上游哪天又降级成预览。
	duration, probeErr := downloader.hongguoProbeWebFallbackDuration(ctx, mediaURL)
	if probeErr != nil || hongguoLooksLikePreview(duration) {
		if probeErr != nil {
			return providerMedia{}, fmt.Errorf("官网公开片源无法确认时长：%v", probeErr)
		}
		return providerMedia{}, fmt.Errorf("官网公开片源只有 %.0f 秒，疑似预告", duration.Seconds())
	}
	return providerMedia{
		URL:        mediaURL,
		Referer:    hongguoBaseURL + "/",
		Duration:   duration,
		Quality:    hongguoWebFallbackQuality,
		SourceKind: hongguoSourceWeb,
	}, nil
}

// hongguoValidWebFallbackURL 校验网页接口返回的片源地址：必须是 https、
// 落在红果视频 CDN 白名单内、无用户名密码、且未过期。防止接口被污染后
// 把任意地址交给播放器/下载器。
func hongguoValidWebFallbackURL(raw string, expire int64) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for _, suffix := range []string{".qznovelvod.com", ".fqnovelvod.com", ".douyinvod.com"} {
		if strings.HasSuffix(host, suffix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false
	}
	if expire > 0 && expire*1000 <= time.Now().UnixMilli()+5000 {
		return "", false
	}
	return parsed.String(), true
}

// hongguoProbeWebFallbackDuration 读片源头部判断真实时长（复用 App 通道的
// mp4 头部解析器，约定：Range 0-65535 一次拿到 moov/mvhd）。
func (downloader *Downloader) hongguoProbeWebFallbackDuration(ctx context.Context, mediaURL string) (time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", hongguoWebFallbackUA)
	request.Header.Set("Referer", hongguoBaseURL+"/")
	request.Header.Set("Range", "bytes=0-65535")
	response, err := downloader.doCatalogRequestWithTimeout(request, hongguoWebFallbackProbeTimeout)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("片源返回 HTTP %d", response.StatusCode)
	}
	prefix, err := io.ReadAll(io.LimitReader(response.Body, hongguoMediaProbeBytes))
	if err != nil {
		return 0, err
	}
	return hongguoMediaDurationFromPrefix(prefix)
}
