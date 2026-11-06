package core

// 苹果CMS 系站源的榜单解析（0.8.25 第三批）。
//
// 网果 /label/top.html 与发果 /xzyxvt/<slug>.html 都是普通卡片列表页，
// 直接复用 maccmsCards；名次按页面出现顺序推（这些站不标注序号）。
// 囧次元 label/hot.html 特殊：是「日漫榜 + 剧场榜」两张纯文字 paper 榜，
// 每条带序号但没有封面，单独解析。

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// buildMaccmsRanking 把 maccmsCards 的结果包装成榜单页。
// 页面上不写名次，按出现顺序编号；同一部剧去重后重排。
func buildMaccmsRanking(items []Drama, board rankingBoard) (rankingPage, error) {
	if len(items) == 0 {
		return rankingPage{}, fmt.Errorf("%s 榜单未解析到内容，站点页面可能已变化", board.Name)
	}
	result := rankingPage{Items: make([]rankingItem, 0, len(items)), TotalPages: 1}
	seen := map[string]bool{}
	for _, item := range items {
		// maccmsCards 认卡片的锚点是详情链接，ID 恒非空；空 ID / 空标题
		// 只可能来自模板残片，直接跳过。
		if item.ID == "" || item.Title == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		result.Items = append(result.Items, rankingItem{Rank: len(result.Items) + 1, Drama: item})
	}
	if len(result.Items) == 0 {
		return rankingPage{}, fmt.Errorf("%s 榜单条目全部无效", board.Name)
	}
	return result, nil
}

// buildHuangjuRanking / buildDuanju51Ranking：这两个站点的目录接口
// （黄剧 /dramas、51 的 /rank/ 卡片）已经产出 Drama 列表，只补名次。
func buildHuangjuRanking(items []Drama, board rankingBoard, page int) (rankingPage, error) {
	if len(items) == 0 {
		return rankingPage{}, errors.New("黄剧榜单未返回内容，站点可能暂不可用")
	}
	pageSize := 20
	result := rankingPage{
		Items:      make([]rankingItem, 0, len(items)),
		HasMore:    len(items) >= pageSize,
		TotalPages: page + 1,
	}
	seen := map[string]bool{}
	for index, item := range items {
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		rank := (page-1)*pageSize + index + 1
		metric := ""
		if item.Heat != "" {
			metric = item.Heat + " 热度"
		}
		result.Items = append(result.Items, rankingItem{Rank: rank, Drama: item, Metric: metric})
	}
	if len(result.Items) == 0 {
		return rankingPage{}, errors.New("黄剧榜单条目全部无效")
	}
	return result, nil
}

func buildDuanju51Ranking(items []Drama, board rankingBoard, page int) (rankingPage, error) {
	if len(items) == 0 {
		return rankingPage{}, fmt.Errorf("51短剧%s未返回内容，站点可能暂不可用", board.Name)
	}
	pageSize := 20
	result := rankingPage{
		Items:      make([]rankingItem, 0, len(items)),
		HasMore:    len(items) >= pageSize,
		TotalPages: page + 1,
	}
	seen := map[string]bool{}
	for index, item := range items {
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		rank := (page-1)*pageSize + index + 1
		result.Items = append(result.Items, rankingItem{Rank: rank, Drama: item})
	}
	if len(result.Items) == 0 {
		return rankingPage{}, errors.New("51短剧榜单条目全部无效")
	}
	return result, nil
}

// ---------------------------------------------------------------- E站新番榜

// ezdmwRankingEntry：`<a href="/Index/bangumi/81018.html"...>
// <p><span>1</span><strong>标题</strong></em></p><img .../><strong>简介</strong></a>`
var (
	ezdmwRankingBlock = regexp.MustCompile(`(?is)<a\s+href="(/Index/bangumi/[0-9]+\.html)"[^>]*>\s*<p>\s*<span>([0-9]{1,3})</span>\s*<strong[^>]*>([^<]{1,120})</strong>`)
)

// parseEZDMWRanking 解析 E站 fan_ranking 页。
//
// 页面顶部的月份链接（2026年10月 / 7月 / 1月）只做展示，不参与解析 ——
// 不带 name 参数的 /Index/fan_ranking.html 默认返回当季，跟导航一致。
func parseEZDMWRanking(body, base string) (rankingPage, error) {
	failure := errors.New("E站新番榜格式或页面已变化，请稍后重试")
	matches := ezdmwRankingBlock.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return rankingPage{}, failure
	}
	result := rankingPage{Items: make([]rankingItem, 0, len(matches)), TotalPages: 1}
	seen := map[string]bool{}
	previous := 0
	for _, match := range matches {
		id := strings.TrimSuffix(strings.TrimPrefix(match[1], "/Index/bangumi/"), ".html")
		rank, err := strconv.Atoi(strings.TrimSpace(match[2]))
		title := strings.TrimSpace(match[3])
		if err != nil || rank <= previous || title == "" || seen[id] {
			return rankingPage{}, failure
		}
		seen[id], previous = true, rank
		result.Items = append(result.Items, rankingItem{Rank: rank, Drama: Drama{
			ID:          providerDramaID(sourceEZDMW, id),
			Source:      sourceEZDMW,
			SourceID:    id,
			Title:       title,
			Name:        title,
			ChannelName: "E站",
		}})
	}
	if len(result.Items) == 0 {
		return rankingPage{}, failure
	}
	return result, nil
}

// ---------------------------------------------------------------- 囧次元 paper 榜

// jcydmPaperTitle：区块标题（h3.module-paper-item-title）。
var jcydmPaperTitle = regexp.MustCompile(`(?is)<h3[^>]*class="[^"]*module-paper-item-title[^"]*"[^>]*>\s*([^<]{1,30})\s*</h3>`)

// jcydmPaperEntry：`<a href="/index.php/vod/detail/id/13686.html">
// <div class="module-paper-item-num ...">1</div><div class="module-paper-item-info">
// <span class="module-paper-item-infotitle">标题</span><p>更新状态</p></div></a>`
var jcydmPaperEntry = regexp.MustCompile(`(?is)<a\s+href="(/index\.php/vod/detail/id/[0-9]+\.html)"[^>]*>\s*<div[^>]*class="[^"]*module-paper-item-num[^"]*"[^>]*>\s*([0-9]{1,3})\s*</div>\s*<div[^>]*class="[^"]*module-paper-item-info[^"]*"[^>]*>\s*<span[^>]*class="[^"]*module-paper-item-infotitle[^"]*"[^>]*>\s*([^<]{1,120})\s*</span>\s*(?:<p>([^<]{0,40})</p>)?`)

// parseJcydmRanking 解析囧次元 label/hot 页里的两张 paper 榜。
//
// 页面含「日漫榜」与「动漫剧场榜」两个区块，按 board.upstreamKey 取其一：
// riman → 日漫榜；juchangban → 动漫剧场榜。paper 榜每条只有序号 / 标题 /
// 更新状态，**没有封面** —— 同一页的卡片区（含封面）里能对上 ID 的顺手补上
// （实测 20 条里有 7 条能补），其余等详情页 / 目录合并时再补。
func parseJcydmRanking(body, base string, board rankingBoard) (rankingPage, error) {
	profile, found := maccmsProfileFor(sourceJcydm)
	if !found {
		return rankingPage{}, errors.New("囧次元站源配置缺失")
	}
	// 同页卡片区的封面索引：paper 榜自己不带图，这是唯一不额外发请求的来源。
	covers := map[string]string{}
	for _, card := range maccmsCards(body, profile, strings.TrimRight(base, "/")) {
		if cover := nativeText(card.Cover); cover != "" {
			covers[card.SourceID] = cover
		}
	}
	// 找目标区块标题的定位文本。
	want := "日漫榜"
	if board.upstreamKey == "juchangban" {
		want = "动漫剧场榜"
	}
	target := -1
	for _, loc := range jcydmPaperTitle.FindAllStringSubmatchIndex(body, -1) {
		if strings.Contains(body[loc[2]:loc[3]], want) {
			target = loc[3]
			break
		}
	}
	if target < 0 {
		return rankingPage{}, fmt.Errorf("囧次元%s区块缺失，站点页面可能已变化", want)
	}
	// 区块边界：下一个 paper 区块标题（或页面尾）。
	rest := body[target:]
	end := len(rest)
	if next := jcydmPaperTitle.FindStringIndex(rest); next != nil {
		end = next[0]
	}
	segment := rest[:end]

	matches := jcydmPaperEntry.FindAllStringSubmatch(segment, -1)
	if len(matches) == 0 {
		return rankingPage{}, fmt.Errorf("囧次元%s未解析到条目", want)
	}
	result := rankingPage{Items: make([]rankingItem, 0, len(matches)), TotalPages: 1}
	seen := map[string]bool{}
	previous := 0
	for _, match := range matches {
		id := strings.TrimSuffix(strings.TrimPrefix(match[1], "/index.php/vod/detail/id/"), ".html")
		rank, err := strconv.Atoi(strings.TrimSpace(match[2]))
		title, remark := strings.TrimSpace(match[3]), strings.TrimSpace(match[4])
		if err != nil || rank <= previous || title == "" || seen[id] {
			return rankingPage{}, fmt.Errorf("囧次元%s条目无效", want)
		}
		seen[id], previous = true, rank
		drama := Drama{
			ID:           providerDramaID(sourceJcydm, id),
			Source:       sourceJcydm,
			SourceID:     id,
			Title:        title,
			Name:         title,
			Remark:       remark,
			ChannelName:  profile.Name,
			CategoryName: profile.Name,
		}
		if status := maccmsReleaseStatus(remark); status != "" {
			drama.ReleaseStatus = status
		}
		if cover := covers[id]; cover != "" {
			drama.Cover, drama.CoverURL = cover, cover
		}
		result.Items = append(result.Items, rankingItem{Rank: rank, Drama: drama})
	}
	if len(result.Items) == 0 {
		return rankingPage{}, fmt.Errorf("囧次元%s条目全部无效", want)
	}
	return result, nil
}
