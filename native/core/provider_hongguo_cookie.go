package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 红果登录态（0.8.14）。
//
// 背景：2026-10-27 起红果对**匿名**访问关闭了正片 ——
//
//   - 公开片源 `/video/public/{vid}/360p.mp4` 只有前 3 集，而且都是固定
//     10 秒预告（`mvhd` duration=10000/timescale=1000），第 4 集起 404；
//   - 网页播放页是服务端渲染的，匿名访问时 `protectedPlayback: true`、
//     `videoList` 为空，前端 JS 只会拼出上面那个预告地址；
//   - App 取流接口 `/novel/player/multi_video_model/v1/` 对匿名请求返回
//     `200 + 空正文`（不是签名问题：详情接口用同一套签名是正常的）。
//
// 也就是说，红果正片现在需要**登录态**。这里让用户把网页登录后的 Cookie
// 粘进来，我们把它带到网页播放页与 App 接口上 —— 播放页的 SSR 拿到有效
// sessionid 后才会渲染出真实片源。
//
// 设计取舍：
//   - 只做"带 Cookie"，不在客户端实现登录（手机号/扫码登录涉及验证码、
//     滑块与 passport 签名，做不了也不该做）。
//   - 不填也能用：三条匿名链路照旧尝试，只是大概率拿不到正片。
//   - Cookie 只存在本机设置里（按用户档案隔离），不上传、不写日志。

// hongguoCookieMaxLength 限制一次粘贴的长度，避免把整段请求头塞进来。
const hongguoCookieMaxLength = 8192

// hongguoNormalizeCookie 把用户粘贴的内容整理成合法的 Cookie 头。
//
// 兼容三种粘贴形态：整行 `Cookie: a=1; b=2`、多行 `a=1\nb=2`、以及
// 从开发者工具里复制的带分号结尾的片段。
func hongguoNormalizeCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"Cookie:", "cookie:", "COOKIE:"} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))
			break
		}
	}
	var parts []string
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, ";")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	joined := strings.Join(parts, "; ")
	if len(joined) > hongguoCookieMaxLength {
		joined = joined[:hongguoCookieMaxLength]
	}
	return joined
}

// setHongguoCookie 保存登录态；传空字符串表示清除。
func (d *Downloader) setHongguoCookie(raw string) {
	cookie := hongguoNormalizeCookie(raw)
	d.hongguoCookieMu.Lock()
	d.hongguoCookieText = cookie
	d.hongguoCookieMu.Unlock()
}

// hongguoCookieHeader 取出当前登录态，没配置时返回空串。
func (d *Downloader) hongguoCookieHeader() string {
	d.hongguoCookieMu.RLock()
	defer d.hongguoCookieMu.RUnlock()
	return d.hongguoCookieText
}

// hongguoCookieConfigured 供设置页回显"是否已配置"。
func (d *Downloader) hongguoCookieConfigured() bool {
	return d.hongguoCookieHeader() != ""
}

// fetchHongguoText 抓网页文本；配置了登录态时带上 Cookie。
//
// 没配置时直接走通用的 fetchProviderText，保持与其它站源一致的
// 镜像竞速与重试行为。
func (d *Downloader) fetchHongguoText(ctx context.Context, rawURL, referer string) (string, error) {
	cookie := d.hongguoCookieHeader()
	if cookie == "" {
		return d.fetchProviderText(ctx, rawURL, referer)
	}
	if referer == "" {
		referer = rawURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", referer)
	request.Header.Set("Cookie", cookie)
	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("红果页面不可用（HTTP %d）", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
