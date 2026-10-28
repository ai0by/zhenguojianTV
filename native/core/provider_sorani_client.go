package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 青空次元（sorani）—— 纯 JSON API，番剧 / 剧场动画 / 特摄三类。
//
// 上游接口（0.8.15 接入，移植自姊妹项目 guoapp 的实测结论）：
//
//	GET /video?page=&size=&categoryId=        目录
//	GET /video/{id}                           详情（含 episodes[]）
//	GET /video/search?keyword=&page=&size=    搜索
//	GET /video/tags                           标签（未用于目录，保留扩展位）
//	GET /video/episode/{episodeId}/play?lineCode=anime_jp_m3u8   播放
//
// 默认线路 anime_jp_m3u8 是实测有效线；lineCode 必须精确匹配，
// 其余取值返回空 playUrl。
const (
	soraniSiteBaseURL = "https://www.sorani.net"
	soraniUserAgent   = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
)

// soraniAPIBaseURL 用变量而不是常量：单测要把它指到 httptest 服务器上。
var soraniAPIBaseURL = "https://api.sorani.cc/sorani-cms/api"

// soraniPlayCacheTTL 是"分集 → 播放地址"的本地复用窗口。
//
// 实测（2026-10-28，GitHub runner）：同一个 episodeId 连着请求两次 play 接口，
// **第二次返回空 playUrl**。而我们的链路里同一集被解析两次是常态 ——
// 预载失败回退、解析后又要预缓存、用户连点两次切集……都会撞上。
// 所以换出来的地址必须记一小段时间，不能每次都去问上游。
const soraniPlayCacheTTL = 90 * time.Second

type soraniPlayEntry struct {
	address string
	expires time.Time
}

type soraniAPIClient struct {
	downloader *Downloader
	base       string
	site       string
	playMu     sync.Mutex
	play       map[string]soraniPlayEntry
}

// cachedPlay 取"最近换出来"的播放地址；过期或没有就返回空。
func (client *soraniAPIClient) cachedPlay(episodeID string) string {
	client.playMu.Lock()
	defer client.playMu.Unlock()
	entry, ok := client.play[episodeID]
	if !ok {
		return ""
	}
	if !time.Now().Before(entry.expires) {
		delete(client.play, episodeID)
		return ""
	}
	return entry.address
}

func (client *soraniAPIClient) rememberPlay(episodeID, address string) {
	if episodeID == "" || address == "" {
		return
	}
	client.playMu.Lock()
	defer client.playMu.Unlock()
	if client.play == nil {
		client.play = map[string]soraniPlayEntry{}
	}
	// 顺手清掉过期的；还超过上限就整表重置（单集地址本来就很短命）。
	if len(client.play) >= 64 {
		now := time.Now()
		for key, entry := range client.play {
			if !now.Before(entry.expires) {
				delete(client.play, key)
			}
		}
		if len(client.play) >= 64 {
			client.play = map[string]soraniPlayEntry{}
		}
	}
	client.play[episodeID] = soraniPlayEntry{
		address: address,
		expires: time.Now().Add(soraniPlayCacheTTL),
	}
}

func (d *Downloader) soraniClient() *soraniAPIClient {
	d.soraniOnce.Do(func() {
		d.sorani = &soraniAPIClient{
			downloader: d,
			base:       strings.TrimRight(soraniAPIBaseURL, "/"),
			site:       d.providerBaseURL(sourceSorani),
		}
	})
	return d.sorani
}

func (client *soraniAPIClient) get(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	base, err := url.Parse(client.base)
	if err != nil || !isProviderHTTPMediaURL(client.base) || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("青空次元接口地址无效")
	}
	timeout := 15 * time.Second
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := client.base + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("无法创建青空次元请求")
	}
	// 千万别自己设 `Accept-Encoding`：net/http 的 Transport 只在请求头
	// **没有**这个头时才自动协商并解压。0.8.14 红果设备注册就是栽在这一行
	// （拿到压缩字节 → json.Unmarshal 必失败），这里不再重复。
	request.Header.Set("User-Agent", soraniUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", client.site+"/")
	request.Header.Set("Origin", client.site)

	d := client.downloader
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	// 与黄剧客户端一致：接口请求不带共享 Cookie Jar，且只允许同源重定向。
	transport := *d.client
	transport.Jar = nil
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || providerMediaOrigin(next.URL) != providerMediaOrigin(base) {
			return errors.New("青空次元接口重定向地址异常")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := transport.Do(request)
	if err != nil {
		return nil, fmt.Errorf("青空次元连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	observeSourceResponse(ctx, response)
	blocked := catalogResponseBlockReason(response, data) != ""
	if response.StatusCode < 200 || response.StatusCode >= 300 || blocked {
		return nil, d.catalogResponseError(request, response, data)
	}
	if readErr != nil || int64(len(data)) > limit {
		return nil, errors.New("青空次元返回的数据过大或读取失败")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	// 上游统一用 code=200 表示成功；非 200 一律视为业务错误。
	if row, ok := decoded.(map[string]any); ok {
		if code := nativeText(row["code"]); code != "" && code != "200" {
			message := mapString(row, "message", "msg")
			if message == "" {
				message = "上游返回错误码 " + code
			}
			return nil, errors.New("青空次元接口错误：" + truncate(message, 120))
		}
	}
	return decoded, nil
}
