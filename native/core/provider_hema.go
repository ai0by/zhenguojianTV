package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 河马剧场（hema）原生站源，0.8.17 接入。
//
// 上游是 Next.js 站，页面数据全在 `__NEXT_DATA__` 里，所以「目录 / 详情 / 搜索」
// 三条链路都不需要逆向接口：
//
//	GET  /browse/{typeThreeId}/{page}   分类目录（全部用 0，page=1 会 307 回 /browse/{id}）
//	GET  /drama/{bookId}                详情 + 完整 chapterList
//	POST /seo/video/6007                搜索（body {"sourceType":1,"keyword","index"}）
//
// 但**网页只放出前 5 集**：`chapterList` 里 `isCharge=1` 的分集根本没有
// `chapterVideoVo`（实测 91 集的剧只有第 1~5 集带地址）。真正的地址要走 App
// 门户接口 `POST https://freevideo.zqqds.cn/free-video-portal/portal/1139`，
// 请求体与响应体都是 AES-128-CBC 加密（见 hemaAppKey / hemaAppIV）。
//
// 这条 App 链路是 0.8.17 接入的关键：不接它，河马就只能播 5 集，等于残废。
// 实测（2026-10-30）第 1 集、第 6 集、第 51 集都返回 206 video/mp4 可播，
// 且不需要 Referer / Cookie。
const (
	hemaMaxPage    = 100000
	hemaMaxKeyword = 64
	hemaMaxID      = 32
	// hemaPageSize 是上游分类页固定条数（实测每页 10 条，不能改）。
	hemaPageSize = 10
	// hemaMaxCategories 限制分类入口数量。上游 typeThreeList 有 126 项，
	// 全塞进横向 chip 条根本没法用，这里按「热门优先 + 站点原序」截断。
	hemaMaxCategories = 40
)

// 两个基址用变量而不是常量：单测要把它指到 httptest 服务器上。
var (
	hemaSiteBaseURL   = "https://www.kuaikaw.cn"
	hemaAppAPIBaseURL = "https://freevideo.zqqds.cn"
)

// 河马 App 门户接口的对称密钥。base64 解码后都是 16 字节 → AES-128-CBC / PKCS7。
//
//	key: ZHpramdmeXhnc2h5bGd6bQ== → dzkjgfyxgshylgzm
//	iv : YXBpdXBkb3duZWRjcnlwdA== → apiupdownedcrypt
//
// 这两个值来自上游 App 的客户端实现（OmniBox-Spider 的「河马短剧」采集脚本），
// 属于**固定常量**，不是每台设备一换。改版时若接口报解密失败，先回来核对这里。
var (
	hemaAppKey = []byte("dzkjgfyxgshylgzm")
	hemaAppIV  = []byte("apiupdownedcrypt")
)

// 门户接口要求的设备指纹。实测匿名即可，服务端会给一个游客 userId。
// 保持固定值（而不是每次随机）更接近真实客户端，也不会触发设备风控。
const (
	hemaAppUtdidTmp  = "A20260731163429368CCKSq5"
	hemaAppUtdid     = "80e5f8f1406e0cd7ab8aecaab158d693"
	hemaAppInstall   = int64(1785483288736)
	hemaAppVersion   = "3.7.0"
	hemaAppPname     = "com.dz.hmjc"
	hemaAppChannel   = "VHSE1000000"
	hemaAppUserAgent = "okhttp/4.10.0"
)

var (
	// reHemaNextData 抓 Next.js 的服务端渲染数据块。
	reHemaNextData = regexp.MustCompile(`(?is)<script[^>]*id=["']__NEXT_DATA__["'][^>]*>(.*?)</script>`)
	// hemaCategoryIDPattern 分类 ID 是纯数字（0 表示「全部」）。
	hemaCategoryIDPattern = regexp.MustCompile(`^[0-9]{1,10}$`)
	hemaBookIDPattern     = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// hemaPreferredCategoryNames 是「热门分类」，排在最前面。
// 其余分类按站点原序跟在后面，凑满 hemaMaxCategories 为止。
var hemaPreferredCategoryNames = []string{
	"甜宠", "古装仙侠", "现代言情", "青春", "豪门恩怨",
	"逆袭", "重生", "系统", "总裁", "职场商战",
}

func validHemaCategory(category string) bool {
	if category == "" {
		return true
	}
	return hemaCategoryIDPattern.MatchString(category)
}

func hemaBookID(value string) bool {
	return hemaBookIDPattern.MatchString(strings.TrimSpace(value))
}

// hemaNextDataProps 从 HTML 里取出 `__NEXT_DATA__.props.pageProps`。
func hemaNextDataProps(body string) (map[string]any, error) {
	match := reHemaNextData.FindStringSubmatch(body)
	if len(match) < 2 {
		return nil, errors.New("河马剧场页面结构异常（未找到页面数据）")
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(match[1]))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("河马剧场页面数据格式无效")
	}
	root, _ := decoded.(map[string]any)
	props, _ := root["props"].(map[string]any)
	pageProps, _ := props["pageProps"].(map[string]any)
	if pageProps == nil {
		return nil, errors.New("河马剧场页面数据不完整")
	}
	return pageProps, nil
}

// hemaDramaFromMap 把 bookList / bookInfoVo 里的记录映射成 Drama。
// 列表、搜索、详情三处的书字段完全一致（实测），所以共用一份映射。
func hemaDramaFromMap(row map[string]any) (Drama, error) {
	if row == nil {
		return Drama{}, errors.New("河马剧场返回的剧集信息不完整")
	}
	id := strings.TrimSpace(mapString(row, "bookId"))
	title := strings.TrimSpace(mapString(row, "bookName"))
	if !hemaBookID(id) || title == "" {
		return Drama{}, errors.New("河马剧场返回的剧集信息不完整")
	}
	description := mapString(row, "introduction", "desc")
	cover := providerCoverAddress(firstAnyValue(row, "coverWap", "cover", "coverUrl"), hemaSiteBaseURL+"/")

	episodes := 0
	for _, key := range []string{"totalChapterNum", "chapterNum", "totalChapter"} {
		if count, err := strconv.Atoi(nativeText(row[key])); err == nil && count > 0 && count <= 100000 {
			episodes = count
			break
		}
	}

	category := "短剧"
	tags := make([]string, 0, 8)
	seen := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || len([]rune(value)) > 24 || len(tags) >= 12 {
			return
		}
		seen[value] = true
		tags = append(tags, value)
	}
	if rows, ok := row["bookTypeThree"].([]any); ok {
		for _, entry := range rows {
			item, _ := entry.(map[string]any)
			addTag(mapString(item, "name"))
		}
	}
	if actor := mapString(row, "actor"); actor != "" {
		addTag("主演 " + truncate(actor, 16))
	}
	if actress := mapString(row, "actress"); actress != "" {
		addTag("主演 " + truncate(actress, 16))
	}
	if desc := mapString(row, "statusDesc"); desc != "" {
		addTag(truncate(desc, 16))
	}
	if followers := mapString(row, "followCount"); followers != "" {
		addTag(truncate(followers, 16) + "追剧")
	}

	return Drama{
		ID: providerDramaID(sourceHema, id), Source: sourceHema, SourceID: id,
		Title: truncate(title, 256), Name: truncate(title, 256),
		Desc: truncate(description, 12000), Intro: truncate(description, 12000),
		Cover: cover, CoverURL: cover,
		TotalEpisode: episodes, EpisodeCount: episodes,
		Category: category, ChannelName: "河马剧场",
		Tags: tags,
	}, nil
}

// hemaCategoryFromList 把 typeThreeList 转成可展示的分类。
// 上游一项可能带多个 ID（如「萌宝」= [589,1175]），取第一个非 0 的作为入口。
func hemaCategoriesFromList(rows []any) []nativeCategory {
	entries := make([]nativeCategory, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		item, _ := entry.(map[string]any)
		if item == nil {
			continue
		}
		name := strings.TrimSpace(mapString(item, "name"))
		if name == "" || len([]rune(name)) > 16 {
			continue
		}
		ids, _ := item["typeThreeIds"].([]any)
		id := ""
		for _, value := range ids {
			candidate := strings.TrimSpace(nativeText(value))
			if hemaCategoryIDPattern.MatchString(candidate) && candidate != "0" {
				id = candidate
				break
			}
		}
		// 「全部」在上游是 typeThreeIds=[0]，nativeCategories 已经自带这一项。
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		entries = append(entries, nativeCategory{ID: id, Name: name})
	}
	// 热门分类提前，其余保持站点原序。
	ordered := make([]nativeCategory, 0, len(entries))
	picked := map[string]bool{}
	for _, name := range hemaPreferredCategoryNames {
		for _, entry := range entries {
			if entry.Name == name && !picked[entry.ID] {
				picked[entry.ID] = true
				ordered = append(ordered, entry)
			}
		}
	}
	for _, entry := range entries {
		if picked[entry.ID] {
			continue
		}
		ordered = append(ordered, entry)
	}
	if len(ordered) > hemaMaxCategories {
		ordered = ordered[:hemaMaxCategories]
	}
	return ordered
}

// hemaGet 走公共的目录请求通道（限速、代理回落、重试都在那一层）。
func (d *Downloader) hemaGet(ctx context.Context, address, referer string) (string, error) {
	return d.fetchProviderText(ctx, address, referer)
}

// hemaPostJSON 发一次 JSON POST。搜索接口只认 POST，公共通道只有 GET 版本。
func (d *Downloader) hemaPostJSON(ctx context.Context, address string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("河马剧场请求参数无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("无法创建河马剧场请求")
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Origin", hemaSiteBaseURL)
	request.Header.Set("Referer", hemaSiteBaseURL+"/search")
	// 上游按这个头识别站点；不带会返回 403。
	request.Header.Set("pname", "www.kuaikaw.cn")

	timeout := providerTimeout
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background {
		timeout = 8 * time.Second
	}
	response, err := d.doCatalogRequestWithTimeout(request, timeout)
	if err != nil {
		return nil, fmt.Errorf("河马剧场连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	if readErr != nil {
		return nil, errors.New("河马剧场返回的数据读取失败")
	}
	if int64(len(data)) > providerMaxBodyBytes {
		return nil, errors.New("河马剧场返回的数据过大")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || catalogResponseBlockReason(response, data) != "" {
		return nil, d.catalogResponseError(request, response, data)
	}
	return data, nil
}

// fetchHemaCategories 从 /browse 的页面数据里取分类表。
// 上游分类是站点配置的一部分，不随内容变化，但仍然每次都取一次
// （nativeCategories 有进程内缓存，实际不会频繁请求）。
func (d *Downloader) fetchHemaCategories(ctx context.Context) ([]nativeCategory, error) {
	body, err := d.hemaGet(ctx, hemaSiteBaseURL+"/browse", hemaSiteBaseURL+"/")
	if err != nil {
		return nil, err
	}
	pageProps, err := hemaNextDataProps(body)
	if err != nil {
		return nil, err
	}
	rows, _ := pageProps["typeThreeList"].([]any)
	if len(rows) == 0 {
		return nil, errors.New("河马剧场暂未返回内容分类，请稍后重试")
	}
	categories := hemaCategoriesFromList(rows)
	if len(categories) == 0 {
		return nil, errors.New("河马剧场暂未返回内容分类，请稍后重试")
	}
	return categories, nil
}

// fetchHemaCatalogPage 拉一页分类目录；query 非空时走搜索接口。
func (d *Downloader) fetchHemaCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > hemaMaxPage {
		return nil, false, errors.New("河马剧场目录页码无效")
	}
	if !validHemaCategory(category) {
		return nil, false, errors.New("河马剧场内容分类无效")
	}
	if len([]rune(query)) > hemaMaxKeyword {
		return nil, false, errors.New("河马剧场搜索关键词过长")
	}
	if query != "" {
		return d.searchHema(ctx, query, page)
	}
	// 「全部」在上游是 typeThreeId=0。
	if category == "" {
		category = "0"
	}
	address := fmt.Sprintf("%s/browse/%s/%d", hemaSiteBaseURL, category, page)
	body, err := d.hemaGet(ctx, address, hemaSiteBaseURL+"/browse")
	if err != nil {
		return nil, false, err
	}
	pageProps, err := hemaNextDataProps(body)
	if err != nil {
		return nil, false, err
	}
	rows, ok := pageProps["bookList"].([]any)
	if !ok {
		return nil, false, errors.New("河马剧场目录数据格式无效")
	}
	if len(rows) > 200 {
		return nil, false, errors.New("河马剧场目录数据格式无效")
	}
	hasMore := len(rows) >= hemaPageSize
	// 上游的 pages 在越界页会回退成上界值（实测 /browse/1170/28 返回 pages=26），
	// 所以只把「当前页小于总页数」当作有更多，不能反过来用。
	if pages, err := strconv.Atoi(nativeText(pageProps["pages"])); err == nil && pages > 0 {
		hasMore = page < pages
	}
	items := make([]Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		drama, err := hemaDramaFromMap(row)
		if err != nil {
			return nil, false, err
		}
		if seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

// searchHema 走站点自己的搜索接口，分页参数叫 index（从 1 开始）。
func (d *Downloader) searchHema(ctx context.Context, keyword string, page int) ([]Drama, bool, error) {
	payload := map[string]any{"sourceType": 1, "keyword": keyword, "index": page}
	data, err := d.hemaPostJSON(ctx, hemaSiteBaseURL+"/seo/video/6007", payload)
	if err != nil {
		return nil, false, err
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false, errors.New("河马剧场搜索返回的数据格式无效")
	}
	if code := nativeText(decoded["retCode"]); code != "" && code != "0" {
		return nil, false, fmt.Errorf("河马剧场搜索失败（错误码 %s）", truncate(code, 16))
	}
	body, _ := decoded["data"].(map[string]any)
	if body == nil {
		return nil, false, errors.New("河马剧场搜索返回的数据格式无效")
	}
	rows, _ := body["bookList"].([]any)
	if len(rows) > 200 {
		return nil, false, errors.New("河马剧场搜索返回的数据格式无效")
	}
	hasMore := nativeText(body["isMore"]) == "1"
	items := make([]Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		drama, err := hemaDramaFromMap(row)
		if err != nil {
			continue
		}
		if seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

// fetchHemaDetail 拉详情与完整分集表。
//
// 分集里只放 `hema-play://<chapterId>` 占位：真正的播放地址要按 chapterId
// 现取（网页只给前 5 集，App 门户接口才给全），展开详情时不该把 90 集
// 挨个请求一遍。
func (d *Downloader) fetchHemaDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !hemaBookID(sourceID) {
		return Drama{}, nil, errors.New("河马剧场剧集 ID 无效")
	}
	address := hemaSiteBaseURL + "/drama/" + url.PathEscape(sourceID)
	body, err := d.hemaGet(ctx, address, hemaSiteBaseURL+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	pageProps, err := hemaNextDataProps(body)
	if err != nil {
		return Drama{}, nil, err
	}
	info, _ := pageProps["bookInfoVo"].(map[string]any)
	drama, err := hemaDramaFromMap(info)
	if err != nil {
		return Drama{}, nil, err
	}
	if drama.SourceID != sourceID {
		return Drama{}, nil, errors.New("河马剧场详情与请求剧集不符")
	}
	rows, ok := pageProps["chapterList"].([]any)
	if !ok || len(rows) == 0 {
		return Drama{}, nil, errors.New("河马剧场该剧暂无分集")
	}
	if len(rows) > 10000 {
		return Drama{}, nil, errors.New("河马剧场分集目录格式无效")
	}
	chapters := make([]Chapter, 0, len(rows))
	seenIDs := map[string]bool{}
	seenOrders := map[int]bool{}
	for index, entry := range rows {
		row, _ := entry.(map[string]any)
		if row == nil {
			continue
		}
		chapterID := strings.TrimSpace(mapString(row, "chapterId"))
		if !hemaBookID(chapterID) || seenIDs[chapterID] {
			return Drama{}, nil, errors.New("河马剧场分集目录包含无效或重复分集")
		}
		order := index + 1
		if parsed, err := strconv.Atoi(nativeText(row["chapterIndex"])); err == nil && parsed > 0 && parsed <= 100000 {
			order = parsed
		}
		if seenOrders[order] {
			return Drama{}, nil, errors.New("河马剧场分集编号重复，请刷新目录")
		}
		seenIDs[chapterID], seenOrders[order] = true, true
		title := strings.TrimSpace(mapString(row, "chapterName"))
		if title == "" {
			title = fmt.Sprintf("第 %d 集", order)
		}
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceHema, sourceID, chapterID), Source: sourceHema,
			Title: truncate(title, 128), CurrentEpisode: json.RawMessage(strconv.Itoa(order)),
			VideoURL: "hema-play://" + url.PathEscape(chapterID),
			Referer:  hemaSiteBaseURL + "/",
		})
	}
	// chapterList 已经按 chapterIndex 升序返回（实测），这里再排一次兜底。
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		if left == right {
			return chapters[i].ID < chapters[j].ID
		}
		return left < right
	})
	return drama, chapters, nil
}

// hemaAppEnvelope 是门户接口要求的「公共参数」，加密后塞进 `datas` 头。
func hemaAppEnvelope() map[string]any {
	session := hemaRandomUUID()
	return map[string]any{
		"freeflow": 0, "version": hemaAppVersion, "pname": hemaAppPname,
		"channelCode": hemaAppChannel, "utdidTmp": hemaAppUtdidTmp, "token": "",
		"utdid": hemaAppUtdid, "os": "android", "osv": 28,
		"brand": "OnePlus", "model": "HD1910", "manu": "OnePlus",
		"userId": "", "launch": "shortcut", "mchid": "", "nchid": hemaAppChannel,
		"session1": session, "session2": session, "startScene": "shortcut",
		"recSwitch": false, "ds": -1, "installTime": hemaAppInstall,
		"p": 59, "nonce": hemaRandomHex(32), "timeZone": "Asia/Shanghai",
		"timestamp": strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
}

func hemaRandomHex(length int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, length)
	seed := time.Now().UnixNano()
	for index := 0; index < length; index++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		out = append(out, digits[byte(seed>>59)&0x0f])
	}
	return string(out)
}

func hemaRandomUUID() string {
	const digits = "0123456789abcdef"
	raw := hemaRandomHex(32)
	out := make([]byte, 0, 36)
	for index, char := range raw {
		if index == 8 || index == 12 || index == 16 || index == 20 {
			out = append(out, '-')
		}
		if index == 12 {
			out = append(out, '4')
			continue
		}
		if index == 16 {
			// y 位固定 8/9/a/b
			out = append(out, digits[8+int(char)%4])
			continue
		}
		out = append(out, byte(char))
	}
	return string(out)
}

func hemaAESEncryptString(plain string) (string, error) {
	encrypted, err := aesCBCEncrypt([]byte(plain), hemaAppKey, hemaAppIV)
	if err != nil {
		return "", errors.New("河马剧场加密请求失败")
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func hemaAESDecryptString(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", errors.New("河马剧场返回的数据无法解码")
	}
	plain, err := aesCBCDecrypt(raw, hemaAppKey, hemaAppIV)
	if err != nil {
		return "", errors.New("河马剧场返回的数据无法解密")
	}
	return string(plain), nil
}

// hemaAppRequest 调一次 App 门户接口并返回解密后的 data。
func (d *Downloader) hemaAppRequest(ctx context.Context, portal string, payload any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("河马剧场播放参数无效")
	}
	encryptedBody, err := hemaAESEncryptString(string(body))
	if err != nil {
		return nil, err
	}
	envelope, err := json.Marshal(hemaAppEnvelope())
	if err != nil {
		return nil, errors.New("河马剧场请求参数无效")
	}
	encryptedEnvelope, err := hemaAESEncryptString(string(envelope))
	if err != nil {
		return nil, err
	}

	address := hemaAppAPIBaseURL + "/free-video-portal/portal/" + url.PathEscape(portal)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(encryptedBody))
	if err != nil {
		return nil, errors.New("无法创建河马剧场请求")
	}
	request.Header.Set("User-Agent", hemaAppUserAgent)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("alg", "HG45LKBS")
	request.Header.Set("datas", encryptedEnvelope)

	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return nil, fmt.Errorf("河马剧场播放接口连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, errors.New("河马剧场播放接口读取失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("河马剧场播放接口返回 HTTP %d", response.StatusCode)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("河马剧场播放接口返回的数据格式无效")
	}
	if code := nativeText(decoded["code"]); code != "" && code != "0" {
		return nil, fmt.Errorf("河马剧场播放接口返回错误码 %s", truncate(code, 16))
	}
	encrypted := nativeText(decoded["data"])
	if encrypted == "" {
		return nil, errors.New("河马剧场播放接口未返回播放数据")
	}
	plain, err := hemaAESDecryptString(encrypted)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder = json.NewDecoder(strings.NewReader(plain))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, errors.New("河马剧场播放数据格式无效")
	}
	return result, nil
}

// resolveHemaMedia 按 chapterId 向 App 门户接口换取直链。
//
// 网页端只放出前 5 集，所以「详情里已经有 mp4」这条捷径靠不住：
// 一律走门户接口，拿到的是 720P 的 mp4（不需要 Referer、不需要 Cookie）。
func (d *Downloader) resolveHemaMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceHema, sourceID, "")
	if !valid || source != sourceHema || !hemaBookID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("河马剧场播放分集信息无效，请刷新详情")
	}
	chapterID := strings.TrimPrefix(task.Chapter.ID, prefix)
	if !hemaBookID(chapterID) {
		return providerMedia{}, errors.New("河马剧场分集 ID 无效")
	}
	data, err := d.hemaAppRequest(ctx, "1139", map[string]any{
		"bookId": sourceID, "chapterIds": []string{chapterID},
		"unClockType": "load", "chapterId": chapterID, "resolutionRate": "720P",
	})
	if err != nil {
		return providerMedia{}, err
	}
	rows, _ := data["chapterInfo"].([]any)
	var content map[string]any
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		if row == nil {
			continue
		}
		if strings.TrimSpace(mapString(row, "chapterId")) != chapterID {
			continue
		}
		content, _ = row["content"].(map[string]any)
		break
	}
	if content == nil && len(rows) == 1 {
		// 上游只回一条时不再要求 chapterId 精确匹配（实测偶发不回该字段）。
		row, _ := rows[0].(map[string]any)
		content, _ = row["content"].(map[string]any)
	}
	if content == nil {
		return providerMedia{}, errors.New("河马剧场该分集暂无可播放地址")
	}
	candidates := make([]string, 0, 6)
	if switches, ok := content["mp4SwitchUrl"].([]any); ok {
		for _, entry := range switches {
			candidates = append(candidates, nativeText(entry))
		}
	}
	candidates = append(candidates,
		nativeText(content["mp4720p"]), nativeText(content["mp4"]),
		nativeText(content["vodMp4Url"]), nativeText(content["mp4Url"]))
	address := ""
	for _, candidate := range candidates {
		if isProviderHTTPMediaURL(candidate) {
			address = candidate
			break
		}
	}
	if address == "" {
		return providerMedia{}, errors.New("河马剧场该分集暂无可播放地址")
	}
	// 直链带签名（`?t=…&sign=…`），只在本进程内直接播放，不要缓存太久。
	return providerMedia{URL: address, Referer: hemaAppAPIBaseURL + "/"}, nil
}
