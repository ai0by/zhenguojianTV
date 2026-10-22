package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const ezdmwBaseURL = "https://www.ezdmw.org"
const ezdmwPageSize = 20

var ezdmwBaseMirrors = []string{
	"https://www.ezdmw.org",
	"https://m.ezdmw.org",
	"https://ezdmw.org",
}

var (
	ezdmwDetailPathPattern = regexp.MustCompile("(?i)^/(?:show|Index/show|Index/detail|Index/bangumi|Index/video|v)/([0-9]+)\\.html$")
	ezdmwEpisodePathPattern = regexp.MustCompile("(?i)^/(?:Index/bangumi|Index/video|v)/([0-9]+)\\.html$")
	ezdmwAnchorPattern = regexp.MustCompile("(?is)<a\\b([^>]*)>(.*?)</a>")
	ezdmwEpisodeCountPattern = regexp.MustCompile("(?i)(?:更新到|更新至)\\s*0*([0-9]+)|(?:共|全)\\s*0*([0-9]+)\\s*(?:集|话|期)|([0-9]+)\\s*集\\s*完结")
	ezdmwMediaURLPattern = regexp.MustCompile("(?i)(?:https?:)?//[^\\\"'<>\\s]+?\\.(?:m3u8|mp4)(?:\\?[^\\\"'<>\\s]*)?")
	ezdmwVideoTagPattern = regexp.MustCompile("(?is)<(?:video|source)\\b[^>]*>")
	ezdmwIframePattern = regexp.MustCompile("(?is)<iframe\\b[^>]*>")
	ezdmwPlayerDataPattern = regexp.MustCompile("(?is)(?:player_data|playerData|video_data|videoData)\\s*[:=]\\s*[\\\"']([^\\\"']{16,})[\\\"']")
	ezdmwImageTagPattern = regexp.MustCompile("(?is)<img\\b[^>]*>")
	ezdmwTitleEpisodeSuffix = regexp.MustCompile("(?i)\\s*第\\s*0*[0-9]+\\s*集.*$")
	ezdmwTitleSiteSuffix = regexp.MustCompile("(?i)\\s*[-|_—·]*\\s*(?:E站弹幕网|E站动漫|E站)\\s*$")
	ezdmwHTMLCommentPattern = regexp.MustCompile("(?s)<!--.*?-->")
	ezdmwNumericIDPattern = regexp.MustCompile("^[0-9]{1,12}$")
)

func ezdmwMirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "m.ezdmw.org" || host == "www.ezdmw.org" || host == "ezdmw.org"
}

func ezdmwAttr(raw string, names ...string) string {
	for _, name := range names {
		pattern := regexp.MustCompile("(?is)(?:^|\\s)" + regexp.QuoteMeta(name) + "\\s*=\\s*[\\\"']([^\\\"']*)[\\\"']")
		if match := pattern.FindStringSubmatch(raw); len(match) > 1 {
			return strings.TrimSpace(html.UnescapeString(match[1]))
		}
	}
	return ""
}

func ezdmwElementBlock(raw, tag string, matchOpening func(string) bool) string {
	startPattern := regexp.MustCompile("(?is)<" + regexp.QuoteMeta(tag) + "\\b[^>]*>")
	tokenPattern := regexp.MustCompile("(?is)</?" + regexp.QuoteMeta(tag) + "\\b[^>]*>")
	for _, start := range startPattern.FindAllStringIndex(raw, -1) {
		opening := raw[start[0]:start[1]]
		if matchOpening != nil && !matchOpening(opening) {
			continue
		}
		depth := 1
		cursor := start[1]
		for _, token := range tokenPattern.FindAllStringIndex(raw[cursor:], -1) {
			tokenText := raw[cursor+token[0] : cursor+token[1]]
			if strings.HasPrefix(strings.TrimSpace(tokenText), "</") {
				depth--
			} else if !strings.HasSuffix(strings.TrimSpace(tokenText), "/>") {
				depth++
			}
			if depth == 0 {
				return raw[start[0] : cursor+token[1]]
			}
		}
	}
	return ""
}

func ezdmwDramaIDFromURL(raw string) string {
	parsed, err := url.Parse(html.UnescapeString(strings.TrimSpace(raw)))
	if err != nil {
		return ""
	}
	match := ezdmwDetailPathPattern.FindStringSubmatch(parsed.Path)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func ezdmwEpisodeIDFromURL(raw string) string {
	parsed, err := url.Parse(html.UnescapeString(strings.TrimSpace(raw)))
	if err != nil {
		return ""
	}
	match := ezdmwEpisodePathPattern.FindStringSubmatch(parsed.Path)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func ezdmwCleanTitle(raw string) string {
	title := cleanText(raw)
	title = ezdmwTitleEpisodeSuffix.ReplaceAllString(title, "")
	title = ezdmwTitleSiteSuffix.ReplaceAllString(title, "")
	return strings.Trim(title, " \t\r\n-|_—·")
}

func parseEZDMWEpisodeCount(raw string) int {
	match := ezdmwEpisodeCountPattern.FindStringSubmatch(raw)
	if len(match) < 2 {
		return 0
	}
	for _, group := range match[1:] {
		if group == "" {
			continue
		}
		if count, err := strconv.Atoi(group); err == nil && count > 0 {
			return count
		}
	}
	return 0
}

func ezdmwNearbyTitle(raw string, start, end int) string {
	pPattern := regexp.MustCompile("(?is)<p\\b[^>]*>(.*?)</p>")
	bestDistance := int(^uint(0) >> 1)
	bestTitle := ""
	for _, match := range pPattern.FindAllStringSubmatchIndex(raw, -1) {
		if len(match) < 4 {
			continue
		}
		distance := 0
		if match[1] < start {
			distance = start - match[1]
		} else if match[0] > end {
			distance = match[0] - end
		}
		if distance > 600 || distance >= bestDistance {
			continue
		}
		paragraph := ezdmwHTMLCommentPattern.ReplaceAllString(raw[match[2]:match[3]], " ")
		title := ezdmwCleanTitle(cleanText(paragraph))
		if title != "" {
			bestDistance = distance
			bestTitle = title
		}
	}
	return bestTitle
}

func parseEZDMWCatalogItems(raw, pageURL string) []Drama {
	scope := ezdmwElementBlock(raw, "section", func(opening string) bool {
		return strings.EqualFold(extractAttr(opening, "id"), "some_drama")
	})
	if scope == "" {
		scope = raw
	}
	positions := map[string]int{}
	items := make([]Drama, 0, ezdmwPageSize)
	for _, match := range ezdmwAnchorPattern.FindAllStringSubmatchIndex(scope, -1) {
		attributes := scope[match[2]:match[3]]
		inner := scope[match[4]:match[5]]
		href := extractAttr(attributes, "href")
		sourceID := ezdmwDramaIDFromURL(href)
		if sourceID == "" {
			continue
		}
		title := ezdmwCleanTitle(cleanText(inner))
		if title == "" {
			title = ezdmwCleanTitle(extractAttr(attributes, "title", "aria-label"))
		}
		if title == "" {
			title = ezdmwNearbyTitle(scope, match[0], match[1])
		}
		cover := ""
		for _, tag := range ezdmwImageTagPattern.FindAllString(inner, -1) {
			cover = firstNonEmpty(ezdmwAttr(tag, "data-src", "data-original", "src"))
			if cover != "" {
				break
			}
		}
		coverURL := ""
		if cover != "" {
			resolved := resolveProviderURL(pageURL, cover)
			if isProviderHTTPMediaURL(resolved) {
				coverURL = resolved
			}
		}
		contextText := cleanText(contextBlock(scope, match[0], match[1], 180))
		count := parseEZDMWEpisodeCount(contextText)
		releaseStatus := ""
		if strings.Contains(contextText, "完结") {
			releaseStatus = "finished"
		} else if strings.Contains(contextText, "连载") || strings.Contains(contextText, "更新") {
			releaseStatus = "ongoing"
		}
		drama := Drama{
			ID: providerDramaID(sourceEZDMW, sourceID), Source: sourceEZDMW, SourceID: sourceID,
			Title: title, Name: title, Cover: coverURL, CoverURL: coverURL,
			CategoryName: "动漫", Episodes: count, TotalEpisode: count, EpisodeCount: count,
			ReleaseStatus: releaseStatus,
		}
		if index, exists := positions[sourceID]; exists {
			existing := items[index]
			if existing.Title == "" && title != "" {
				existing.Title, existing.Name = title, title
			}
			if existing.Cover == "" && coverURL != "" {
				existing.Cover, existing.CoverURL = coverURL, coverURL
			}
			if (existing.Episodes == nil || nativeText(existing.Episodes) == "0") && count > 0 {
				existing.Episodes, existing.TotalEpisode, existing.EpisodeCount = count, count, count
			}
			if existing.ReleaseStatus == "" && releaseStatus != "" {
				existing.ReleaseStatus = releaseStatus
			}
			items[index] = existing
			continue
		}
		positions[sourceID] = len(items)
		items = append(items, drama)
	}
	out := make([]Drama, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Title) != "" {
			out = append(out, item)
		}
	}
	return out
}

func ezdmwHasMore(raw string, count int) bool {
	for _, match := range ezdmwAnchorPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) < 3 {
			continue
		}
		label := strings.ToLower(cleanText(match[2]))
		if strings.Contains(label, "下一页") || strings.Contains(label, "下页") || label == "next" {
			return true
		}
	}
	return count >= ezdmwPageSize
}

func (d *Downloader) fetchEZDMWCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if strings.TrimSpace(category) != "" {
		return nil, false, errors.New("E站不支持该分类")
	}
	if page < 1 {
		page = 1
	}
	base := strings.TrimRight(d.providerBaseURL(sourceEZDMW), "/")
	address := base + "/"
	if query = strings.TrimSpace(query); query != "" {
		address = base + "/Index/search.html?searchText=" + url.QueryEscape(query)
	}
	if page > 1 {
		separator := "?"
		if strings.Contains(address, "?") {
			separator = "&"
		}
		address += separator + "page=" + strconv.Itoa(page)
	}
	body, err := d.fetchProviderText(ctx, address, base+"/")
	if err != nil {
		return nil, false, err
	}
	items := parseEZDMWCatalogItems(body, address)
	if len(items) == 0 && query == "" && page == 1 {
		homeURL := base + "/"
		if homeBody, homeErr := d.fetchProviderText(ctx, homeURL, homeURL); homeErr == nil {
			items = parseEZDMWCatalogItems(homeBody, homeURL)
			if len(items) > 0 {
				body = homeBody
			}
		}
	}
	if len(items) == 0 {
		return nil, false, errors.New("E站目录未解析到番剧，站点页面结构可能已变化")
	}
	more := false
	if query != "" {
		more = ezdmwHasMore(body, len(items))
	}
	return items, more, nil
}

func ezdmwEpisodeTitle(raw string, index int) string {
	title := ezdmwCleanTitle(raw)
	if title == "" || regexp.MustCompile("^[0-9]+$").MatchString(title) {
		return fmt.Sprintf("第%d集", index)
	}
	if episodeIndex(title, 0) > 0 {
		return title
	}
	return fmt.Sprintf("第%d集", index)
}

func parseEZDMWEpisodes(raw, detailURL, sourceID string) []Chapter {
	scope := ezdmwElementBlock(raw, "section", func(opening string) bool {
		for _, class := range strings.Fields(extractAttr(opening, "class")) {
			if class == "anthology" {
				return true
			}
		}
		return false
	})
	if scope == "" {
		scope = raw
	}
	seenIDs := map[string]bool{}
	seenIndexes := map[int]bool{}
	chapters := make([]Chapter, 0, 24)
	for _, match := range ezdmwAnchorPattern.FindAllStringSubmatchIndex(scope, -1) {
		attributes := scope[match[2]:match[3]]
		inner := scope[match[4]:match[5]]
		href := extractAttr(attributes, "href")
		pageURL := resolveProviderURL(detailURL, href)
		episodeID := ezdmwEpisodeIDFromURL(pageURL)
		if episodeID == "" || seenIDs[episodeID] || !isProviderHTTPMediaURL(pageURL) {
			continue
		}
		// E站的 /Index/bangumi/ 页面是番剧/分集信息页，真正的播放线路
		// 使用 /Index/video/{id}.html；旧 /v/ 链接也统一映射到该播放路由。
		if parsedPage, parseErr := url.Parse(pageURL); parseErr == nil {
			parsedPage.Path = "/Index/video/" + episodeID + ".html"
			pageURL = parsedPage.String()
		}
		title := cleanText(inner)
		if title == "" {
			title = extractAttr(attributes, "title", "aria-label")
		}
		index := episodeIndex(title, 0)
		if index <= 0 {
			index = episodeIndex(href, 0)
		}
		if index <= 0 {
			index = len(chapters) + 1
		}
		if seenIndexes[index] {
			continue
		}
		title = ezdmwEpisodeTitle(title, index)
		seenIDs[episodeID], seenIndexes[index] = true, true
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceEZDMW, sourceID, episodeID),
			Source: sourceEZDMW, Title: title,
			VideoURL: "ezdmw-play://" + episodeID, PageURL: pageURL,
			Referer: detailURL, CurrentEpisode: rawEpisode(index),
		})
	}
	sortProviderChapters(chapters)
	return uniqueChapters(chapters)
}

func ezdmwCoverURL(raw, pageURL string) string {
	metaPattern := regexp.MustCompile("(?is)<meta\\b[^>]*(?:property|name)=[\\\"'](?:og:image|twitter:image)[\\\"'][^>]*>")
	for _, tag := range metaPattern.FindAllString(raw, -1) {
		if address := ezdmwAttr(tag, "content"); address != "" {
			resolved := resolveProviderURL(pageURL, address)
			if isProviderHTTPMediaURL(resolved) {
				return resolved
			}
		}
	}
	for _, tag := range ezdmwImageTagPattern.FindAllString(raw, -1) {
		address := ezdmwAttr(tag, "data-src", "data-original", "src")
		if address == "" {
			continue
		}
		resolved := resolveProviderURL(pageURL, address)
		if isProviderHTTPMediaURL(resolved) {
			return resolved
		}
	}
	return ""
}

func (d *Downloader) fetchEZDMWDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimSpace(sourceID)
	if !ezdmwNumericIDPattern.MatchString(sourceID) {
		return Drama{}, nil, errors.New("E站番剧 ID 无效")
	}
	base := strings.TrimRight(d.providerBaseURL(sourceEZDMW), "/")
	// E站的列表入口在不同页面/版本中可能链接到番剧页或某一集的
	// /Index/bangumi/、/v/ 页面；这些页面都可能包含完整选集列表。
	paths := []string{
		"/Index/bangumi/" + sourceID + ".html",
		"/show/" + sourceID + ".html",
		"/Index/show/" + sourceID + ".html",
		"/Index/detail/" + sourceID + ".html",
		"/v/" + sourceID + ".html",
	}
	var body, detailURL string
	var chapters []Chapter
	var lastErr error
	for _, path := range paths {
		candidateURL := base + path
		candidateBody, err := d.fetchProviderText(ctx, candidateURL, base+"/")
		if err != nil {
			lastErr = err
			continue
		}
		candidateChapters := parseEZDMWEpisodes(candidateBody, candidateURL, sourceID)
		if len(candidateChapters) == 0 {
			lastErr = errors.New("页面未解析到分集")
			continue
		}
		body, detailURL, chapters = candidateBody, candidateURL, candidateChapters
		break
	}
	if len(chapters) == 0 {
		if lastErr != nil {
			return Drama{}, nil, fmt.Errorf("E站未找到包含选集的详情页：%w", lastErr)
		}
		return Drama{}, nil, errors.New("E站未找到包含选集的详情页")
	}
	title := ezdmwCleanTitle(extractPageTitle(body))
	if title == "" {
		title = "番剧 " + sourceID
	}
	count := len(chapters)
	releaseStatus := "ongoing"
	if strings.Contains(body, "完结") {
		releaseStatus = "finished"
	}
	cover := ezdmwCoverURL(body, detailURL)
	description := extractDescription(body)
	drama := Drama{
		ID: providerDramaID(sourceEZDMW, sourceID), Source: sourceEZDMW, SourceID: sourceID,
		Title: title, Name: title, Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, CategoryName: "动漫",
		Episodes: count, TotalEpisode: count, EpisodeCount: count, ReleaseStatus: releaseStatus,
	}
	return drama, chapters, nil
}

func (d *Downloader) fetchEZDMWChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	drama, chapters, err := d.fetchEZDMWDetail(ctx, sourceID)
	if err != nil {
		return "", nil, err
	}
	return drama.DisplayTitle(), chapters, nil
}

func ezdmwUnescapePlayerText(raw string) string {
	return strings.NewReplacer(
		"\\/", "/",
		"\\u0026", "&",
		"\\u002f", "/",
		"\\u003a", ":",
		"\\x26", "&",
		"&amp;", "&",
	).Replace(html.UnescapeString(raw))
}

func ezdmwPlaylistURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.Contains(strings.ToLower(parsed.Path), "/index/listres/")
}

func ezdmwDirectMedia(rawHTML, pageURL string) string {
	rawHTML = ezdmwUnescapePlayerText(rawHTML)
	for _, tag := range ezdmwVideoTagPattern.FindAllString(rawHTML, -1) {
		if address := ezdmwAttr(tag, "src", "data-src", "data-url"); address != "" {
			// E站播放器的 listres 接口使用 .webp 后缀，但页面脚本明确交给 Hls.js 加载。
			if strings.Contains(strings.ToLower(address), "/index/listres/") || strings.Contains(strings.ToLower(address), "/listres/") {
				if media := resolveProviderURL(pageURL, address); isProviderHTTPMediaURL(media) {
					return media
				}
			}
			if media := normalizeProviderMediaURL(pageURL, address); media != "" && isProviderHTTPMediaURL(media) {
				return media
			}
		}
	}
	if media := parseDataHLS(rawHTML, pageURL); media != "" && isProviderHTTPMediaURL(media) {
		return media
	}
	if media := parseAIVideoURL(rawHTML, pageURL); media != "" && isProviderHTTPMediaURL(media) {
		return media
	}
	for _, address := range ezdmwMediaURLPattern.FindAllString(rawHTML, -1) {
		address = strings.TrimRight(address, ".,);]")
		if strings.HasPrefix(address, "//") {
			address = "https:" + address
		}
		if media := normalizeProviderMediaURL(pageURL, address); media != "" && isProviderHTTPMediaURL(media) {
			return media
		}
	}
	return ""
}

func ezdmwMediaFromPlayerFrameURL(frameURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(frameURL))
	if err != nil {
		return ""
	}
	for _, key := range []string{"src", "url", "file", "video", "media", "play"} {
		candidate := strings.TrimSpace(parsed.Query().Get(key))
		for depth := 0; depth < 4 && candidate != ""; depth++ {
			if media := ezdmwDirectMedia(candidate, frameURL); media != "" {
				return media
			}
			var decoded []byte
			var decodeErr error
			decoded, decodeErr = base64.RawURLEncoding.DecodeString(candidate)
			if decodeErr != nil {
				decoded, decodeErr = base64.URLEncoding.DecodeString(candidate)
			}
			if decodeErr != nil {
				decoded, decodeErr = base64.RawStdEncoding.DecodeString(candidate)
			}
			if decodeErr != nil {
				decoded, decodeErr = base64.StdEncoding.DecodeString(candidate)
			}
			if decodeErr == nil {
				candidate = strings.TrimSpace(string(decoded))
				continue
			}
			unescaped, unescapeErr := url.QueryUnescape(candidate)
			if unescapeErr == nil && unescaped != candidate {
				candidate = strings.TrimSpace(unescaped)
				continue
			}
			break
		}
	}
	return ""
}

func parseEZDMWPlayerMedia(rawHTML, pageURL string) string {
	if media := ezdmwDirectMedia(rawHTML, pageURL); media != "" {
		return media
	}
	for _, match := range ezdmwPlayerDataPattern.FindAllStringSubmatch(rawHTML, -1) {
		candidate := strings.TrimSpace(html.UnescapeString(match[1]))
		for depth := 0; depth < 3; depth++ {
			if media := ezdmwDirectMedia(candidate, pageURL); media != "" {
				return media
			}
			decoded, err := base64.StdEncoding.DecodeString(candidate)
			if err != nil {
				decoded, err = base64.RawStdEncoding.DecodeString(candidate)
			}
			if err != nil {
				break
			}
			candidate = string(decoded)
		}
		if decoded, err := url.QueryUnescape(candidate); err == nil {
			if media := ezdmwDirectMedia(decoded, pageURL); media != "" {
				return media
			}
		}
	}
	return ""
}

func ezdmwSafePageURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isProviderHTTPMediaURL(raw) {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "0.0.0.0" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return false
	}
	return true
}

func parseEZDMWPlayerFrame(rawHTML, pageURL string) string {
	var fallback string
	for _, tag := range ezdmwIframePattern.FindAllString(rawHTML, -1) {
		address := ezdmwAttr(tag, "src", "data-src")
		if address == "" {
			continue
		}
		resolved := resolveProviderURL(pageURL, address)
		if !ezdmwSafePageURL(resolved) {
			continue
		}
		if fallback == "" {
			fallback = resolved
		}
		lower := strings.ToLower(address)
		if strings.Contains(lower, "player") || strings.Contains(lower, "play") || strings.Contains(lower, "embed") {
			return resolved
		}
	}
	return fallback
}

func (d *Downloader) resolveEZDMWPlayback(ctx context.Context, task Task) (providerMedia, error) {
	source, _, valid := splitProviderDramaID(task.DramaID)
	if !valid || source != sourceEZDMW {
		return providerMedia{}, errors.New("E站番剧信息无效，请刷新详情")
	}
	episodeID := strings.TrimPrefix(strings.TrimSpace(task.Chapter.VideoURL), "ezdmw-play://")
	base := strings.TrimRight(d.providerBaseURL(sourceEZDMW), "/")
	pageURL := strings.TrimSpace(task.Chapter.PageURL)
	if pageURL == "" {
		if !ezdmwNumericIDPattern.MatchString(episodeID) {
			return providerMedia{}, errors.New("E站分集播放标识无效，请刷新详情")
		}
		pageURL = base + "/Index/bangumi/" + episodeID + ".html"
	}
	pageURL = resolveProviderURL(base+"/", pageURL)
	parsed, parseErr := url.Parse(pageURL)
	if parseErr != nil || !ezdmwMirrorHost(parsed.Hostname()) || !ezdmwSafePageURL(pageURL) {
		return providerMedia{}, errors.New("E站分集页面地址无效")
	}
	referer := base + "/"
	visited := map[string]bool{}
	for depth := 0; depth < 3; depth++ {
		if visited[pageURL] {
			return providerMedia{}, errors.New("E站播放线路出现循环跳转")
		}
		visited[pageURL] = true
		body, err := d.fetchProviderText(ctx, pageURL, referer)
		if err != nil {
			return providerMedia{}, fmt.Errorf("获取E站播放页面失败：%w", err)
		}
		if media := parseEZDMWPlayerMedia(body, pageURL); media != "" {
			return providerMedia{URL: media, Referer: pageURL}, nil
		}
		next := parseEZDMWPlayerFrame(body, pageURL)
		if next == "" || visited[next] {
			break
		}
		if media := ezdmwMediaFromPlayerFrameURL(next); media != "" {
			return providerMedia{URL: media, Referer: pageURL}, nil
		}
		referer, pageURL = pageURL, next
	}
	return providerMedia{}, errors.New("E站未解析到可播放的媒体地址，可能线路失效或需要浏览器验证")
}
