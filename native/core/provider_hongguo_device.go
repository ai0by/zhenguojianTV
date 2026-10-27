package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 红果游客设备注册（0.8.14）。
//
// 背景：2026-10-27 实测，取流接口
// `POST /novel/player/multi_video_model/v1/` 对**未注册的 device_id**
// 返回 `200 + 空正文`。之前一直判断成"上游对游客关闭了正片"，实际是
// `device_id` / `install_id` 由客户端本地随机生成，服务端根本不认这个设备。
//
// 对照实验（同一接口、同一参数、同一时刻）：
//   - 本地随机 device_id  → 连续 3 次 `200 + 0 字节`
//   - 注册得到的 device_id → 稳定返回 24.6KB 真实 video_model
//
// 注册走的是字节 App 首次启动时那条通用链路，不需要登录、不需要 Cookie、
// 不需要签名：
//
//	POST https://log.snssdk.com/service/2/device_register/?aid=8662
//
// 响应里 `device_id_str` / `install_id_str` 就是后续取流要带的设备标识。
// 每次注册都是新设备，所以只需注册一次并持久化（见 hongguoCatalogState.Registered）。
// hongguoDeviceRegisterURL 是游客设备注册地址。
//
// 用变量而不是常量，只为单测能把它指到 httptest server —— 注册响应用 gzip
// 压缩，而"能不能解压"取决于客户端有没有自己声明 Accept-Encoding（见
// hongguoRegisterDevice 里的说明），这一点必须端到端验证。
var hongguoDeviceRegisterURL = "https://log.snssdk.com/service/2/device_register/?aid=8662"

// hongguoDeviceRegisterAttempts 是注册的尝试次数。实测该接口偶发
// `SSL UNEXPECTED_EOF`（约 1/6），重试一次基本就过。
const hongguoDeviceRegisterAttempts = 3

// hongguoDeviceRegisterTimeout 比 providerTimeout（12 秒）短一半。
//
// 注册只是取流前的一次"顺带"请求，正常几百毫秒就回。网络不通时按 12 秒
// 超时 × 3 次会卡满 36 秒，用户看到的就是长时间转圈 —— 宁可快速失败。
const hongguoDeviceRegisterTimeout = 6 * time.Second

// hongguoDeviceRetryCooldown 是注册失败后的冷却时间。冷却期内不再重试，
// 直接把上次的错误抛给用户，避免"点一次播、转 36 秒、再点还转 36 秒"。
const hongguoDeviceRetryCooldown = 2 * time.Minute

// errHongguoDeviceUnregistered 表示游客设备没注册成功。
//
// 这种情况下取流接口必然返回空正文，公开片源与网页链路也只会给 10 秒预告
// （会被时长校验拒掉），继续往下试只是让用户多等两个网络往返。
var errHongguoDeviceUnregistered = errors.New("红果游客设备注册失败")

type hongguoDeviceRegistration struct {
	DeviceID  string
	InstallID string
	Token     string
}

// hongguoDeviceHeader 构造 ss_app_log 的 header。
//
// 字段看起来很多，但服务端只认其中一部分；缺 `package` / `app_version` /
// `cdid` 之类会返回 `device_id: 0`（等于没注册）。这里按真机抓到的形态补齐，
// 随机字段每次生成，保证每台设备独立。
func hongguoDeviceHeader() map[string]any {
	versionCode, _ := strconv.Atoi(hongguoAppVersionCode)
	return map[string]any{
		"display_name":          "红果短剧",
		"update_version_code":   versionCode,
		"manifest_version_code": versionCode,
		"aid":                   8662,
		"channel":               "oppo_8662_64",
		"package":               "com.phoenix.read",
		"app_version":           hongguoAppVersionName,
		"version_code":          versionCode,
		"app_version_minor":     "",
		"sdk_version":           "2.14.0",
		"os":                    "Android",
		"os_version":            "13",
		"os_api":                33,
		"device_model":          "PGT-AN10",
		"device_brand":          "OPPO",
		"device_manufacturer":   "OPPO",
		"cpu_abi":               "arm64-v8a",
		"device_platform":       "android",
		"density_dpi":           480,
		"display_density":       "xxhdpi",
		"resolution":            "1080x2400",
		"language":              "zh",
		"timezone":              8,
		"access":                "wifi",
		"ac":                    "wifi",
		"region":                "CN",
		"tz_name":               "Asia/Shanghai",
		"tz_offset":             28800,
		"sim_region":            "cn",
		"carrier":               "CHINA MOBILE",
		"mcc_mnc":               "46000",
		"rom":                   "PGT-AN10_13_TP1A.220905.001",
		"rom_version":           "TP1A.220905.001",
		"sig_hash":              "",
		"guest_mode":            1,
		"cold_start":            1,
		"first_launch":          1,
		"applog_disable":        0,
		"cdid":                  hongguoRandomUUID(),
		"openudid":              hongguoRandomHex(16),
		"clientudid":            hongguoRandomUUID(),
		"iid":                   0,
		"last_report_time":      0,
		"not_request_sender":    0,
	}
}

// hongguoRandomHex 生成 n 个十六进制字符。
func hongguoRandomHex(n int) string {
	const alphabet = "0123456789abcdef"
	var builder strings.Builder
	builder.Grow(n)
	for index := 0; index < n; index++ {
		builder.WriteByte(alphabet[hongguoRandomByte()%16])
	}
	return builder.String()
}

// hongguoRandomUUID 生成标准 8-4-4-4-12 形态的 UUID 字符串。
func hongguoRandomUUID() string {
	return hongguoRandomHex(8) + "-" + hongguoRandomHex(4) + "-" + hongguoRandomHex(4) + "-" + hongguoRandomHex(4) + "-" + hongguoRandomHex(12)
}

func hongguoRandomByte() int {
	var buffer [1]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return int(time.Now().UnixNano() & 0xff)
	}
	return int(buffer[0])
}

// hongguoRegisterDevice 向服务端注册一台游客设备。
//
// 失败时会重试（接口偶发 SSL EOF）。返回的 ID 必须是纯数字，否则视为无效，
// 因为取流 query 里要用它，非数字会被服务端当成垃圾参数。
func (downloader *Downloader) hongguoRegisterDevice(ctx context.Context) (hongguoDeviceRegistration, error) {
	body, err := json.Marshal(map[string]any{
		"magic_tag": "ss_app_log",
		"header":    hongguoDeviceHeader(),
		"_gen_time": time.Now().Unix(),
	})
	if err != nil {
		return hongguoDeviceRegistration{}, err
	}
	var lastErr error
	for attempt := 0; attempt < hongguoDeviceRegisterAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(providerQuickRetryDelay()):
			case <-ctx.Done():
				return hongguoDeviceRegistration{}, ctx.Err()
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoDeviceRegisterURL, bytes.NewReader(body))
		if err != nil {
			return hongguoDeviceRegistration{}, err
		}
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		request.Header.Set("User-Agent", hongguoAppUserAgent)
		// 千万别自己设 `Accept-Encoding: gzip`。
		//
		// 该接口固定返回 gzip 正文（`Content-Encoding: gzip`，首两字节 `1f8b`）。
		// net/http 的 Transport 只在请求头**没有** Accept-Encoding 时才自动加
		// gzip 并自动解压；一旦自己设了，Transport 就认为调用方要自己处理，
		// 于是 io.ReadAll 拿到的是压缩字节，json.Unmarshal 必然失败 ——
		// 0.8.14 首个构建就是因为这一行导致注册始终失败，取流退回空正文。
		response, err := downloader.doCatalogRequestWithTimeout(request, hongguoDeviceRegisterTimeout)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("红果设备注册不可用（HTTP %d）", response.StatusCode)
			continue
		}
		var decoded struct {
			DeviceID     any    `json:"device_id"`
			InstallID    any    `json:"install_id"`
			DeviceIDStr  string `json:"device_id_str"`
			InstallIDStr string `json:"install_id_str"`
			DeviceToken  string `json:"device_token"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			lastErr = errors.New("红果设备注册响应格式异常")
			continue
		}
		deviceID := firstNonEmpty(decoded.DeviceIDStr, hongguoAnyID(decoded.DeviceID))
		installID := firstNonEmpty(decoded.InstallIDStr, hongguoAnyID(decoded.InstallID))
		// `device_id: 0` 表示 header 没被接受，等于没注册，换一组随机字段再试。
		if !hongguoValidDeviceID(deviceID) || !hongguoValidDeviceID(installID) {
			lastErr = errors.New("红果设备注册未返回有效设备号")
			continue
		}
		return hongguoDeviceRegistration{DeviceID: deviceID, InstallID: installID, Token: decoded.DeviceToken}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("红果设备注册失败")
	}
	return hongguoDeviceRegistration{}, lastErr
}

// hongguoAnyID 兼容数字与字符串两种下发形态。
func hongguoAnyID(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed <= 0 {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

// hongguoValidDeviceID 要求是纯数字且明显大于 0。
func hongguoValidDeviceID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "0" && hongguoNumericID.MatchString(value)
}

// ensureHongguoDevice 保证当前会话用的是**服务端注册过**的设备。
//
// 已经注册过就直接返回；否则注册一次并写回状态。注册失败不阻断取流 ——
// 退回旧的随机设备继续试（大概率空正文，但错误信息会指向注册失败）。
func (downloader *Downloader) ensureHongguoDevice(ctx context.Context) error {
	client := downloader.hongguoClient()
	client.mu.Lock()
	registered := client.state.Registered
	client.mu.Unlock()
	if registered {
		return nil
	}
	// 串行化：并发取流时只注册一次。
	client.deviceMu.Lock()
	defer client.deviceMu.Unlock()
	client.mu.Lock()
	registered = client.state.Registered
	cooling := !client.deviceRetryAfter.IsZero() && time.Now().Before(client.deviceRetryAfter)
	lastErr := client.deviceErr
	client.mu.Unlock()
	if registered {
		return nil
	}
	// 冷却期内不重试：网络不通时连续超时会让每次播放都卡很久。
	if cooling {
		if lastErr != nil {
			return lastErr
		}
		return errHongguoDeviceUnregistered
	}
	registration, err := downloader.hongguoRegisterDevice(ctx)
	if err != nil {
		// 用户主动取消（切集、退出）不算失败，不记冷却，否则切回来还要干等。
		if ctx.Err() == nil {
			client.mu.Lock()
			client.deviceRetryAfter = time.Now().Add(hongguoDeviceRetryCooldown)
			client.deviceErr = err
			client.mu.Unlock()
		}
		return err
	}
	client.mu.Lock()
	client.state.DeviceID = registration.DeviceID
	client.state.InstallID = registration.InstallID
	client.state.Registered = true
	client.deviceRetryAfter = time.Time{}
	client.deviceErr = nil
	client.mu.Unlock()
	return nil
}
