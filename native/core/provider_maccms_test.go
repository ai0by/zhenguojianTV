package core

import (
	"strconv"
	"strings"
	"testing"
)

// 苹果CMS 采集适配器的离线单测（0.8.11）。
//
// fixture 全部照抄四个实测站的真实结构，只删掉无关区块。重点覆盖三件容易
// 回归的事：
//  1. 模板给 class 加随机前缀时，卡片还能认出来（花果 `_9dbe…-vodlist__item`）；
//  2. 多线路模板同一集列三遍时，分集不能变成三倍（发果 n1/n2/n3）；
//  3. 播放地址双路径（player_aaaa / 裸 m3u8）都要能出地址。

const maccmsHuaguoListHTML = `<div class="_9dbe81752a1dbf17-pannel_bd"><ul class="_9dbe81752a1dbf17-vodlist clearfix">` +
	`<li class="_9dbe81752a1dbf17-vodlist__item">` +
	`<a class="_9dbe81752a1dbf17-vodlist__thumb lazyload" href="/zywview/91188.html" title="染川舟之假千金是真团宠-动漫合集" data-original="https://yqk.j3kjn242sq.com/image/cover/2026/10/25/4aa898ed85d242e2a380d16fcd3d0717.jpeg">` +
	`<span class="play hidden-xs"></span><span class="pic-text text-right">已完结</span></a>` +
	`<h4 class="_9dbe81752a1dbf17-vodlist__title"><a href="/zywview/91188.html" title="染川舟之假千金是真团宠-动漫合集">染川舟之假千金是真团宠-动漫合集</a></h4>` +
	`</li>` +
	`<li class="_9dbe81752a1dbf17-vodlist__item">` +
	`<a class="_9dbe81752a1dbf17-vodlist__thumb lazyload" href="/zywview/29.html" title="开局认亲后，保洁老妈曝光了身家" data-original="https://img.jlsdssfa.com/image/cover/2024/07/06/bf7cfd6107dd430cb18f82fdd6176470.jpg">` +
	`<span class="pic-text text-right">全100集</span></a>` +
	`<h4 class="_9dbe81752a1dbf17-vodlist__title"><a href="/zywview/29.html">开局认亲后，保洁老妈曝光了身家</a></h4>` +
	`</li>` +
	`</ul></div>`

// 伍果的 wap 模板（手机 UA 拿到的就是这一份）：每张卡片的 <img alt> 都是同一个
// 占位名"疯狗传"，真片名只在 BrowseList_bookName 的文字里。
const maccmsWuguoWapListHTML = `<div class="browse_browseContent"><div class="BrowseList_listBox">` +
	`<div class="BrowseList_listItem">` +
	`<a class="image_imageBox BrowseList_itemImg" href="/index.php/vod/detail/id/57762.html">` +
	`<img alt="疯狗传" loading="lazy" width="210" height="300" class="image_imageItem" src="https://img.lzipic.com/upload/vod/20261026-1/498fd67d880cda44d9033d00fb71a9bb.jpg">` +
	`</a>` +
	`<a class="BrowseList_lastChapter" href="/index.php/vod/detail/id/57762.html">已完结</a>` +
	`<div class="BrowseList_itemContent">` +
	`<a class="BrowseList_bookName" href="/index.php/vod/detail/id/57762.html">逃婚三年，江少的花心人设崩了</a>` +
	`<a class="BrowseList_bookViewCount" href="/index.php/vod/detail/id/57762.html">186人在追</a>` +
	`<div class="BrowseList_tagsBox"><a href="/index.php/vod/search/class/%E7%9F%AD%E5%89%A7.html">短剧</a></div>` +
	`</div></div>` +
	`<div class="BrowseList_listItem">` +
	`<a class="image_imageBox BrowseList_itemImg" href="/index.php/vod/detail/id/57489.html">` +
	`<img alt="疯狗传" loading="lazy" width="210" height="300" class="image_imageItem" src="https://tyyswimg2.com/upload/vod/20261023-1/d8c9a4a5a141fd530c1b1b59af52ddad.jpg">` +
	`</a>` +
	`<a class="BrowseList_lastChapter" href="/index.php/vod/detail/id/57489.html">更新全集</a>` +
	`<div class="BrowseList_itemContent">` +
	`<a class="BrowseList_bookName" href="/index.php/vod/detail/id/57489.html">暗恋对象比我先动心了</a>` +
	`<a class="BrowseList_bookViewCount" href="/index.php/vod/detail/id/57489.html">546人在追</a>` +
	`</div></div>` +
	`</div></div>`

const maccmsWangguoListHTML = `<div class="row posts-wrapper scroll">` +
	`<div class="col-lg-2 col-md-4 col-6">` +
	`<article id="post-876" class="post post-grid post-876 hentry">` +
	`<div class="entry-media"><div class="placeholder">` +
	`<a href="/vod/49933.html" title="妈咪受辱，大小姐回国掀翻全场">` +
	`<img class="lazyload" data-src="https://pic.youkupic.com/upload/vod/20261024-1/944df57113684a89e3a2270f1ca9717f.jpg" src="data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==" alt="妈咪受辱，大小姐回国掀翻全场" />` +
	`<span class="meta-post-type1">2026</span><span class="meta-post-type2">全集</span>` +
	`</a></div></div>` +
	`<div class="entry-wrapper"><header class="entry-header">` +
	`<a href="/vod/49933.html" title="妈咪受辱，大小姐回国掀翻全场" rel="bookmark">妈咪受辱，大小姐回国掀翻全场</a>` +
	`</header><div class="entry-footer"><div class="entry-meta">` +
	`<span class="meta-views"><a href="/show/26---短剧--------.html" title="短剧">短剧</a></span>` +
	`</div></div></div></article></div></div>`

const maccmsWuguoListHTML = `<div class="BrowseList_listBox"><div class="BrowseList_itemBox">` +
	`<a class="image_imageScaleBox BrowseList_imageBox" href="/index.php/vod/detail/id/57762.html">` +
	`<img alt="逃婚三年，江少的花心人设崩了" loading="lazy" width="184" height="264" class="image_imageItem" src="https://img.lzipic.com/upload/vod/20261026-1/498fd67d880cda44d9033d00fb71a9bb.jpg">` +
	`</a>` +
	`<a class="BrowseList_totalChapterNum" href="/index.php/vod/detail/id/57762.html">已完结</a>` +
	`<div class="BrowseList_itemRight">` +
	`<a class="BrowseList_bookName" href="/index.php/vod/detail/id/57762.html"><span>逃婚三年，江少的花心人设崩了</span>` +
	`<span class="BrowseList_bookRate"><img class="BrowseList_rateIcon" src="/template/fire.png">186人在追</span></a>` +
	`</div></div></div>`

// 发果：三线路 × 9 集，且模板在结构里插了噪声标签。
var maccmsFaguoDetailHTML = `<html><head><title>《狂热先生》高清免费观看-泰剧-168影院</title>` +
	`<meta property="og:image" content="https://hongniuzyimage.com/cover/f4a0e4f02d102bd049806f27690048ac.jpg"></head><body>` +
	`<h1 class="title">狂热先生2.0</h1>` +
	`<div class="stui-vodlist__box">` +
	maccmsFaguoPlaylist(1) + maccmsFaguoPlaylist(2) + maccmsFaguoPlaylist(3) +
	`</div></body></html>`

func maccmsFaguoPlaylist(line int) string {
	var builder strings.Builder
	builder.WriteString(`<div id="playlist` + strconv.Itoa(line) + `" class="tab-pane fade in clearfix"><ul class="stui-content__playlist clearfix">`)
	for index := 1; index <= 9; index++ {
		builder.WriteString(`<var></var><li><a title="狂热先生第` + strconv.Itoa(index) + `集" href="/xzyxvp/429996n` + strconv.Itoa(line) + `m` + strconv.Itoa(index) + `.html">第` + strconv.Itoa(index) + `集</a></li>`)
	}
	builder.WriteString(`</ul></div>`)
	return builder.String()
}

// 花果：选集区只有"全集"一条，另外挂一个 APP 推广链接（必须被过滤掉）。
const maccmsHuaguoDetailHTML = `<html><head><title>短剧《不做替身后被长公主截胡赐婚》全集免费高清在线观看_短剧主演(内详)_花生短剧</title></head><body>` +
	`<h1>不做替身后被长公主截胡赐婚8.0</h1>` +
	`<ul class="_9dbe81752a1dbf17-content__playlist playlink clearfix">` +
	`<li id="1"><a class="ds-text-green" title="APP播放" href="/heji.html">APP播放</a></li>` +
	`<li><a title="全集" href="/zywplay/20523-0-0.html" target="_self">全集</a></li>` +
	`</ul></body></html>`

// 伍果：整部剧一条"第01集"，播放页用 player_aaaa 下发 m3u8。
const maccmsWuguoDetailHTML = `<html><head><title>绝世天医-短剧绝世天医全集-绝世天医免费在线观看</title>` +
	`<meta name="description" content="热播短剧绝世天医讲述了:王浩本是一个普通外卖员，妻子嫌他没钱跟他提离婚。"></head><body>` +
	`<img class="PcHeader_logoTxt" src="https://www.duanju55.com/upload/site/20260918-1/logo.png" alt="站标">` +
	`<h1 class="DramaDetail_bookName">绝世天医</h1>` +
	`<img alt="绝世天医" width="184" height="264" src="https://img.lzipic.com/upload/vod/20260630-1/poster.jpg">` +
	`<div class="pcDrama_catalog">` +
	`<a title='第01集' class="pcDrama_catalogItem" href="/index.php/vod/play/id/9500/sid/1/nid/1.html" target="_self">第01集</a>` +
	`</div></body></html>`

const maccmsWuguoPlayHTML = `<html><body><script type="text/javascript">` +
	`var player_aaaa={"flag":"play","encrypt":0,"trysee":0,"points":0,"link":"\/index.php\/vod\/play\/id\/9500\/sid\/1\/nid\/1.html",` +
	`"url":"https:\/\/tyyszywvod2.com\/videos\/202606\/30\/6a3de64ab4f36433b91d78d9\/66323e\/index.m3u8","from":"tym3u8","id":"9500","sid":1,"nid":1}` +
	`</script></body></html>`

const maccmsHuaguoPlayHTML = `<html><body>` +
	`<script>document.write('<iframe src="/player/mui-player.php?'+ '不做替身后被长公主截胡赐婚 全集' +',https://v11.ppqrrs.com/wjv11/202508/15/SnEXGkSedD83/video/index.m3u8,'+ nextPage + '"></iframe>')</script>` +
	`</body></html>`

func TestMaccmsCardsParsesPrefixedClasses(t *testing.T) {
	profile, found := maccmsProfileFor(sourceHuaguo)
	if !found {
		t.Fatal("花果 profile 缺失")
	}
	items := maccmsCards(maccmsHuaguoListHTML, profile, huaguoBaseURL)
	if len(items) != 2 {
		t.Fatalf("应解析出 2 部剧，实际 %d：%+v", len(items), items)
	}
	first := items[0]
	if first.ID != "huaguo:91188" || first.Title != "染川舟之假千金是真团宠-动漫合集" {
		t.Fatalf("首条解析错误：%+v", first)
	}
	if !strings.Contains(nativeText(first.Cover), "/image/cover/2026/10/25/4aa898ed85d242e2a380d16fcd3d0717.jpeg") {
		t.Fatalf("封面解析错误：%v", first.Cover)
	}
	if first.Remark != "已完结" {
		t.Fatalf("角标解析错误：%q", first.Remark)
	}
	if first.ReleaseStatus != "finished" {
		t.Fatalf("完结状态解析错误：%q", first.ReleaseStatus)
	}
	// "全100集" 要能解出集数，否则列表卡片会一直显示"集数待补齐"。
	if nativeEpisodeCount(items[1].TotalEpisode) != 100 {
		t.Fatalf("集数解析错误：%v", items[1].TotalEpisode)
	}
}

func TestMaccmsCardsReadsArticleAndBookNameTemplates(t *testing.T) {
	wangguo, _ := maccmsProfileFor(sourceWangguo)
	items := maccmsCards(maccmsWangguoListHTML, wangguo, wangguoBaseURL)
	if len(items) != 1 {
		t.Fatalf("网果应解析出 1 部剧，实际 %d", len(items))
	}
	if items[0].Title != "妈咪受辱，大小姐回国掀翻全场" || items[0].Remark != "全集" {
		t.Fatalf("网果卡片解析错误：%+v", items[0])
	}

	wuguo, _ := maccmsProfileFor(sourceWuguo)
	items = maccmsCards(maccmsWuguoListHTML, wuguo, wuguoBaseURL)
	if len(items) != 1 {
		t.Fatalf("伍果应解析出 1 部剧，实际 %d", len(items))
	}
	// 伍果的详情链接没有 title 属性，标题只能从 img alt 拿。
	if items[0].Title != "逃婚三年，江少的花心人设崩了" {
		t.Fatalf("伍果标题解析错误：%q", items[0].Title)
	}
	if items[0].Remark != "已完结" {
		t.Fatalf("伍果角标解析错误：%q", items[0].Remark)
	}
}

// 伍果的 wap 模板会把所有卡片的 <img alt> 写成同一个占位名，
// 0.8.11 联网冒烟时 24 张卡片全叫"疯狗传"，就是栽在这里。
func TestMaccmsCardsIgnoresPlaceholderAlt(t *testing.T) {
	profile, _ := maccmsProfileFor(sourceWuguo)
	items := maccmsCards(maccmsWuguoWapListHTML, profile, wuguoBaseURL)
	if len(items) != 2 {
		t.Fatalf("应解析出 2 部剧，实际 %d", len(items))
	}
	if items[0].Title != "逃婚三年，江少的花心人设崩了" {
		t.Fatalf("首条标题取到了占位 alt：%q", items[0].Title)
	}
	if items[1].Title != "暗恋对象比我先动心了" {
		t.Fatalf("第二条标题错误：%q", items[1].Title)
	}
	if items[0].Remark != "已完结" || items[1].Remark != "更新全集" {
		t.Fatalf("角标解析错误：%q / %q", items[0].Remark, items[1].Remark)
	}
}

func TestMaccmsEpisodesDedupesMultiLineTemplates(t *testing.T) {
	profile, _ := maccmsProfileFor(sourceFaguo)
	episodes := maccmsEpisodes(maccmsFaguoDetailHTML, profile, "https://www.xzyx168.com/xzyxvd/429996.html")
	if len(episodes) != 9 {
		t.Fatalf("三线路各 9 集应去重成 9 集，实际 %d", len(episodes))
	}
	// 保留第一条线路（n1），而不是随机取。
	if !strings.Contains(episodes[0].URL, "429996n1m1.html") {
		t.Fatalf("应保留第一条线路，实际 %q", episodes[0].URL)
	}
	if episodes[8].Index != 9 || episodes[8].Key != "9" {
		t.Fatalf("集号解析错误：%+v", episodes[8])
	}
}

func TestMaccmsEpisodesKeepsSingleFullSet(t *testing.T) {
	profile, _ := maccmsProfileFor(sourceHuaguo)
	episodes := maccmsEpisodes(maccmsHuaguoDetailHTML, profile, "https://www.zywest263.com/zywview/20523.html")
	if len(episodes) != 1 {
		t.Fatalf("花果整部剧只有一个视频，应解析出 1 条，实际 %d：%+v", len(episodes), episodes)
	}
	if episodes[0].Title != "全集" || !strings.HasSuffix(episodes[0].URL, "/zywplay/20523-0-0.html") {
		t.Fatalf("全集解析错误：%+v", episodes[0])
	}
	// /heji.html 那个"APP播放"推广链接不能被当成一集。
	for _, episode := range episodes {
		if strings.Contains(episode.URL, "heji") {
			t.Fatalf("APP 推广链接混进了分集：%+v", episode)
		}
	}
}

func TestMaccmsPlayerMediaReadsPlayerJSON(t *testing.T) {
	media := maccmsPlayerMedia(maccmsWuguoPlayHTML, "https://www.duanju55.com/index.php/vod/play/id/9500/sid/1/nid/1.html")
	if media != "https://tyyszywvod2.com/videos/202606/30/6a3de64ab4f36433b91d78d9/66323e/index.m3u8" {
		t.Fatalf("player_aaaa 解析错误：%q", media)
	}
}

func TestMaccmsPlayerMediaFallsBackToBareURL(t *testing.T) {
	// 花果的播放页没有 player_aaaa，地址是 JS 拼在 iframe src 里的，
	// 而且后面还粘着一个逗号，必须只取到 .m3u8 为止。
	media := maccmsPlayerMedia(maccmsHuaguoPlayHTML, "https://www.zywest263.com/zywplay/20523-0-0.html")
	if media != "https://v11.ppqrrs.com/wjv11/202508/15/SnEXGkSedD83/video/index.m3u8" {
		t.Fatalf("裸 m3u8 兜底解析错误：%q", media)
	}
}

func TestMaccmsPlayerFrameSkipsScriptConcatenation(t *testing.T) {
	// 花果那种 `'…'+ '…' +'` 的拼接 src 不是可访问地址，不能拿去再抓一层。
	if frame := maccmsPlayerFrame(maccmsHuaguoPlayHTML, "https://www.zywest263.com/zywplay/20523-0-0.html"); frame != "" {
		t.Fatalf("JS 拼接的 iframe src 不应被当成播放器外壳页：%q", frame)
	}
	body := `<html><body><iframe src="/player/index.php?url=abc"></iframe></body></html>`
	frame := maccmsPlayerFrame(body, "https://www.duanju2.com/play/1-1-1.html")
	if frame != "https://www.duanju2.com/player/index.php?url=abc" {
		t.Fatalf("播放器外壳页解析错误：%q", frame)
	}
}

func TestMaccmsCleanTitleStripsSeoSuffix(t *testing.T) {
	cases := []struct{ raw, expected string }{
		{"短剧《不做替身后被长公主截胡赐婚》全集免费高清在线观看_短剧主演(内详)_花生短剧", "不做替身后被长公主截胡赐婚"},
		{"妈咪受辱，大小姐回国掀翻全场_高清完整版在线观看_爽文短剧 - 短剧网", "妈咪受辱，大小姐回国掀翻全场"},
		{"《狂热先生》高清免费观看-泰剧-168影院", "狂热先生"},
		{"绝世天医-短剧绝世天医全集-绝世天医免费在线观看 - 免费五五短剧视频分享大全", "绝世天医"},
		{"逃婚三年，江少的花心人设崩了", "逃婚三年，江少的花心人设崩了"},
		// 正常片名里带"第 N 季"不能被当成 SEO 尾巴砍掉。
		{"总裁的替身新娘-第二季", "总裁的替身新娘-第二季"},
		{"第2季", "第2季"},
	}
	for _, item := range cases {
		if actual := maccmsCleanTitle(item.raw); actual != item.expected {
			t.Fatalf("标题清理错误：%q → %q，期望 %q", item.raw, actual, item.expected)
		}
	}
}

func TestMaccmsDetailTitlePrefersHeadingAndStripsScore(t *testing.T) {
	profile, _ := maccmsProfileFor(sourceFaguo)
	drama := maccmsDetailDrama(maccmsFaguoDetailHTML, profile, "429996", "https://www.xzyx168.com/xzyxvd/429996.html")
	if drama.Title != "狂热先生" {
		t.Fatalf("详情标题解析错误：%q", drama.Title)
	}
	if nativeText(drama.Cover) != "https://hongniuzyimage.com/cover/f4a0e4f02d102bd049806f27690048ac.jpg" {
		t.Fatalf("详情封面应优先用 og:image，实际 %v", drama.Cover)
	}
}

func TestMaccmsDetailCoverSkipsSiteLogo(t *testing.T) {
	profile, _ := maccmsProfileFor(sourceWuguo)
	drama := maccmsDetailDrama(maccmsWuguoDetailHTML, profile, "9500", "https://www.duanju55.com/index.php/vod/detail/id/9500.html")
	// 第一张图是站标（/upload/site/…），真海报是 /upload/vod/…，不能拿错。
	if nativeText(drama.Cover) != "https://img.lzipic.com/upload/vod/20260630-1/poster.jpg" {
		t.Fatalf("详情封面取到了站标：%v", drama.Cover)
	}
	if !strings.Contains(drama.Intro, "王浩本是一个普通外卖员") {
		t.Fatalf("详情简介解析错误：%q", drama.Intro)
	}
}

func TestMaccmsProfileURLShapes(t *testing.T) {
	huaguo, _ := maccmsProfileFor(sourceHuaguo)
	if got := huaguo.ListURL(huaguoBaseURL, 1); got != "https://www.zywest263.com/zywtype/27.html" {
		t.Fatalf("花果第 1 页地址错误：%q", got)
	}
	if got := huaguo.ListURL(huaguoBaseURL, 2); got != "https://www.zywest263.com/zywtype/27-2.html" {
		t.Fatalf("花果第 2 页地址错误：%q", got)
	}
	// 花果的搜索页不认 page，第 2 页必须返回空串让上层收手，而不是重复第 1 页。
	if got := huaguo.SearchURL(huaguoBaseURL, "总裁", 2); got != "" {
		t.Fatalf("花果搜索第 2 页应为空，实际 %q", got)
	}

	faguo, _ := maccmsProfileFor(sourceFaguo)
	if got := faguo.SearchURL(faguoBaseURL, "总裁", 2); got != "https://www.xzyx168.com/xzyxvc/%E6%80%BB%E8%A3%81-wdyswzqun2num.html" {
		t.Fatalf("发果搜索地址错误：%q", got)
	}
	if got := faguo.ListURL(faguoBaseURL, 2); got != "" {
		t.Fatalf("发果短剧分类只有一页，第 2 页应为空，实际 %q", got)
	}

	wuguo, _ := maccmsProfileFor(sourceWuguo)
	if got := wuguo.ListURL(wuguoBaseURL, 3); got != "https://www.duanju55.com/index.php/vod/type/id/1/page/3.html" {
		t.Fatalf("伍果第 3 页地址错误：%q", got)
	}

	// 网果搜索被 WAF 拦死，profile 里必须是 nil，否则用户点搜索只会看到"安全验证"。
	wangguo, _ := maccmsProfileFor(sourceWangguo)
	if wangguo.SearchURL != nil {
		t.Fatal("网果搜索应保持 nil（站点返回安全验证页）")
	}
}

func TestMaccmsSourceForHost(t *testing.T) {
	cases := map[string]string{
		"www.zywest263.com": sourceHuaguo,
		"www.duanju2.com":   sourceWangguo,
		"www.xzyx168.com":   sourceFaguo,
		"www.duanju55.com":  sourceWuguo,
		"huangguoai.com":    "",
	}
	for host, expected := range cases {
		if actual := maccmsSourceForHost(host); actual != expected {
			t.Fatalf("%s → %q，期望 %q", host, actual, expected)
		}
	}
}

func TestMaccmsSourcesAreRegisteredProviders(t *testing.T) {
	for _, source := range []string{sourceHuaguo, sourceWangguo, sourceFaguo, sourceWuguo} {
		if !isHuangguoProviderSource(source) {
			t.Fatalf("%s 没注册进 provider 站源白名单", source)
		}
		if canonicalProviderSource(source) != source {
			t.Fatalf("%s 的规范名被改写成了 %q", source, canonicalProviderSource(source))
		}
		if _, _, valid := splitProviderDramaID(providerDramaID(source, "1")); !valid {
			t.Fatalf("%s 的剧集 ID 无法解析", source)
		}
	}
}
