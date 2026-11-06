package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rankingCacheTTL = 5 * time.Minute

type rankingBoard struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Name        string `json:"name"`
	Description string `json:"description"`
	path        string
	upstreamKey string
}

var rankingBoards = []rankingBoard{
	{ID: "hongguo-hot", Source: sourceHongguo, Name: "总热播榜", Description: "红果观看、互动等综合热度；每日更新。", path: "hot-drama", upstreamKey: "hongguo"},
	{ID: "hongguo-real", Source: sourceHongguo, Name: "真人剧榜", Description: "红果真人剧热播榜；每日更新。", path: "hot-real-drama", upstreamKey: "real"},
	{ID: "hongguo-comic", Source: sourceHongguo, Name: "漫剧榜", Description: "红果漫剧热播榜；每日更新。", path: "hot-comic-drama", upstreamKey: "comic"},
	{ID: "hongguo-ai", Source: sourceHongguo, Name: "AI剧榜", Description: "红果 AI 剧热播榜；每日更新。", path: "hot-ai-drama", upstreamKey: "ai"},
	{ID: "huangdou-all", Source: sourceHuangdou, Name: "总榜", Description: "黄豆短剧总榜，保留站点返回的顺序。", upstreamKey: "all"},
	{ID: "huangdou-mogai", Source: sourceHuangdou, Name: "魔改榜", Description: "黄豆站点魔改榜。", upstreamKey: "mogai"},
	{ID: "huangdou-search", Source: sourceHuangdou, Name: "搜索榜", Description: "黄豆站点搜索榜。", upstreamKey: "search"},
	{ID: "huangdou-favorite", Source: sourceHuangdou, Name: "收藏榜", Description: "黄豆站点收藏榜。", upstreamKey: "favorite"},
	{ID: "huangdou-finish", Source: sourceHuangdou, Name: "完结榜", Description: "黄豆站点完结榜。", upstreamKey: "finish"},
	{ID: "huangguo-hot", Source: sourceHuangguoAI, Name: "热播榜", Description: "黄果站点热播 TOP 20；每日更新。", path: "hot"},
	{ID: "huangguo-recommend", Source: sourceHuangguoAI, Name: "推荐榜", Description: "黄果站点推荐 TOP 20。", path: "recommend"},
	{ID: "huangguo-potential", Source: sourceHuangguoAI, Name: "潜力榜", Description: "黄果站点潜力 TOP 20。", path: "potential"},
	// ---- 0.8.25 第三批：把其余站源的榜单补齐（用户要求逐源排查、尽量接入）。
	//
	// 上游形态各不相同，解析分别落在 parseHuangjuRanking / parseDuanju51Ranking /
	// parseEZDMWRanking / maccmsCards + paper 榜解析，抓取入口统一走
	// fetchRankingPage 的 switch。
	{ID: "huangju-hot", Source: sourceHuangju, Name: "热播榜", Description: "黄剧站点按热度排序。", upstreamKey: "hot"},
	{ID: "huangju-new", Source: sourceHuangju, Name: "最新上架", Description: "黄剧站点按上架时间排序。", upstreamKey: "new"},
	{ID: "51duanju-total", Source: sourceDuanju51, Name: "总榜", Description: "51短剧播放爆表总榜。", upstreamKey: "total"},
	{ID: "51duanju-week", Source: sourceDuanju51, Name: "周榜", Description: "51短剧一周播放榜。", upstreamKey: "week"},
	{ID: "51duanju-month", Source: sourceDuanju51, Name: "月榜", Description: "51短剧一月播放榜。", upstreamKey: "month"},
	{ID: "ezdmw-season", Source: sourceEZDMW, Name: "新番榜", Description: "E站当季新番人气排行（按月更新）。", upstreamKey: "season"},
	{ID: "wangguo-top", Source: sourceWangguo, Name: "排行榜", Description: "网果站点影片排行榜。", path: "/label/top.html"},
	{ID: "faguo-duanju", Source: sourceFaguo, Name: "短剧月榜", Description: "发果站点短剧月榜。", path: "/xzyxvt/duanjun.html"},
	{ID: "faguo-anime", Source: sourceFaguo, Name: "动漫月榜", Description: "发果站点动漫月榜。", path: "/xzyxvt/dongmann.html"},
	{ID: "jcydm-riman", Source: sourceJcydm, Name: "日漫榜", Description: "囧次元日本动漫人气榜。", upstreamKey: "riman"},
	{ID: "jcydm-juchang", Source: sourceJcydm, Name: "剧场榜", Description: "囧次元动漫剧场人气榜。", upstreamKey: "juchangban"},
}

type rankingItem struct {
	Rank   int    `json:"rank"`
	Drama  Drama  `json:"drama"`
	Metric string `json:"metric,omitempty"`
}

type rankingPage struct {
	BoardID     string        `json:"boardId"`
	Page        int           `json:"page"`
	Items       []rankingItem `json:"items"`
	HasMore     bool          `json:"hasMore"`
	TotalPages  int           `json:"totalPages,omitempty"`
	UpdatedText string        `json:"updatedText,omitempty"`
	FetchedAt   time.Time     `json:"fetchedAt"`
	Stale       bool          `json:"stale,omitempty"`
	Warning     string        `json:"warning,omitempty"`
}

type rankingCall struct {
	done chan struct{}
	page rankingPage
	err  error
}

type rankingCache struct {
	mu      sync.Mutex
	pages   map[string]rankingPage
	pending map[string]*rankingCall
}

func findRankingBoard(id string) (rankingBoard, bool) {
	for _, board := range rankingBoards {
		if board.ID == id {
			return board, true
		}
	}
	return rankingBoard{}, false
}

func cloneRankingPage(page rankingPage) rankingPage {
	page.Items = append([]rankingItem{}, page.Items...)
	return page
}

func (d *Downloader) loadRankingPage(ctx context.Context, board rankingBoard, page int, refresh bool) (rankingPage, error) {
	key := board.ID + ":" + strconv.Itoa(page)
	cache := &d.rankings
	cache.mu.Lock()
	if cache.pages == nil {
		cache.pages = map[string]rankingPage{}
		cache.pending = map[string]*rankingCall{}
	}
	cached, exists := cache.pages[key]
	if exists && !refresh && time.Since(cached.FetchedAt) >= 0 && time.Since(cached.FetchedAt) < rankingCacheTTL {
		cache.mu.Unlock()
		return cloneRankingPage(cached), nil
	}
	if pending := cache.pending[key]; pending != nil {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return rankingPage{}, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.loadRankingPage(ctx, board, page, refresh)
			}
			return cloneRankingPage(pending.page), pending.err
		}
	}
	pending := &rankingCall{done: make(chan struct{})}
	cache.pending[key] = pending
	cache.mu.Unlock()

	result, err := d.fetchRankingPage(ctx, board, page)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err == nil {
		result.BoardID, result.Page, result.FetchedAt = board.ID, page, time.Now()
		if refresh && page == 1 {
			for cachedKey, old := range cache.pages {
				if old.BoardID == board.ID {
					delete(cache.pages, cachedKey)
				}
			}
		}
		if len(cache.pages) >= 128 {
			oldestKey, oldest := "", time.Now()
			for cachedKey, old := range cache.pages {
				if old.FetchedAt.Before(oldest) {
					oldestKey, oldest = cachedKey, old.FetchedAt
				}
			}
			delete(cache.pages, oldestKey)
		}
		cache.pages[key] = cloneRankingPage(result)
		d.writeRankingCacheLocked()
	} else if exists && ctx.Err() == nil && time.Since(cached.FetchedAt) >= 0 && time.Since(cached.FetchedAt) < 24*time.Hour {
		result = cloneRankingPage(cached)
		result.Stale = true
		result.Warning = "站点暂不可用，显示上次取得的榜单，请稍后刷新。"
		err = nil
	}
	pending.page, pending.err = cloneRankingPage(result), err
	delete(cache.pending, key)
	close(pending.done)
	return result, err
}

func (d *Downloader) fetchRankingPage(ctx context.Context, board rankingBoard, page int) (rankingPage, error) {
	switch board.Source {
	case sourceHongguo:
		pageURL := d.providerBaseURL(sourceHongguo) + "/rank/" + board.path + "?page=" + strconv.Itoa(page)
		body, err := d.fetchProviderText(ctx, pageURL, d.providerBaseURL(sourceHongguo)+"/")
		if err != nil {
			return rankingPage{}, err
		}
		return parseHongguoRanking(body, board, page)
	case sourceHuangdou:
		var decoded any
		err := newHuangdouAPIClient(d).call(ctx, "/drama/rank", map[string]any{"tab": board.upstreamKey, "page": strconv.Itoa(page)}, &decoded)
		if err != nil {
			return rankingPage{}, err
		}
		return parseHuangdouRanking(decoded, page)
	case sourceHuangguoAI:
		if page != 1 {
			return rankingPage{}, errors.New("该榜单只有一页")
		}
		pageURL := d.providerBaseURL(sourceHuangguoAI) + "/ranks/" + board.path + "/"
		body, err := d.fetchProviderText(ctx, pageURL, d.providerBaseURL(sourceHuangguoAI)+"/")
		if err != nil {
			return rankingPage{}, err
		}
		return parseHuangguoRanking(body, board)
	// ---- 0.8.25 第三批：黄剧 / 51 / E站 / 网果 / 发果 / 囧次元的榜单。
	case sourceHuangju:
		// 黄剧的 /dramas 接口本身支持 sort=hot/new，目录页一直在用；
		// 榜单只是「不带分类、按排序取前几页」，直接复用。
		// 注意 latest 必须传 @new（sort=new），空分类是 sort=hot。
		if page < 1 || page > 25 {
			return rankingPage{}, errors.New("榜单页码无效")
		}
		category := ""
		if board.upstreamKey == "new" {
			category = huangjuNewestCategory
		}
		items, _, err := d.fetchHuangjuCatalogPage(ctx, page, category, "")
		if err != nil {
			return rankingPage{}, err
		}
		return buildHuangjuRanking(items, board, page)
	case sourceDuanju51:
		// 51 的总/周/月榜都是 /rank/ 系列页面，走现有 Nuxt 卡片解析。
		if page < 1 || page > 50 {
			return rankingPage{}, errors.New("榜单页码无效")
		}
		path := "/rank/"
		switch board.upstreamKey {
		case "week":
			if page > 1 {
				path = "/rank/week/page/" + strconv.Itoa(page) + "/"
			} else {
				path = "/rank/week/"
			}
		case "month":
			if page > 1 {
				path = "/rank/month/page/" + strconv.Itoa(page) + "/"
			} else {
				path = "/rank/month/"
			}
		default:
			if page > 1 {
				path = "/rank/page/" + strconv.Itoa(page) + "/"
			}
		}
		body, err := d.fetchDuanju51Page(ctx, path)
		if err != nil {
			return rankingPage{}, err
		}
		return buildDuanju51Ranking(duanju51CardDramas(duanju51DecodeHTML(body)), board, page)
	case sourceEZDMW:
		if page != 1 {
			return rankingPage{}, errors.New("该榜单只有一页")
		}
		base := strings.TrimRight(d.providerBaseURL(sourceEZDMW), "/")
		// 不带参数的 fan_ranking.html 直接回「参数有误」；?atype=hor
		// 返回站点默认编排的动漫总榜（实测 30 条，与导航入口一致）。
		body, err := d.fetchProviderText(ctx, base+"/Index/fan_ranking.html?atype=hor", base+"/")
		if err != nil {
			return rankingPage{}, err
		}
		return parseEZDMWRanking(body, base)
	case sourceWangguo, sourceFaguo:
		// 网果 / 发果都是苹果CMS：榜单页就是一张普通列表页，
		// 用 maccmsCards 解析，名次按页面出现顺序推。
		if page != 1 {
			return rankingPage{}, errors.New("该榜单只有一页")
		}
		profile, found := maccmsProfileFor(board.Source)
		if !found {
			return rankingPage{}, fmt.Errorf("不支持的榜单站源 %s", board.Source)
		}
		base := strings.TrimRight(d.providerBaseURL(board.Source), "/")
		address := base + board.path
		body, err := d.fetchProviderText(ctx, address, base+"/")
		if err != nil {
			return rankingPage{}, err
		}
		return buildMaccmsRanking(maccmsCards(body, profile, base), board)
	case sourceJcydm:
		// 囧次元 label/hot 页面是「日漫榜 + 剧场榜」两张纯文字 paper 榜，
		// 每条带序号 / 标题 / 更新状态；封面只能从同页卡片区补（补不全），
		// 缺的等详情页或目录合并时再补。
		if page != 1 {
			return rankingPage{}, errors.New("该榜单只有一页")
		}
		base := strings.TrimRight(d.providerBaseURL(sourceJcydm), "/")
		body, err := d.fetchProviderText(ctx, base+"/index.php/label/hot.html", base+"/")
		if err != nil {
			return rankingPage{}, err
		}
		return parseJcydmRanking(body, base, board)
	default:
		return rankingPage{}, fmt.Errorf("不支持的榜单站源 %s", board.Source)
	}
}
