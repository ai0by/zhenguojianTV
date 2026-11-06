package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// 苹果CMS（MacCMS）通用采集适配器（0.8.11）。
//
// 国内短剧站里苹果CMS v10 的模板改装占了绝大多数，接口形态统一、模板千差万别。
// 这一层只做两件事：
//
//  1. 每个站用 maccmsProfile 描述**差异**（详情路由、播放路由、列表路由、搜索路由）；
//  2. 解析（卡片 / 选集 / 播放地址）全部共享，靠"class 后缀 + 子树里只有一个详情链接"
//     来认卡片，而不是写死每站的 class 名。
//
// 当前接入四个实测站（目录 / 详情 / 播放地址都跑通过）：
//
//	花果 huaguo   https://www.zywest263.com   详情 /zywview/<id>.html   播放 /zywplay/…
//	网果 wangguo  https://www.duanju2.com     详情 /vod/<id>.html       播放 /play/…
//	发果 faguo    https://www.xzyx168.com     详情 /xzyxvd/<id>.html    播放 /xzyxvp/…
//	伍果 wuguo    https://www.duanju55.com    详情 /index.php/vod/detail/id/<id>.html
//
// 注意"苹果CMS 只是接口形态统一"这件事：四个站里花果 / 网果 / 伍果都是
// **整部剧一个视频**（选集区只有"全集"一条），只有发果按集拆开。
// 加新站必须一站一站验，不能批量上架。

const (
	sourceHuaguo  = "huaguo"
	sourceWangguo = "wangguo"
	sourceFaguo   = "faguo"
	sourceWuguo   = "wuguo"

	// 0.8.25：两个动漫番剧站（用户要求接入，实测列表/详情/播放全通）。
	//
	// 与上面四个短剧站的关键差别：**它们是按集拆开的**（MK 动漫一部 12~24 集、
	// 囧次元一部 12~19 集），而且清晰度普遍 720p~1080p —— 短剧站那种"整部剧
	// 一个视频"的假设在这里不成立，所以 chapter 解析必须走通用路径。
	sourceMxdm  = "mxdm"
	sourceJcydm = "jcydm"
)

var (
	huaguoBaseURL  = "https://www.zywest263.com"
	wangguoBaseURL = "https://www.duanju2.com"
	faguoBaseURL   = "https://www.xzyx168.com"
	wuguoBaseURL   = "https://www.duanju55.com"

	// MX 动漫（Kazumi 规则库里的 MXdm）。h5.dcc3.com 是它的主域名。
	mxdmBaseURL = "https://www.dcc3.com"
	// 囧次元（Kazumi 规则库里的 9ciyuan）。域名会漂移，改这里即可。
	jcydmBaseURL = "https://www.jcydm1.com"

	maccmsNumericID = regexp.MustCompile(`^[0-9]{1,12}$`)

	// maccmsSaneEpisodeCeiling 是"角标上写的集数"能接受的上限。
	//
	// 短剧单集 60~120 秒，几百集已经是长剧；超过这个数基本都是角标文本
	// 跨卡片串了（实测出现过 `全110109集`）。宁可显示"集数未知"，也不能让
	// 详情页顶着「共 110109 集」这种明显荒唐的数字。
	maccmsSaneEpisodeCeiling = 999

	// 播放链接里的线路 / 集号（0.8.24）。两种模板各一条：
	//   /play/15269-2-70.html              → sid=2 nid=70
	//   /index.php/vod/play/id/50001/sid/1/nid/1.html → sid=1 nid=1
	maccmsPlayRouteDash  = regexp.MustCompile(`(?i)/play/[0-9]+-([0-9]+)-([0-9]+)\.html$`)
	maccmsPlayRouteNamed = regexp.MustCompile(`(?i)/vod/play/id/[0-9]+/sid/([0-9]+)/nid/([0-9]+)\.html$`)

	// 播放页里播放器配置的 JSON：`<script>var player_aaaa={...}</script>`。
	maccmsPlayerJSON  = regexp.MustCompile(`(?s)player_aaaa\s*=\s*(\{.*?\})\s*</script>`)
	maccmsPlayerField = regexp.MustCompile(`(?i)"url"\s*:\s*"([^"]+)"`)
	// 兜底：整页里裸的 m3u8/mp4 地址。花果的模板把地址拼在 iframe src 的 JS 串里，
	// 没有 player_aaaa，只能这样捞。
	maccmsMediaURL  = regexp.MustCompile(`(?i)(?:https?:)?//[^\s"'<>\\]+?\.(?:m3u8|mp4)(?:\?[^\s"'<>\\]*)?`)
	maccmsIframeTag = regexp.MustCompile(`(?is)<iframe\b[^>]*>`)
	maccmsTagAttr   = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*["']([^"']*)["']`)

	// 详情页标题清理：`短剧《X》全集免费高清在线观看_…`、`X_高清完整版在线观看_…`。
	maccmsTitleBracket = regexp.MustCompile(`《([^》]{1,80})》`)
	maccmsTitleScore   = regexp.MustCompile(`\s*[0-9]{1,2}\.[0-9]\s*$`)
)

var maccmsTitleSuffixes = []string{
	"全集免费高清在线观看", "高清完整版在线观看", "免费高清在线观看",
	"免费在线观看", "高清免费观看", "在线观看", "在线播放",
	"高清完整版", "完整版", "全集", "高清", "免费",
}

// maccmsProfile 描述一个苹果CMS 站点的**差异部分**。
type maccmsProfile struct {
	Source string
	Name   string
	Base   string
	// DetailPath 从详情页链接的 path 里认剧并取出剧集 ID。
	DetailPath *regexp.Regexp
	// PlayPath 从详情页里认「分集 / 播放」链接的 path。
	PlayPath *regexp.Regexp
	// DetailURL 按剧集 ID 拼详情页地址。
	DetailURL func(base, id string) string
	// ListURL 拼目录页（page 从 1 开始）。返回空串表示该站没有这一页。
	ListURL func(base string, page int) string
	// SearchURL 拼搜索页；nil 表示该站的搜索被 WAF 拦死（网果），只走本地缓存搜索。
	SearchURL func(base, query string, page int) string
}

var maccmsProfiles = map[string]maccmsProfile{
	sourceHuaguo: {
		Source:     sourceHuaguo,
		Name:       "花果",
		Base:       huaguoBaseURL,
		DetailPath: regexp.MustCompile(`(?i)^/zywview/([0-9]+)\.html$`),
		PlayPath:   regexp.MustCompile(`(?i)^/zywplay/[0-9]+-[0-9]+-[0-9]+\.html$`),
		DetailURL: func(base, id string) string {
			return base + "/zywview/" + id + ".html"
		},
		ListURL: func(base string, page int) string {
			// 27 = 短剧分类；第 2 页起是 /zywtype/27-2.html。
			if page <= 1 {
				return base + "/zywtype/27.html"
			}
			return base + "/zywtype/27-" + strconv.Itoa(page) + ".html"
		},
		SearchURL: func(base, query string, page int) string {
			// 这个模板的搜索**不认 page 参数**（实测 page=2 返回"第0页"空结果），
			// 所以只给第一页，翻页交给"本地缓存 + 提示换关键词"。
			if page > 1 {
				return ""
			}
			return base + "/search.html?searchtype=1&searchword=" + url.QueryEscape(query)
		},
	},
	sourceWangguo: {
		Source: sourceWangguo,
		Name:   "网果",
		Base:   wangguoBaseURL, DetailPath: regexp.MustCompile(`(?i)^/vod/([0-9]+)\.html$`),
		PlayPath: regexp.MustCompile(`(?i)^/play/[0-9]+-[0-9]+-[0-9]+\.html$`),
		DetailURL: func(base, id string) string {
			return base + "/vod/" + id + ".html"
		},
		ListURL: func(base string, page int) string {
			if page <= 1 {
				return base + "/type/duanju.html"
			}
			return base + "/type/duanju/page/" + strconv.Itoa(page) + ".html"
		},
		// 网果的 /search/ 一律返回"安全验证"页（带 cookie 也一样），
		// 与其给用户一个必然失败的入口，不如退回本地缓存搜索。
		SearchURL: nil,
	},
	sourceFaguo: {
		Source:     sourceFaguo,
		Name:       "发果",
		Base:       faguoBaseURL,
		DetailPath: regexp.MustCompile(`(?i)^/xzyxvd/([0-9]+)\.html$`),
		PlayPath:   regexp.MustCompile(`(?i)^/xzyxvp/[0-9]+n[0-9]+m[0-9]+\.html$`),
		DetailURL: func(base, id string) string {
			return base + "/xzyxvd/" + id + ".html"
		},
		ListURL: func(base string, page int) string {
			// 发果的短剧分类只有一页（实测整页 42 部，没有分页器）。
			if page > 1 {
				return ""
			}
			return base + "/xzyxvt/duanjun.html"
		},
		SearchURL: func(base, query string, page int) string {
			if page < 1 {
				page = 1
			}
			return base + "/xzyxvc/" + url.PathEscape(query) + "-wdyswzqun" + strconv.Itoa(page) + "num.html"
		},
	},
	sourceWuguo: {
		Source:     sourceWuguo,
		Name:       "伍果",
		Base:       wuguoBaseURL,
		DetailPath: regexp.MustCompile(`(?i)^/index\.php/vod/detail/id/([0-9]+)\.html$`),
		PlayPath:   regexp.MustCompile(`(?i)^/index\.php/vod/play/id/[0-9]+/sid/[0-9]+/nid/[0-9]+\.html$`),
		DetailURL: func(base, id string) string {
			return base + "/index.php/vod/detail/id/" + id + ".html"
		},
		ListURL: func(base string, page int) string {
			if page <= 1 {
				return base + "/index.php/vod/type/id/1.html"
			}
			return base + "/index.php/vod/type/id/1/page/" + strconv.Itoa(page) + ".html"
		},
		SearchURL: func(base, query string, page int) string {
			if page < 1 {
				page = 1
			}
			return base + "/index.php/vod/search.html?wd=" + url.QueryEscape(query) + "&page=" + strconv.Itoa(page)
		},
	},
	// MX 动漫（0.8.25 接入，逐项实测通过）：
	//   列表 /type/riman/          日漫第 1 页 36 部，分页共 248 页
	//   搜索 /search/?wd=<kw>      火影 12 部
	//   详情 /detail/20162/        108 集
	//   播放 /play/20162-1-1/      Artplayer 内嵌 m3u8，直链可拉（720p 多码率）
	//
	// 注意分页格式是 /type/riman/2/（**结尾带斜杠**），不是常见的
	// /type/riman/page/2/ —— 后者直接 404，别按常规套路套。
	sourceMxdm: {
		Source:     sourceMxdm,
		Name:       "MX动漫",
		Base:       mxdmBaseURL,
		DetailPath: regexp.MustCompile(`(?i)^/detail/([0-9]+)/?$`),
		PlayPath:   regexp.MustCompile(`(?i)^/play/[0-9]+-[0-9]+-[0-9]+/?$`),
		DetailURL: func(base, id string) string {
			return base + "/detail/" + id + "/"
		},
		ListURL: func(base string, page int) string {
			// 日漫分类（riman）。首页 / 是全部最新，作为第 1 页的入口。
			if page <= 1 {
				return base + "/type/riman/"
			}
			return base + "/type/riman/" + strconv.Itoa(page) + "/"
		},
		SearchURL: func(base, query string, page int) string {
			// 搜索只有一页（实测 12 条上限），不认 page 参数。
			_ = page
			return base + "/search/?wd=" + url.QueryEscape(query)
		},
	},
	// 囧次元（0.8.25 接入，逐项实测通过）：
	//   列表 /index.php/vod/type/id/21.html   第 1 页 53 部
	//   搜索 /index.php/vod/search/wd/<kw>.html  火影 16 部
	//   详情 /index.php/vod/detail/id/7040.html  《我独自升级 第二季》
	//   播放 /index.php/vod/play/id/7040/sid/1/nid/1.html
	//        地址藏在 <script> 的 JSON 里（"url":"...m3u8"），1080x608 可拉
	//
	// 与伍果的路径结构一致（都是标准 MacCMS v10），但**分类 id 不同**：
	// 伍果用 id=1，囧次元动漫在 id=21（21 日漫 / 23 国漫）。
	sourceJcydm: {
		Source:     sourceJcydm,
		Name:       "囧次元",
		Base:       jcydmBaseURL,
		DetailPath: regexp.MustCompile(`(?i)^/index\.php/vod/detail/id/([0-9]+)\.html$`),
		PlayPath:   regexp.MustCompile(`(?i)^/index\.php/vod/play/id/[0-9]+/sid/[0-9]+/nid/[0-9]+\.html$`),
		DetailURL: func(base, id string) string {
			return base + "/index.php/vod/detail/id/" + id + ".html"
		},
		ListURL: func(base string, page int) string {
			if page <= 1 {
				return base + "/index.php/vod/type/id/21.html"
			}
			return base + "/index.php/vod/type/id/21/page/" + strconv.Itoa(page) + ".html"
		},
		SearchURL: func(base, query string, page int) string {
			if page < 1 {
				page = 1
			}
			if page == 1 {
				return base + "/index.php/vod/search/wd/" + url.PathEscape(query) + ".html"
			}
			return base + "/index.php/vod/search/wd/" + url.PathEscape(query) +
				"/page/" + strconv.Itoa(page) + ".html"
		},
	},
}

func maccmsProfileFor(source string) (maccmsProfile, bool) {
	profile, found := maccmsProfiles[canonicalProviderSource(source)]
	return profile, found
}

func maccmsMirrorHost(host string) bool {
	return maccmsSourceForHost(host) != ""
}

// maccmsSourceForHost 反查域名属于哪个苹果CMS 站源。
func maccmsSourceForHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	for _, profile := range maccmsProfiles {
		parsed, err := url.Parse(profile.Base)
		if err == nil && strings.EqualFold(parsed.Hostname(), host) {
			return profile.Source
		}
	}
	return ""
}

// maccmsPath 取链接的 path 部分。站点的详情链接有相对也有绝对（伍果首页就是绝对地址），
// 统一先解析再匹配，避免把 `https://host/zywview/1.html` 判成非详情页。
func maccmsPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Path
}

func maccmsDetailID(raw string, profile maccmsProfile) string {
	path := maccmsPath(raw)
	if path == "" || profile.DetailPath == nil {
		return ""
	}
	if matches := profile.DetailPath.FindStringSubmatch(path); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// maccmsRawAttr 从原始标签字符串里取属性（给 iframe 这类还没解析的片段用）。
func maccmsRawAttr(tag string, names ...string) string {
	for _, match := range maccmsTagAttr.FindAllStringSubmatch(tag, -1) {
		for _, name := range names {
			if strings.EqualFold(match[1], name) && strings.TrimSpace(match[2]) != "" {
				return strings.TrimSpace(match[2])
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------- 目录 / 卡片

func (d *Downloader) fetchMaccmsCatalogPage(ctx context.Context, source string, page int, query string) ([]Drama, bool, error) {
	profile, found := maccmsProfileFor(source)
	if !found {
		return nil, false, errors.New("该站源不支持网页采集")
	}
	if page < 1 {
		page = 1
	}
	base := strings.TrimRight(d.providerBaseURL(source), "/")
	var address string
	if strings.TrimSpace(query) != "" {
		if profile.SearchURL == nil {
			return nil, false, fmt.Errorf("%s 暂不支持在线搜索", profile.Name)
		}
		address = profile.SearchURL(base, strings.TrimSpace(query), page)
	} else {
		address = profile.ListURL(base, page)
	}
	// 空地址表示"这个站没有这一页"（发果不分页、花果搜索不分页），
	// 不是错误：交给上层按 hasMore=false 收手。
	if address == "" {
		return nil, false, nil
	}
	body, err := d.fetchProviderText(ctx, address, base+"/")
	if err != nil {
		return nil, false, err
	}
	items := maccmsCards(body, profile, base)
	return items, len(items) > 0, nil
}

// maccmsDistinctDetailLinks 数子树里有几个**不同的**详情链接。
//
// 这是认卡片的锚点：从详情链接的 <a> 往上爬，只要父节点里仍然只有这一个
// 详情链接，说明父节点还是同一张卡片；一旦爬进列表容器（多个详情链接）就停。
// 比写死 class 名稳，因为苹果CMS 模板会给 class 加每站随机的前缀。
func maccmsDistinctDetailLinks(node *html.Node, profile maccmsProfile) int {
	seen := map[string]bool{}
	for _, anchor := range maccmsNodes(node, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		if id := maccmsDetailID(maccmsAttr(anchor, "href"), profile); id != "" {
			seen[id] = true
			if len(seen) > 1 {
				return len(seen)
			}
		}
	}
	return len(seen)
}

func maccmsCardContainer(anchor *html.Node, profile maccmsProfile) *html.Node {
	card := anchor
	for depth := 0; depth < 6; depth++ {
		parent := card.Parent
		if parent == nil || parent.Type != html.ElementNode {
			break
		}
		if maccmsTag(parent, "body", "html", "head") {
			break
		}
		if maccmsDistinctDetailLinks(parent, profile) > 1 {
			break
		}
		card = parent
	}
	return card
}

func maccmsCardTitle(card, anchor *html.Node, profile maccmsProfile, remark string) string {
	for _, name := range []string{"h1", "h2", "h3", "h4"} {
		node := maccmsFind(card, func(current *html.Node) bool { return maccmsTag(current, name) })
		if node == nil {
			continue
		}
		if text := maccmsText(node); text != "" {
			return text
		}
	}
	if title := maccmsAttr(anchor, "title", "aria-label"); title != "" {
		return title
	}
	// 伍果的 wap 模板把每张卡片的 <img alt> 都写成同一个占位名（实测 24 张卡片
	// 全是"疯狗传"），alt 完全不能当标题用。这种情况下只能取"指向同一部剧、
	// 且不是状态角标"的那条文字链接。
	target := maccmsDetailID(maccmsAttr(anchor, "href"), profile)
	for _, link := range maccmsNodes(card, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		if target == "" || maccmsDetailID(maccmsAttr(link, "href"), profile) != target {
			continue
		}
		text := maccmsAnchorLabel(link)
		if text == "" || text == remark || maccmsLooksLikeRemark(text) {
			continue
		}
		return text
	}
	if image := maccmsFind(card, func(current *html.Node) bool { return maccmsTag(current, "img") }); image != nil {
		if alt := maccmsAttr(image, "alt"); alt != "" && !maccmsLooksLikeRemark(alt) {
			return alt
		}
	}
	if text := maccmsText(anchor); text != "" {
		return text
	}
	longest := ""
	for _, link := range maccmsNodes(card, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		if text := maccmsAnchorLabel(link); len([]rune(text)) > len([]rune(longest)) {
			longest = text
		}
	}
	return longest
}

// maccmsAnchorLabel 取链接的"标签文字"。
//
// 桌面模板的标题链接里套着 `<span>片名</span><span>186人在追</span>`，
// 直接取整段文本会把追剧人数拼进片名；wap 模板则是纯文本。两种形态都要认，
// 所以有子元素时只取第一个有文字的。
func maccmsAnchorLabel(anchor *html.Node) string {
	for child := anchor.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		if text := maccmsText(child); text != "" {
			return text
		}
	}
	return maccmsOwnText(anchor)
}

var maccmsRemarkText = regexp.MustCompile(`^(?:已完结|完结|全集|更新[全至到].*|全[0-9]+集|[0-9]+人在追|第[0-9]+集|HD.*)$`)

func maccmsLooksLikeRemark(text string) bool {
	return maccmsRemarkText.MatchString(strings.TrimSpace(text))
}

func maccmsCardCover(card *html.Node, pageURL string) string {
	for _, image := range maccmsNodes(card, func(current *html.Node) bool { return maccmsTag(current, "img") }) {
		for _, attribute := range []string{"data-original", "data-src", "data-lazy-src", "src"} {
			raw := maccmsAttr(image, attribute)
			if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
				continue
			}
			if address := providerCoverAddress(raw, pageURL); address != "" {
				return address
			}
		}
	}
	// 花果这类模板的缩略图 `<a>` 里**没有 <img>**，封面直接挂在链接的
	// data-original 上（用 CSS 背景图渲染），只看 img 会一张封面都取不到。
	for _, anchor := range maccmsNodes(card, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		for _, attribute := range []string{"data-original", "data-src", "data-background"} {
			raw := maccmsAttr(anchor, attribute)
			if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
				continue
			}
			if address := providerCoverAddress(raw, pageURL); address != "" {
				return address
			}
		}
	}
	return ""
}

// maccmsCardRemark 取卡片上的"更新状态"角标。
//
// 各站模板的类名不同，但都落在同一批语义后缀上；`pic-text` 在发果那类模板里
// 装的是片名本身，所以文本与标题相同时要跳过，否则会拿标题当状态。
func maccmsCardRemark(card *html.Node, title string) string {
	for _, name := range []string{"pic-text", "meta-post-type2", "module-item-note", "totalChapterNum", "lastChapter", "remarks", "note"} {
		for _, node := range maccmsNodes(card, func(current *html.Node) bool { return maccmsHasClass(current, name) }) {
			text := maccmsText(node)
			if text == "" || text == title {
				continue
			}
			return text
		}
	}
	return ""
}

func maccmsReleaseStatus(remark string) string {
	switch {
	case strings.Contains(remark, "完结"), strings.Contains(remark, "全集"), strings.Contains(remark, "全"):
		return "finished"
	case strings.Contains(remark, "更新"), strings.Contains(remark, "连载"):
		return "ongoing"
	default:
		return ""
	}
}

func maccmsCards(body string, profile maccmsProfile, base string) []Drama {
	document := maccmsDocument(body)
	if document == nil {
		return nil
	}
	var items []Drama
	seen := map[string]bool{}
	for _, anchor := range maccmsNodes(document, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		sourceID := maccmsDetailID(maccmsAttr(anchor, "href"), profile)
		if sourceID == "" || seen[sourceID] {
			continue
		}
		card := maccmsCardContainer(anchor, profile)
		remark := maccmsCardRemark(card, "")
		title := strings.TrimSpace(maccmsCardTitle(card, anchor, profile, remark))
		if title == "" {
			continue
		}
		// 发果那类模板把片名塞在 `pic-text` 里，先取到的"角标"其实就是标题；
		// 拿到标题后重算一次，把它从角标里排除。
		if remark == title {
			remark = maccmsCardRemark(card, title)
		}
		seen[sourceID] = true
		link := resolveProviderURL(base+"/", maccmsAttr(anchor, "href"))
		item := Drama{
			ID:            providerDramaID(profile.Source, sourceID),
			Source:        profile.Source,
			SourceID:      sourceID,
			Title:         title,
			Name:          title,
			Cover:         maccmsCardCover(card, firstNonEmpty(link, base+"/")),
			ChannelName:   profile.Name,
			CategoryName:  profile.Name,
			Remark:        remark,
			ReleaseStatus: maccmsReleaseStatus(remark),
		}
		item.CoverURL = item.Cover
		// 集数只在角标里能解析出来时才填，而且必须落在**合理区间**内。
		//
		// 上限 999 的理由：短剧单集 60~120 秒，整部剧再长也就几百集；
		// 实测脏数据是 `全110109集`（角标文本跨卡片串了）和 880 / 801 / 1100
		// 这类明显不是集数的数字。这类值一旦写进目录，详情页会显示
		// 「共 110109 集」，比不显示更糟 —— 用户会以为这剧有十几万集。
		if count := episodeIndex(remark, 0); count > 0 && count <= maccmsSaneEpisodeCeiling {
			item.Episodes, item.TotalEpisode, item.EpisodeCount = count, count, count
		}
		items = append(items, item)
	}
	return items
}

// ---------------------------------------------------------------- 详情 / 选集

func (d *Downloader) fetchMaccmsDetail(ctx context.Context, source, sourceID string) (Drama, []Chapter, error) {
	profile, found := maccmsProfileFor(source)
	if !found {
		return Drama{}, nil, errors.New("该站源不支持网页采集")
	}
	sourceID = strings.Trim(strings.TrimSpace(sourceID), "/")
	if !maccmsNumericID.MatchString(sourceID) {
		return Drama{}, nil, fmt.Errorf("%s 剧集 ID 无效", profile.Name)
	}
	base := strings.TrimRight(d.providerBaseURL(source), "/")
	var lastErr error
	for _, address := range maccmsDetailCandidates(profile, base, sourceID) {
		if err := ctx.Err(); err != nil {
			return Drama{}, nil, err
		}
		body, err := d.fetchProviderText(ctx, address, base+"/")
		if err != nil {
			lastErr = err
			continue
		}
		episodes := maccmsEpisodes(body, profile, address)
		if len(episodes) == 0 {
			lastErr = errors.New("页面未解析到分集")
			continue
		}
		drama := maccmsDetailDrama(body, profile, sourceID, address)
		chapters := make([]Chapter, 0, len(episodes))
		for index, episode := range episodes {
			number := episode.Index
			if number <= 0 {
				number = index + 1
			}
			title := strings.TrimSpace(episode.Title)
			if title == "" {
				title = fmt.Sprintf("第%d集", number)
			}
			chapters = append(chapters, Chapter{
				ID:     providerChapterID(source, sourceID, episode.Key),
				Source: source,
				Title:  title,
				// 播放地址要再发一次请求才拿得到（播放页里的 player_aaaa / iframe），
				// 这里只存标记，真正解析放在 resolveMaccmsPlayback 里按需做，
				// 否则拉一次详情要为每集都多打一个请求。
				VideoURL:       "maccms-play://" + episode.Key,
				PageURL:        episode.URL,
				Referer:        base + "/",
				CurrentEpisode: rawEpisode(number),
			})
		}
		sortProviderChapters(chapters)
		chapters = uniqueChapters(chapters)
		// 集数以**实际可播的分集**为准，不用详情页上的"更新至 N 集"：
		// 0.8.11 修黄果时踩过的坑就是"外面 42 集、里面 2 集"，这里宁可少也不能虚报。
		count := len(chapters)
		drama.Episodes, drama.TotalEpisode, drama.EpisodeCount = count, count, count
		return drama, chapters, nil
	}
	if lastErr == nil {
		lastErr = errors.New("未找到该剧的详情页")
	}
	return Drama{}, nil, fmt.Errorf("%s 未取到分集：%w", profile.Name, lastErr)
}

func maccmsDetailCandidates(profile maccmsProfile, base, sourceID string) []string {
	candidates := []string{profile.DetailURL(base, sourceID)}
	// 模板改版常换详情路由，补几个苹果CMS 常见形态做兜底。
	for _, path := range []string{
		"/index.php/vod/detail/id/" + sourceID + ".html",
		"/detail/" + sourceID + ".html",
		"/vod/" + sourceID + ".html",
	} {
		candidate := base + path
		duplicated := false
		for _, existing := range candidates {
			if existing == candidate {
				duplicated = true
				break
			}
		}
		if !duplicated {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func maccmsDetailDrama(body string, profile maccmsProfile, sourceID, detailURL string) Drama {
	document := maccmsDocument(body)
	title := maccmsDetailTitle(document, body)
	if title == "" {
		title = profile.Name + " " + sourceID
	}
	description := truncate(maccmsDetailIntro(document, body), 2000)
	cover := maccmsDetailCover(document, detailURL)
	drama := Drama{
		ID:           providerDramaID(profile.Source, sourceID),
		Source:       profile.Source,
		SourceID:     sourceID,
		Title:        title,
		Name:         title,
		Desc:         description,
		Intro:        description,
		Cover:        cover,
		CoverURL:     cover,
		ChannelName:  profile.Name,
		CategoryName: profile.Name,
	}
	// 连载状态只按页面自己写的来，不猜：苹果CMS 模板里"已完结 / 更新至"都是
	// 正文里明确标出来的，猜错会让卡片上的角标和实际集数互相打脸。
	switch {
	case strings.Contains(body, "已完结"), strings.Contains(body, "完结"):
		drama.ReleaseStatus = "finished"
	case strings.Contains(body, "更新至"), strings.Contains(body, "连载"):
		drama.ReleaseStatus = "ongoing"
	}
	return drama
}

func maccmsDetailTitle(document *html.Node, body string) string {
	for _, name := range []string{"h1", "h2", "h3"} {
		node := maccmsFind(document, func(current *html.Node) bool {
			return maccmsTag(current, name) && maccmsText(current) != ""
		})
		if node == nil {
			continue
		}
		// 花果 / 发果的 h1 后面直接粘着评分（`不做替身后被长公主截胡赐婚8.0`），
		// 只在小数点评分这种形态上剥，避免误伤 `第2季` 这类正常结尾。
		if title := maccmsCleanTitle(maccmsTitleScore.ReplaceAllString(maccmsText(node), "")); title != "" {
			return title
		}
	}
	return maccmsCleanTitle(extractPageTitle(body))
}

// maccmsCleanTitle 把 SEO 标题清成片名。
//
// 四个站的 <title> 长这样：
//
//	短剧《不做替身后被长公主截胡赐婚》全集免费高清在线观看_短剧主演(内详)_花生短剧
//	妈咪受辱，大小姐回国掀翻全场_高清完整版在线观看_爽文短剧 - 短剧网
//	《狂热先生》高清免费观看-泰剧-168影院
//	绝世天医-短剧绝世天医全集-绝世天医免费在线观看 - 免费五五短剧视频分享大全
//
// 共同点是片名一定在 `《》` 里，或者在最前面；后面挂的全是 SEO 尾巴。
// 判"要不要切"靠尾巴里有没有 SEO 词，而不是按分隔符数量硬切 ——
// 否则 `总裁的替身新娘-第二季` 这种正常片名会被砍掉后半截。
func maccmsCleanTitle(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if match := maccmsTitleBracket.FindStringSubmatch(text); len(match) > 1 {
		if title := strings.TrimSpace(match[1]); title != "" {
			return title
		}
	}
	for _, separator := range []string{"_", " - ", "|", "-"} {
		for {
			head, tail, found := strings.Cut(text, separator)
			if !found {
				break
			}
			head = strings.TrimSpace(head)
			if head == "" || !maccmsTitleTailIsSEO(tail) {
				break
			}
			text = head
		}
	}
	for changed := true; changed; {
		changed = false
		for _, suffix := range maccmsTitleSuffixes {
			if strings.HasSuffix(text, suffix) && len([]rune(text)) > len([]rune(suffix)) {
				text = strings.TrimSpace(strings.TrimSuffix(text, suffix))
				changed = true
			}
		}
	}
	text = strings.Trim(text, "-_|·— ")
	if text == "" {
		return strings.TrimSpace(raw)
	}
	return text
}

// maccmsTitleTailIsSEO 判断标题后半截是不是站点拼的 SEO 尾巴。
func maccmsTitleTailIsSEO(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return true
	}
	for _, marker := range []string{"短剧", "全集", "在线观看", "免费", "高清", "完整版", "视频", "剧场", "影院", "网", "动漫"} {
		if strings.Contains(tail, marker) {
			return true
		}
	}
	return false
}

func maccmsDetailIntro(document *html.Node, body string) string {
	for _, name := range []string{"DramaDetail_intro", "module-info-introduction-content", "detail-content", "video-info-content", "desc", "introduction"} {
		node := maccmsFind(document, func(current *html.Node) bool { return maccmsHasClass(current, name) })
		if node == nil {
			continue
		}
		if text := maccmsText(node); len(text) >= 12 {
			return text
		}
	}
	if description := maccmsMeta(document, "description", "og:description"); description != "" {
		return description
	}
	return extractDescription(body)
}

func maccmsDetailCover(document *html.Node, detailURL string) string {
	if address := maccmsMeta(document, "og:image", "twitter:image"); address != "" {
		if cover := providerCoverAddress(address, detailURL); cover != "" {
			return cover
		}
	}
	// 没有 og:image 的模板（网果 / 伍果）第一张图往往是站标，按"像不像海报"筛：
	// 先认 URL 里的 /upload/vod/、/cover/ 这类路径，再退到尺寸判断。
	var sized string
	for _, image := range maccmsNodes(document, func(current *html.Node) bool { return maccmsTag(current, "img") }) {
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			raw := maccmsAttr(image, attribute)
			if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
				continue
			}
			cover := providerCoverAddress(raw, detailURL)
			if cover == "" {
				continue
			}
			lower := strings.ToLower(cover)
			for _, marker := range []string{"/upload/vod/", "/cover/", "/image/cover/", "/pic/"} {
				if strings.Contains(lower, marker) {
					return cover
				}
			}
			if sized == "" {
				width, _ := strconv.Atoi(maccmsAttr(image, "width"))
				height, _ := strconv.Atoi(maccmsAttr(image, "height"))
				if width >= 150 && height >= 200 {
					sized = cover
				}
			}
		}
	}
	return sized
}

// maccmsEpisodes 解析详情页的选集链接。
//
// 苹果CMS 的多线路模板会把同一集在 n1 / n2 / n3 三条线路上各列一遍
// （发果实测 9 集 × 3 条线 = 27 个播放链接），只按链接去重会得到 3 倍分集，
// 所以集号能解析出来时按**集号**去重，保留第一条线路。
//
// 集号来源有两个，优先级不同（0.8.24 修正）：
//
//  1. **播放链接里的 nid**（`/play/<id>-<sid>-<nid>.html`、
//     `/index.php/vod/play/id/<id>/sid/<sid>/nid/<nid>.html`）—— 权威。
//  2. 锚文本 / title 里的「第N集」—— 兜底。
//
// 为什么必须先看链接：网果的详情页在选集区**前面**还有一个「立即播放」按钮，
// 它指向 `/play/<id>-1-1.html`（sid=1 是"全集"线路），锚文本是「立即播放」，
// 里面没有任何集号。按旧逻辑它会落到 `index = len(episodes)+1 = 1`，紧接着
// 真正的「第01集」也解析出 1 —— 两集拿到同一个 Key="1"，`providerChapterID`
// 于是给出同一个章节 ID。客户端按 ID 去重，第 1 集被"全集"顶掉；更糟的是
// 章节 ID 重复会让选集列表错位。改用 nid 之后「立即播放」在自己那套 sid 里
// 是 nid=1，与第 1 集（sid=2, nid=1）仍然同号，所以还要按 sid 分组处理。
func maccmsEpisodes(body string, profile maccmsProfile, pageURL string) []providerEpisode {
	document := maccmsDocument(body)
	if document == nil {
		return nil
	}
	// 先收两拨：有确定集号的，和"全集 / 立即播放"这类没有集号的。
	//
	// 分成两拨是因为要**在有真实分集时丢掉无集号的那条**。旧逻辑把它们混在
	// 一起，无集号的走 `index = len+1` 兜底，恰好落在 1 号位，与紧随其后的
	// 「第01集」撞成同一个 Key —— 章节 ID 重复，第 1 集被"全集"顶掉。
	type pending struct {
		title string
		link  string
		index int
	}
	var numbered []pending
	var unnamed []pending
	seenLinks := map[string]bool{}
	seenNumbers := map[int]bool{}
	for _, anchor := range maccmsNodes(document, func(current *html.Node) bool { return maccmsTag(current, "a") }) {
		href := maccmsAttr(anchor, "href")
		path := maccmsPath(href)
		if path == "" || profile.PlayPath == nil || !profile.PlayPath.MatchString(path) {
			continue
		}
		link := resolveProviderURL(pageURL, href)
		if link == "" || seenLinks[link] {
			continue
		}
		title := strings.TrimSpace(firstNonEmpty(maccmsAttr(anchor, "title"), maccmsText(anchor)))
		if title == "" {
			continue
		}
		if strings.Contains(title, "APP") || strings.Contains(title, "下载") || strings.Contains(title, "客户端") {
			continue
		}
		seenLinks[link] = true
		number := maccmsEpisodeNumber(path, title)
		if number <= 0 {
			unnamed = append(unnamed, pending{title: title, link: link})
			continue
		}
		if seenNumbers[number] {
			continue
		}
		seenNumbers[number] = true
		numbered = append(numbered, pending{title: title, link: link, index: number})
	}
	// 有真实分集时，没有集号的那条一定是「全集 / 立即播放」这类整片入口 ——
	// 留着它既占号又和第一集撞 ID，直接丢掉。
	// 只有全站确实拿不到任何集号（伍果这种"整部一个视频"的站源）时才保留，
	// 否则会变成"这部剧一集都没有"。
	if len(numbered) > 0 {
		unnamed = nil
	}
	episodes := make([]providerEpisode, 0, len(numbered)+len(unnamed))
	for _, item := range numbered {
		episodes = append(episodes, providerEpisode{
			Key:   strconv.Itoa(item.index),
			Title: item.title,
			URL:   item.link,
			Index: item.index,
		})
	}
	for _, item := range unnamed {
		episodes = append(episodes, providerEpisode{
			Key:   strconv.Itoa(len(episodes) + 1),
			Title: item.title,
			URL:   item.link,
			Index: len(episodes) + 1,
		})
	}
	return episodes
}

// maccmsPlayRoute 拆出播放链接里的 sid / nid。
//
// 兼容两种模板：
//
//	网果 / 花果  /play/<id>-<sid>-<nid>.html、/zywplay/<id>-<sid>-<nid>.html
//	伍果 / 发果  /index.php/vod/play/id/<id>/sid/<sid>/nid/<nid>.html
//
// 拆不出来就返回 0，调用方退回用锚文本解析。
func maccmsPlayRoute(path string) (sid, nid int) {
	if m := maccmsPlayRouteDash.FindStringSubmatch(path); len(m) > 2 {
		sid, _ = strconv.Atoi(m[1])
		nid, _ = strconv.Atoi(m[2])
		return sid, nid
	}
	if m := maccmsPlayRouteNamed.FindStringSubmatch(path); len(m) > 2 {
		sid, _ = strconv.Atoi(m[1])
		nid, _ = strconv.Atoi(m[2])
		return sid, nid
	}
	return 0, 0
}

// maccmsEpisodeNumber 决定这一条播放链接算第几集。
//
// 顺序：先看链接里的 nid（权威），再用锚文本兜底。**链接里 sid=1 的单集
// 线路要特殊对待** —— 那是「全集 / 立即播放」，nid 恒为 1，如果直接当第 1 集
// 会把真正的第 1 集挤掉。判定方式：这一条是 sid=1 且锚文本里没有集号
// （「立即播放」「全集」这类），就交给调用方按"无集号"处理。
func maccmsEpisodeNumber(path, title string) int {
	sid, nid := maccmsPlayRoute(path)
	if nid > 0 {
		if sid == 1 && episodeIndex(title, 0) <= 0 {
			// 「全集 / 立即播放」：不是分集，别占号。
			return 0
		}
		return nid
	}
	return episodeIndex(title, 0)
}

// ---------------------------------------------------------------- 播放地址

// maccmsPlayerMedia 从播放页里捞媒体地址。
//
// 双路径（学的是苹果CMS 生态里的通用做法）：
//  1. `player_aaaa` 的 JSON `"url"` 字段 —— 网果 / 发果 / 伍果 都是这条；
//  2. 整页裸的 m3u8/mp4 地址 —— 花果的模板没有 player_aaaa，地址是 JS 拼在
//     iframe src 里的（`'/player/mui-player.php?'+ '标题' +',https://…/index.m3u8,'`）。
func maccmsPlayerMedia(body, pageURL string) string {
	if matches := maccmsPlayerJSON.FindStringSubmatch(body); len(matches) > 1 {
		if inner := maccmsPlayerField.FindStringSubmatch(matches[1]); len(inner) > 1 {
			if media := normalizeProviderMediaURL(pageURL, inner[1]); media != "" {
				return media
			}
		}
	}
	for _, tag := range maccmsIframeTag.FindAllString(body, -1) {
		for _, raw := range maccmsMediaURL.FindAllString(tag, -1) {
			if media := normalizeProviderMediaURL(pageURL, raw); media != "" {
				return media
			}
		}
	}
	for _, raw := range maccmsMediaURL.FindAllString(body, -1) {
		if media := normalizeProviderMediaURL(pageURL, raw); media != "" {
			return media
		}
	}
	return ""
}

// maccmsPlayerFrame 找"播放器外壳页"（iframe），用于再抓一层。
// 花果那类 JS 拼接的 src 已经在 maccmsPlayerMedia 里处理过，这里跳过。
func maccmsPlayerFrame(body, pageURL string) string {
	tag := maccmsIframeTag.FindString(body)
	if tag == "" {
		return ""
	}
	// 花果这类模板的 src 是 JS 拼接出来的（`'/player/x.php?'+ '标题' +',https://…m3u8,'`），
	// 属性值会被第一个单引号截断成半截地址，抓下去只会 404。
	if strings.Contains(tag, "'+") || strings.Contains(tag, `"+`) || strings.Contains(tag, "+'") {
		return ""
	}
	src := maccmsRawAttr(tag, "src", "data-src")
	if src == "" || strings.ContainsAny(src, "'\"") || strings.ContainsAny(src, " \t") {
		return ""
	}
	frame := resolveProviderURL(pageURL, src)
	if !isProviderHTTPMediaURL(frame) {
		return ""
	}
	return frame
}

func (d *Downloader) resolveMaccmsPlayback(ctx context.Context, task Task) (providerMedia, error) {
	source, _, valid := splitProviderDramaID(task.DramaID)
	profile, found := maccmsProfileFor(source)
	if !valid || !found {
		return providerMedia{}, errors.New("站源信息无效，请刷新详情")
	}
	base := strings.TrimRight(d.providerBaseURL(source), "/")
	pageURL := resolveProviderURL(base+"/", strings.TrimSpace(task.Chapter.PageURL))
	if !isProviderHTTPMediaURL(pageURL) {
		return providerMedia{}, errors.New("缺少播放页地址，请刷新详情")
	}
	visited := map[string]bool{}
	referer := base + "/"
	for depth := 0; depth < 3; depth++ {
		if visited[pageURL] {
			return providerMedia{}, errors.New("播放线路出现循环跳转")
		}
		visited[pageURL] = true
		body, err := d.fetchProviderText(ctx, pageURL, referer)
		if err != nil {
			return providerMedia{}, fmt.Errorf("获取%s播放页失败：%w", profile.Name, err)
		}
		if media := maccmsPlayerMedia(body, pageURL); media != "" {
			return providerMedia{URL: media, Referer: pageURL}, nil
		}
		frame := maccmsPlayerFrame(body, pageURL)
		if frame == "" {
			break
		}
		referer, pageURL = pageURL, frame
	}
	return providerMedia{}, fmt.Errorf("%s 未解析到可播放地址，可能线路失效", profile.Name)
}

// ---------------------------------------------------------------- 接入点辅助

func (d *Downloader) fetchMaccmsChapters(ctx context.Context, source, sourceID string) (string, []Chapter, error) {
	drama, chapters, err := d.fetchMaccmsDetail(ctx, source, sourceID)
	if err != nil {
		return "", nil, err
	}
	return drama.DisplayTitle(), chapters, nil
}
