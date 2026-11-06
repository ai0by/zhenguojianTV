package core

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type nativeCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var nativeAICategories = []nativeCategory{
	{ID: "ai-duanju", Name: "AI 短剧"},
	{ID: "ai-manju", Name: "AI 漫剧"},
	{ID: "ai-huanlian", Name: "AI 换脸"},
	{ID: "ai-mogai", Name: "AI 魔改"},
}

func nativeCatalogKey(source, category string) string {
	source = canonicalProviderSource(source)
	if category = strings.TrimSpace(category); category != "" {
		return source + "|" + category
	}
	return source
}

func validNativeCategory(source, category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 128 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	switch source {
	case sourceHongguo:
		for _, genre := range hongguoAppGenres {
			if category == genre.key {
				return true
			}
		}
	case sourceHuangguoAI:
		for _, entry := range nativeAICategories {
			if category == entry.ID {
				return true
			}
		}
	case sourceHuangguoVideo:
		id, err := strconv.Atoi(category)
		return err == nil && id > 0
	case sourceCloudFront:
		return strings.TrimSpace(category) == category
	case sourceHuangju:
		return category == huangjuNewestCategory || validHuangjuID(category) && !strings.HasPrefix(category, "@")
	case sourceDuanju51:
		return validDuanju51Category(category)
	case sourceSorani:
		return validSoraniCategory(category)
	case sourceHema:
		return validHemaCategory(category)
	case sourceHuangdou:
		// 黄豆 UI 没有分类，但目录抓取用 `order:<v>` 切换排序维度
		// （单排序被站点卡在 3000 部，三种排序并集才是 5000）。
		if !strings.HasPrefix(category, huangdouOrderCategoryPrefix) {
			return false
		}
		order := strings.TrimPrefix(category, huangdouOrderCategoryPrefix)
		for _, known := range huangdouOrderCategories() {
			if order == known {
				return true
			}
		}
		return false
	}
	return false
}

func (engine *nativeEngine) nativeCategories(ctx context.Context, source string, force bool) ([]nativeCategory, error) {
	return engine.nativeCategoriesFull(ctx, source, force, false)
}

// nativeCategoriesFull 是 nativeCategories 的完整版，多一个 allCategories 开关。
//
// `allCategories=true` 时河马会返回**未截断**的完整分类表（126 项而不是 UI 用
// 的 40 项）。catalogdump 需要它来按分类补全整个剧库，见 hemaWantsAllCategories。
func (engine *nativeEngine) nativeCategoriesFull(ctx context.Context, source string, force, allCategories bool) ([]nativeCategory, error) {
	source = canonicalProviderSource(source)
	all := []nativeCategory{{Name: "全部"}}
	switch source {
	case sourceHongguo:
		for _, genre := range hongguoAppGenres {
			all = append(all, nativeCategory{ID: genre.key, Name: genre.name})
		}
		return all, nil
	case sourceHuangguoAI:
		return append(all, nativeAICategories...), nil
	case sourceHuangdou:
		// UI 侧黄豆没有分类（上游 = 一个混排列表）。
		if !allCategories {
			return all, nil
		}
		// 但目录抓取必须按**排序维度**遍历：单排序被服务端卡在 3000 部，
		// 三种排序的并集才是 5000。传 `order:<v>` 给 catalog 就能切换排序。
		for _, order := range huangdouOrderCategories() {
			if order == huangdouOrderCategoryAll {
				continue
			}
			all = append(all, nativeCategory{
				ID:   huangdouOrderCategoryPrefix + order,
				Name: "排序:" + order,
			})
		}
		return all, nil
	case sourceDuanju51:
		return append(all, duanju51Categories...), nil
	// 青空次元（0.8.15）：上游只有三个固定一级分类，不需要远端探测。
	case sourceSorani:
		return append(all, soraniCategories...), nil
	case sourceYeguo, sourceXiangjiao, sourceEZDMW, sourceHuaguo, sourceWangguo, sourceFaguo, sourceWuguo,
		sourceMxdm, sourceJcydm:
		return all, nil
	}
	engine.mu.Lock()
	cached := append([]nativeCategory{}, engine.categoryOptions[source]...)
	engine.mu.Unlock()
	// 全量模式不能吃缓存：缓存里存的是 UI 那 40 项，用它抓目录会漏分类。
	if len(cached) > 1 && !force && !allCategories {
		return cached, nil
	}
	d := engine.downloader
	var err error
	switch source {
	case sourceCloudFront:
		var tabs legacyTabList
		err = d.fetchAPI(ctx, "/api/app/playlet-tab/all", nil, &tabs)
		for _, tab := range tabs {
			if validNativeCategory(source, tab.ID) && tab.ID != "" && tab.Name != "" {
				all = append(all, nativeCategory{ID: tab.ID, Name: tab.Name})
			}
		}
	case sourceHuangguoVideo:
		address := d.providerBaseURL(source) + "/videos"
		var body string
		body, err = d.fetchProviderText(ctx, address, d.providerBaseURL(source)+"/")
		if err == nil {
			all = append(all, parseHuangguoVideoCategories(body)...)
		}
	case sourceHuangju:
		var categories []nativeCategory
		categories, err = d.fetchHuangjuCategories(ctx)
		if err == nil {
			all = append(all, categories...)
		}
	// 河马剧场（0.8.17）：分类表在 /browse 的页面数据里，上游有 126 项，
	// fetchHemaCategories 会按「热门优先 + 站点原序」截到 hemaMaxCategories。
	// 抓目录时（allCategories）不截断 —— 否则只抓 40 个分类，其余分类下的剧
	// 永远进不了快照，用户在手机上点那些分类反而让本机比远端多。
	case sourceHema:
		previous := hemaWantsAllCategories
		hemaWantsAllCategories = allCategories
		var categories []nativeCategory
		categories, err = d.fetchHemaCategories(ctx)
		hemaWantsAllCategories = previous
		if err == nil {
			all = append(all, categories...)
		}
	default:
		return nil, errors.New("请选择有效站源")
	}
	if err != nil {
		return nil, err
	}
	if len(all) == 1 {
		return nil, errors.New("站源暂未返回内容分类，请稍后重试")
	}
	engine.mu.Lock()
	if engine.categoryOptions == nil {
		engine.categoryOptions = map[string][]nativeCategory{}
	}
	engine.categoryOptions[source] = all
	engine.writeCatalogDiskLocked()
	engine.mu.Unlock()
	return all, nil
}

var categoryAnchorPattern = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
var categoryTextPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func parseHuangguoVideoCategories(body string) []nativeCategory {
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, match := range categoryAnchorPattern.FindAllStringSubmatch(body, -1) {
		href := html.UnescapeString(extractAttr("<a "+match[1]+">", "href"))
		address, err := url.Parse(href)
		if err != nil || address.Path != "" && strings.TrimRight(address.Path, "/") != "/videos" {
			continue
		}
		id := address.Query().Get("category")
		name := strings.TrimSpace(html.UnescapeString(categoryTextPattern.ReplaceAllString(match[2], "")))
		if !validNativeCategory(sourceHuangguoVideo, id) || id == "" || seen[id] || name == "" || len([]rune(name)) > 24 {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
	}
	return categories
}

func (d *Downloader) fetchHuangguoAICatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	categories := nativeAICategories
	if category != "" {
		for _, entry := range categories {
			if entry.ID == category {
				categories = []nativeCategory{entry}
				break
			}
		}
	}
	type categoryResult struct {
		items []Drama
		err   error
	}
	results := make([]categoryResult, len(categories))
	slots := make(chan struct{}, 2)
	var group sync.WaitGroup
	for index, entry := range categories {
		group.Add(1)
		go func(index int, entry nativeCategory) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			address := fmt.Sprintf("%s/api/videos/category/%s?sort=hot&page=%d&size=24", d.providerBaseURL(sourceHuangguoAI), url.PathEscape(entry.ID), page)
			body, err := d.fetchProviderText(ctx, address, d.providerBaseURL(sourceHuangguoAI)+"/"+entry.ID+"/")
			if err == nil {
				results[index].items = parseHuangguoAIJSONCards([]byte(body), address, entry.Name)
			}
			if err != nil {
				results[index].err = fmt.Errorf("%s: %w", entry.Name, err)
			}
		}(index, entry)
	}
	group.Wait()
	var items []Drama
	var failures []error
	more := false
	for _, result := range results {
		items = append(items, result.items...)
		more = more || len(result.items) >= 24 || result.err != nil
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	return items, more, errors.Join(failures...)
}
