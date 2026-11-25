package core

// 红果 v5 取流适配层（2026-11-19）。
//
// 上游约 11-16 把取流从 v4 升级到 v5，旧的 v4 播放器签名全部返回 200+0
// 空正文，0.8.31 红果彻底不可播。v5 的注册（tt-data 信封）+ 签名
// （X-Khronos/Helios/Medusa）逐字节移植到 core/hgv5 子包（真网 live 测试
// 通过，10 个离线签名向量全绿）。
//
// 本文件只做适配：
//   - 用 production 的 HTTP client / limiter / 代理路由发 v5 请求；
//   - 拿到的原始响应体交给**既有**的 hongguoPlaybackModelFromResult 解析，
//     spade 地址解密、CENC key 派生、试看探测、缓存全部复用，零重复；
//   - v5 失败时回退到 provider_hongguo_playback_routes.go 的 v4 链路，
//     两条路都不动对方，便于灰度与回滚。
//
// 「每台匿名设备只放行一次播放」由 hgv5.NewIdentity 换新身份解决：一次
// 取流里若 v5 首轮返回空正文，就地换一台设备重签重发（最多 v5FetchRounds 次）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"duanjuapp/native/core/hgv5"
)

const (
	// v5FetchRounds 是一次取流里 v5 换设备重试的上限。上游对「用过一次的设备」
	// 回空正文，换一台全新匿名设备通常一次就中；4 次足以覆盖注册抖动。
	v5FetchRounds = 4
	// 单次注册 / 取流的网络预算。注册含 gzip + tt-data，取流含签名与 gzip body，
	// 正常都在 1s 内；给 4s 容忍移动网抖动，同时不让整条 v5 链路拖垮 6s 总预算。
	v5RequestTimeout   = 4 * time.Second
	v5RegisterTimeout  = 4 * time.Second
	v5ModelMaxBodySize = 1 << 20 // 1MB，v5 实测约 24KB
)

// hongguoV5FetchModel 走 v5 协议取一集的 video_model。
//
// 返回的是**原始响应体**（未经解析），由调用方喂给 hongguoPlaybackModelFromResult。
// 全程用 production 的 doPreparedCatalogRequest 发包，继承 playbackPriority、
// host 退避清理与代理直连兜底；v5 的 host 不参与目录侧退避统计。
func (downloader *Downloader) hongguoV5FetchModel(ctx context.Context, videoID string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, hongguoPlaybackFetchBudget)
	defer cancel()

	identity, err := hgv5.NewIdentity()
	if err != nil {
		return nil, fmt.Errorf("红果 v5 生成匿名身份失败: %w", err)
	}

	var lastErr error
	for round := 0; round < v5FetchRounds && fetchCtx.Err() == nil; round++ {
		// 1) 注册（tt-data 信封）。每轮换一台全新设备。
		if round > 0 {
			fresh, ferr := hgv5.NewIdentity()
			if ferr != nil {
				lastErr = ferr
				continue
			}
			identity = fresh
		}
		regBody, rerr := hgv5.BuildRegisterBody(identity, time.Now())
		if rerr != nil {
			lastErr = rerr
			continue
		}
		regQuery, regHeaders := hgv5.RegisterHeaders(identity, regBody)
		regURL := "https://" + hgv5.RegisterHost + hgv5.PathRegister + "?" + encodeHGV5Query(regQuery)
		regReq, reqErr := http.NewRequestWithContext(fetchCtx, http.MethodPost, regURL, bytes.NewReader(regBody))
		if reqErr != nil {
			lastErr = reqErr
			continue
		}
		for key, value := range regHeaders {
			regReq.Header.Set(key, value)
		}
		regResp, regErr := downloader.doPreparedCatalogRequest(regReq, v5RegisterTimeout, nil)
		if regErr != nil {
			lastErr = regErr
			continue
		}
		regRaw, readErr := io.ReadAll(io.LimitReader(regResp.Body, v5ModelMaxBodySize))
		regResp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		deviceID, installID, perr := hgv5.ParseRegister(regRaw)
		if perr != nil {
			lastErr = perr
			continue
		}
		identity.DeviceID, identity.InstallID = deviceID, installID
		identity.Egdi = hgv5.ParseRegisterEgdi(regRaw)

		// 1.5) 设备激活（app_alert_check）。官方客户端注册后会再打一次，
		// 未激活的设备 2026-11-25 起取流一律 200 + 0 字节。失败不阻断，仅记诊断。
		if alertURL, alertUA := hgv5.AlertCheckURL(deviceID, installID, time.Now().UnixMilli()); alertURL != "" {
			if alertReq, aerr := http.NewRequestWithContext(fetchCtx, http.MethodGet, alertURL, nil); aerr == nil {
				alertReq.Header.Set("User-Agent", alertUA)
				alertReq.Header.Set("Accept", "application/json")
				if alertResp, aerr2 := downloader.doPreparedCatalogRequest(alertReq, v5RegisterTimeout, nil); aerr2 == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(alertResp.Body, 4096))
					alertResp.Body.Close()
				}
			}
		}

		// 2) 构造 v5 body / query / 签名，全部基于同一份 gzip body。
		body, berr := hgv5.BuildPlaybackBody(videoID)
		if berr != nil {
			lastErr = berr
			continue
		}
		now := time.Now().UnixMilli()
		query, headers, serr := hgv5.SignRequest(identity, hgv5.PlaybackPairs(identity), body, now)
		if serr != nil {
			lastErr = serr
			continue
		}
		playURL := hgv5.APIBase + hgv5.PlaybackPath() + "?" + query
		req, perr := http.NewRequestWithContext(fetchCtx, http.MethodPost, playURL, bytes.NewReader(body))
		if perr != nil {
			lastErr = perr
			continue
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("User-Agent", hgv5.PlaybackUserAgent())
		req.Header.Set("Accept", "application/json; charset=utf-8,application/x-protobuf")
		req.Header.Set("x-xs-from-web", "0")
		if hgv5.PlaybackNeedsGzip() {
			req.Header.Set("Content-Encoding", "gzip")
		} else {
			req.Header.Del("Content-Encoding")
		}

		resp, derr := downloader.doPreparedCatalogRequest(req, v5RequestTimeout, nil)
		if derr != nil {
			// 命中目录侧残留的 host 退避时，清掉后用新签名原样重发一次（与 v4 同策略）。
			var backoff *requestBackoff
			if errors.As(derr, &backoff) && downloader.limiter != nil {
				downloader.limiter.clearBackoff(backoff.host)
				resp, derr = downloader.doPreparedCatalogRequest(req, v5RequestTimeout, nil)
			}
		}
		if derr != nil {
			lastErr = derr
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(resp.Body, v5ModelMaxBodySize+1))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("红果 v5 取流 HTTP %d", resp.StatusCode)
			continue
		}
		if len(content) == 0 {
			// 2026-11-20 服务端对 v5 POST 静默返回空正文。恢复路径（jackotom 7.7）：
			// 用同一身份改发 video_tab bodyless GET（v5 播放器签名），实测可拿到
			// 完整 video_model。GET 也空才换新设备进入下一轮。
			lastErr = errHongguoAppEmptyModel
			if recovered, tabErr := downloader.hongguoV5VideoTabRecover(fetchCtx, identity, videoID); tabErr == nil && len(recovered) > 0 {
				content = recovered
			} else {
				if tabErr != nil {
					lastErr = fmt.Errorf("%w（video_tab 恢复失败：%v）", errHongguoAppEmptyModel, tabErr)
				}
				continue // 两条路由都空 → 下一轮换新设备重签
			}
		}
		if len(content) > v5ModelMaxBodySize {
			lastErr = errors.New("红果 v5 取流返回数据过大")
			continue
		}
		// 确认是 JSON 且 code=0，避免把网关错误页当成成功 model 缓存。
		var probe struct {
			Code     json.Number `json:"code"`
			BaseResp struct {
				StatusCode json.Number `json:"StatusCode"`
			} `json:"BaseResp"`
			Data json.RawMessage `json:"data"`
		}
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&probe); err != nil || len(probe.Data) == 0 {
			lastErr = errors.New("红果 v5 取流返回格式异常")
			continue
		}
		if code := probe.Code.String(); code != "" && code != "0" {
			lastErr = fmt.Errorf("红果 v5 取流暂不可用（%s）", truncate(code, 20))
			continue
		}
		return content, nil
	}
	if fetchCtx.Err() != nil {
		return nil, errHongguoAppEmptyModel
	}
	if lastErr == nil {
		return nil, errHongguoAppEmptyModel
	}
	return nil, lastErr
}

// hongguoV5VideoTabRecover 在 v5 POST 返回空正文时，用同一身份改发
// video_tab bodyless GET（v5 播放器签名）恢复取流。
//
// 2026-11-20 上游对 v5 POST 静默返回 200+0；开源 jackotom/hongguo-drama 7.7
// （commit 92df757）确认 video_tab 同属 player 签名族，改用 v5 无正文 GET 签名
// 后恢复。本方法逐字节对齐其 videoTabRequest，已用真网探针验证拿到非空 model。
// 返回原始响应体，交给与 POST 相同的解析链处理。
func (downloader *Downloader) hongguoV5VideoTabRecover(ctx context.Context, identity hgv5.Identity, videoID string) ([]byte, error) {
	tabCtx, cancel := context.WithTimeout(ctx, v5RequestTimeout)
	defer cancel()
	tabURL, headers, err := hgv5.SignVideoTabGET(identity, videoID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(tabCtx, http.MethodGet, tabURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("User-Agent", hgv5.VideoTabUserAgent())
	req.Header.Set("Accept", "application/json")

	resp, derr := downloader.doPreparedCatalogRequest(req, v5RequestTimeout, nil)
	if derr != nil {
		var backoff *requestBackoff
		if errors.As(derr, &backoff) && downloader.limiter != nil {
			downloader.limiter.clearBackoff(backoff.host)
			resp, derr = downloader.doPreparedCatalogRequest(req, v5RequestTimeout, nil)
		}
		if derr != nil {
			return nil, derr
		}
	}
	content, readErr := io.ReadAll(io.LimitReader(resp.Body, v5ModelMaxBodySize+1))
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果 video_tab 恢复 HTTP %d", resp.StatusCode)
	}
	if len(content) == 0 {
		return nil, errHongguoAppEmptyModel
	}
	if len(content) > v5ModelMaxBodySize {
		return nil, errors.New("红果 video_tab 恢复返回数据过大")
	}
	return content, nil
}

// hongguoV5PlaybackModel 走完 v5 取流并返回 video_model；失败原因直接上抛，
// 由 resolveHongguoAppMedia 决定是否回退 v4。
func (downloader *Downloader) hongguoV5PlaybackModel(ctx context.Context, videoID string) (map[string]any, error) {
	raw, err := downloader.hongguoV5FetchModel(ctx, videoID)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果 v5 取流返回格式异常")
	}
	return hongguoPlaybackModelFromResult(result, videoID)
}

// encodeHGV5Query 把注册 query 拼成 stable 的 URL 串（键排序，只为可复现）。
func encodeHGV5Query(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(values[key]))
	}
	return strings.Join(parts, "&")
}
