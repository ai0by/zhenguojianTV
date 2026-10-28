package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	dataDir          string
	MaxPagesPerSort  int
	PageSize         int
	Retries          int
	InsecureTLS      bool
	HuangguoAIURL    string
	HuangguoVideoURL string
	HuangdouURL      string
	HongguoURL       string
	HuangjuURL       string
	HuangjuAPIURL    string
	Token            string
	AESKeyHex        string
	InterfaceKey     string
	ParamKey         string
	ParamIV          string
	APIBase          string
	CDNURL           string
}

type Downloader struct {
	rankings               rankingCache
	cfg                    Config
	client                 *http.Client
	huangdouDetails        map[string]huangdouDetailEntry
	huangdouDetailPending  map[string]*huangdouDetailCall
	huangdouVIPProbes      map[string]huangdouVIPProbe
	providerMu             sync.Mutex
	providerHosts          map[string]string
	limiter                *requestLimiter
	proxyRouter            *proxyRouter
	hongguoOnce            sync.Once
	hongguo                *hongguoAppClient
	huangjuOnce            sync.Once
	huangju                *huangjuAPIClient
	soraniOnce             sync.Once
	sorani                 *soraniAPIClient
	diagnostics            *diagnosticLog
	apiMu                  sync.Mutex
	apiBase                string
	apiFailures            map[string]time.Time
	legacyOnce             sync.Once
	legacy                 *legacyAPIClient
	previewMu              sync.Mutex
	previewSessions        map[string]*huangguoPreviewSession
	yeguoCryptoMu          sync.Mutex
	yeguoCrypto            yeguoCryptoConfig
	yeguoCryptoExpires     time.Time
	yeguoCryptoRetryAfter  time.Time
	xiangjiaoSessionMu     sync.Mutex
	xiangjiaoDeviceID      string
	xiangjiaoAccessToken   string
	xiangjiaoAccessExpires time.Time
}

func defaultConfig() Config { return Config{MaxPagesPerSort: 1, PageSize: 30, Retries: 2} }

type nativeDrama struct {
	MetadataSchema int      `json:"metadataSchema"`
	ID             string   `json:"id"`
	Source         string   `json:"source"`
	SourceID       string   `json:"sourceId"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Cover          string   `json:"cover"`
	Episodes       int      `json:"episodes"`
	Category       string   `json:"category"`
	VIP            *bool    `json:"vip"`
	Heat           string   `json:"heat,omitempty"`
	Views          string   `json:"views,omitempty"`
	OnlineDate     string   `json:"onlineDate,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ReleaseStatus  string   `json:"releaseStatus,omitempty"`
}

type nativeInput struct {
	LAN              json.RawMessage         `json:"lan"`
	ExpectedVersions map[string]string       `json:"expectedVersions"`
	SystemProxy      nativeSystemProxy       `json:"systemProxy"`
	Settings         nativeResourceSettings  `json:"settings"`
	JobIDs           []string                `json:"jobIds"`
	Board            string                  `json:"board"`
	Entries          []nativeDownloadEpisode `json:"entries"`
	JobID            string                  `json:"jobId"`
	Command          string                  `json:"command"`
	Action           string                  `json:"action"`
	Directory        string                  `json:"directory"`
	Source           string                  `json:"source"`
	Page             int                     `json:"page"`
	Query            string                  `json:"query"`
	Category         string                  `json:"category"`
	Drama            nativeDrama             `json:"drama"`
	Chapter          Chapter                 `json:"chapter"`
	Index            int                     `json:"index"`
	Quality          int                     `json:"quality"`
	Session          string                  `json:"session"`
	Sequence         int64                   `json:"sequence"`
	Force            bool                    `json:"force"`
	// 弹幕（0.8.11）：按播放会话 + 时间窗拉取，见 app_danmaku.go。
	PlaybackSession string `json:"playbackSession"`
	StartMS         int64  `json:"startMs"`
	DurationMS      int64  `json:"durationMs"`
	// 预缓存（0.8.12）：`Entry` 是已预载好的本机播放地址，`Budget` 是
	// 本次允许写入磁盘的字节数，`Limit` 是缓存容量上限，见 app_precache.go。
	// `ReuseSession` 用来复用已预载的播放会话 —— 不能复用 `Session` 字段，
	// 那个字段被读请求的"调用方标识"占用了（`_read` 会覆盖它）。
	Entry        string `json:"entry"`
	ReuseSession string `json:"reuseSession"`
	Budget       int64  `json:"budget"`
	Limit        int64  `json:"limit"`
}

type nativeCatalogResult struct {
	Items       []nativeDrama `json:"items"`
	HasMore     bool          `json:"hasMore"`
	Page        int           `json:"page"`
	Warning     string        `json:"warning,omitempty"`
	LocalSearch bool          `json:"localSearch"`
	Fresh       bool          `json:"fresh"`
	hongguo     *hongguoCatalogState
	saveError   error
}

type nativePlan struct {
	ExpiresAt       int64             `json:"expiresAt,omitempty"`
	PrefetchedBytes int64             `json:"prefetchedBytes,omitempty"`
	Local           bool              `json:"local"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	Key             string            `json:"decryptionKey,omitempty"`
	Quality         int               `json:"quality"`
	Qualities       []int             `json:"qualities"`
	Session         string            `json:"session,omitempty"`
	RouteIndex      int               `json:"routeIndex"`
	RouteCount      int               `json:"routeCount"`
	// 弹幕（0.8.11）：只有红果能给出这个 id，其余站源为空，前端据此隐藏弹幕入口。
	DanmakuID string `json:"danmakuId,omitempty"`
}

type nativeEngine struct {
	lanMu            sync.Mutex
	lan              *nativeLANServer
	settingsMu       sync.Mutex
	settings         nativeResourceSettings
	readMu           sync.Mutex
	reads            map[string]nativeReadRequest
	readCount        int
	work             map[string]bool
	downloads        *nativeDownloads
	downloader       *Downloader
	directory        string
	mu               sync.Mutex
	catalogs         map[string][]nativeDrama
	catalogStates    map[string]nativeCatalogState
	categoryOptions  map[string][]nativeCategory
	hongguoCatalog   *hongguoCatalogState
	recommendations  map[string]nativeRecommendationState
	catalogSaveError error
	sourceSaveError  error
	covers           *nativeCoverCache
	// 播放分片的磁盘缓存（0.8.12），引擎级持有：设置页要在**没播过任何东西**
	// 的情况下也能读到占用与上限，所以不能挂在懒创建的流服务上。
	media            *nativeMediaCache
	stream           *nativeStreamServer
	playbacks        map[string]nativePlaybackChoice
	playbackMu       sync.Mutex
	playbackSequence int64
	playbackCancel   context.CancelFunc
	sourceTasks      map[string]*nativeSourceTask
	sourceRecords    map[string]nativeSourceRecord
	sourceCatalogMu  sync.Mutex
	sourceCatalogs   map[string]chan struct{}
}

var nativeState struct {
	sync.Mutex
	engine *nativeEngine
}

func nativeText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"url", "src", "cover", "image", "pic"} {
			if text := nativeText(v[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, entry := range v {
			if text := nativeText(entry); text != "" {
				return text
			}
		}
	case json.Number:
		return string(v)
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

func nativeNormalize(drama Drama) nativeDrama {
	cover := ""
	for _, value := range []any{drama.Cover, drama.CoverURL, drama.CoverURLSnake, drama.Image, drama.ImageURL, drama.ImageURLSnake, drama.Img, drama.Pic, drama.Picture, drama.Poster, drama.Thumb, drama.Thumbnail} {
		candidate := nativeText(value)
		if strings.HasPrefix(candidate, "//") {
			candidate = "https:" + candidate
		}
		if isProviderHTTPMediaURL(candidate) {
			cover = candidate
			break
		}
	}
	episodes := 0
	for _, value := range []any{drama.TotalEpisode, drama.TotalEpisodeSnake, drama.EpisodeCount, drama.EpisodeCountSnake,
		drama.ChapterCount, drama.ChapterCountSnake, drama.Total, drama.Episodes,
		drama.EpisodeNum, drama.EpisodeNumSnake, drama.TotalEpisodes, drama.TotalEpisodesSnake} {
		if count := nativeEpisodeCount(value); count > episodes {
			episodes = count
		}
	}
	source, sourceID, _ := splitProviderDramaID(drama.ID)
	return nativeDrama{MetadataSchema: 1, ID: drama.ID, Source: source, SourceID: sourceID, Title: drama.DisplayTitle(),
		Description: firstNonEmpty(drama.Desc, drama.Intro), Cover: cover, Episodes: episodes,
		Category: nativeDramaCategory(drama), VIP: drama.VIP,
		Heat: drama.Heat, Views: drama.Views, OnlineDate: providerReleaseDate(drama.OnlineDate),
		Tags: append([]string(nil), drama.Tags...), ReleaseStatus: drama.ReleaseStatus}
}

// nativeEpisodeCount 从"集数"候选字段里取集数（0.8.10 第三批）。
//
// 用户反馈"有几个源不支持共 X 集展示"。排查下来这些源并不是没给集数，
// 而是给的不是纯数字：`更新至80集`、`共 80 集`、`全80话`、`80集全`、
// `80`（字符串）、`80.0`（浮点）都有。原来一律 `strconv.Atoi`，
// 只要带一个汉字就归零，卡片上于是没有"共 X 集"。
//
// 这里先按纯数字解析，失败再取**第一段连续数字**；完全没有数字时返回 0。
// 只对集数候选字段调用，不会把标题里的年份误当集数。
func nativeEpisodeCount(value any) int {
	text := strings.TrimSpace(nativeText(value))
	if text == "" {
		return 0
	}
	if count, err := strconv.Atoi(text); err == nil {
		return max(count, 0)
	}
	start := -1
	for index := 0; index < len(text); index++ {
		digit := text[index] >= '0' && text[index] <= '9'
		if digit {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			if count, err := strconv.Atoi(text[start:index]); err == nil && count > 0 {
				return count
			}
			start = -1
		}
	}
	if start >= 0 {
		if count, err := strconv.Atoi(text[start:]); err == nil && count > 0 {
			return count
		}
	}
	return 0
}

func nativeDramaCategory(drama Drama) string {
	category := firstNonEmpty(drama.CategoryName, drama.CategoryNameSnake, drama.TypeName, drama.TypeNameSnake, drama.SortName, drama.SortNameSnake, drama.Category)
	if category != "" {
		return category
	}
	switch drama.ChannelName {
	case "真人剧", "短剧", "漫剧", "AI 剧", "AI剧", "AI 短剧", "AI 漫剧", "AI 换脸", "AI 魔改":
		return drama.ChannelName
	}
	return ""
}

func newNativeEngine(directory string) (*nativeEngine, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("应用数据目录无效")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	cfg.dataDir = directory
	router := &proxyRouter{}
	d := &Downloader{cfg: cfg, providerHosts: map[string]string{}, proxyRouter: router,
		limiter: newRequestLimiter(3, 250*time.Millisecond), diagnostics: newDiagnosticLog(directory)}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 20 * time.Second
	// 0.8.10：显式给 DialContext，才能在"这条链路 IPv6 被 RST"时按请求退回 IPv4
	// （香蕉站源的典型症状）。不设置时上下文拿不到，没法按请求区分。
	transport.DialContext = newForceableDialer()
	transport.Proxy = router.proxy
	cdn := newCDNTransport(transport, newDNSResolver(transport))
	d.client = &http.Client{Transport: newHuangguoBrowserTransport(cdn, d), Timeout: 45 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("站源重定向次数过多")
			}
			if len(via) > 0 && !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
				request.Header.Del("X-Preview-Token")
			}
			return nil
		}}
	engine := &nativeEngine{downloader: d, directory: directory, catalogs: map[string][]nativeDrama{}, catalogStates: map[string]nativeCatalogState{}}
	engine.loadResourceSettings()
	d.loadRankingCache()
	engine.loadCatalogCache()
	engine.loadSourceRecords()
	engine.covers = newNativeCoverCache(directory, d)
	engine.media = newNativeMediaCache(directory)
	engine.downloads = newNativeDownloads(engine)
	return engine, nil
}

// mediaCache 取引擎级的分片磁盘缓存，没初始化时按需补上
// （单测里会直接构造 `nativeEngine`，不走 `newNativeEngine`）。
func (engine *nativeEngine) mediaCache() *nativeMediaCache {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.media == nil {
		engine.media = newNativeMediaCache(engine.directory)
	}
	return engine.media
}

func NativeRequest(raw string) (result string) {
	defer func() {
		if recover() != nil {
			result = `{"ok":false,"error":"本地核心处理失败，请重试"}`
		}
	}()
	var input nativeInput
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &input) != nil {
		return `{"ok":false,"error":"请求格式无效"}`
	}
	data, err := nativeDispatch(input)
	envelope := map[string]any{"ok": err == nil}
	if err != nil {
		envelope["error"] = publicError(err).Error()
		if errors.Is(err, errNativeLocalFile) {
			envelope["code"] = "local_media"
		}
		var backoff *requestBackoff
		if errors.As(err, &backoff) {
			envelope["code"] = "source_blocked"
			envelope["retryAt"] = backoff.until
		}
		source := canonicalProviderSource(input.Source)
		if source == "" {
			source = sourceFromDramaID(input.Drama.ID)
		}
		if !errors.Is(err, context.Canceled) && (input.Action == "catalog" || input.Action == "detail" || input.Action == "resolve") {
			nativeState.Lock()
			engine := nativeState.engine
			nativeState.Unlock()
			if engine != nil && nativeSourceAvailable(source) {
				engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
					record.Error = publicError(err).Error()
					if backoff != nil {
						record.RetryAt = backoff.until
					}
					if !record.Running {
						record.Stage, record.FinishedAt = "访问失败", time.Now()
					}
				})
			}
		}
	} else {
		envelope["data"] = data
	}
	body, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return `{"ok":false,"error":"无法读取站源返回的数据"}`
	}
	return string(body)
}

func nativeDispatch(input nativeInput) (any, error) {
	if err := nativeAuthorizeInput(input); err != nil {
		return nil, err
	}
	nativeState.Lock()
	if input.Action == "initialize" {
		if nativeState.engine == nil {
			engine, err := newNativeEngine(input.Directory)
			if err != nil {
				nativeState.Unlock()
				return nil, err
			}
			nativeState.engine = engine
		}
		nativeState.Unlock()
		return map[string]any{"version": "0.2.16", "standalone": true, "allSources": buildAllSources == "true"}, nil
	}
	engine := nativeState.engine
	nativeState.Unlock()
	if engine == nil {
		return nil, errors.New("应用核心尚未就绪，请重新打开应用")
	}
	duration := 60 * time.Second
	if input.Action == "moveDownloads" {
		duration = 10 * time.Minute
	} else if input.Action == "preload" {
		duration = 15 * time.Second
	} else if input.Action == "precache" {
		// 整集预缓存要下几十 MB，给足时间；用户切走时由 `cancelPrecache` 中断。
		duration = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if input.Action == "preload" || input.Action == "precache" || input.Action == "prepareHandoff" || input.Session != "" && (input.Action == "catalog" || input.Action == "categories" || input.Action == "suggestions" || input.Action == "recommendations" || input.Action == "metadata") {
		work, finish, err := engine.beginRead(ctx, input)
		if err != nil {
			return nil, err
		}
		defer finish()
		ctx = work
	}
	switch input.Action {
	case "lan":
		return engine.nativeLAN(ctx, input.Command, input.LAN)
	case "updateSystemProxy":
		return true, engine.updateSystemProxy(input.SystemProxy)
	case "resourceSettings":
		return engine.resourceSettings(), nil
	case "saveResourceSettings":
		return engine.saveResourceSettings(input.Settings)
	case "controlDownloadBatch":
		return engine.downloads.controlBatchExpected(ctx, input.JobIDs, input.Command, input.ExpectedVersions)
	case "preload":
		return engine.nativePreload(ctx, input)
	case "precache":
		// 0.8.12：把整集分片落到磁盘，见 app_precache.go。
		return engine.nativePrecache(ctx, input)
	case "mediaCacheStats":
		return engine.nativeMediaCacheStats(), nil
	case "clearMediaCache":
		return engine.nativeClearMediaCache(), nil
	case "setMediaCacheLimit":
		return engine.nativeSetMediaCacheLimit(input.Limit), nil
	case "prepareHandoff":
		return engine.nativeResolve(ctx, input)
	case "cancelRead":
		engine.cancelRead(input)
		return true, nil
	case "recommendations":
		return engine.nativeRecommendations(ctx, input)
	case "cachedRecommendations":
		return engine.cachedRecommendations(input.Category)
	case "rankingBoards":
		boards := []rankingBoard{}
		for _, board := range rankingBoards {
			if nativeSourceAvailable(board.Source) {
				boards = append(boards, board)
			}
		}
		return map[string]any{"items": boards}, nil
	case "rankings":
		return engine.nativeRanking(ctx, input)
	case "suggestions":
		items, err := engine.suggestions(ctx, input.Query)
		return map[string]any{"items": items}, err
	case "downloadDirectory":
		engine.downloads.mu.Lock()
		root := engine.downloads.root
		engine.downloads.mu.Unlock()
		return map[string]string{"directory": root}, nil
	case "storage":
		return engine.storage()
	case "moveDownloads":
		return true, engine.moveDownloads(ctx, input.Directory)
	case "workLease":
		count, err := engine.workLease(input.JobID, input.Command)
		return map[string]int{"count": count}, err
	case "downloads":
		jobs, err := engine.downloads.snapshot()
		return map[string]any{"jobs": jobs}, err
	case "enqueueDownloads":
		added, err := engine.downloads.enqueueContext(ctx, input)
		return map[string]int{"added": added}, err
	case "controlDownloads":
		return true, engine.downloads.control(input.JobID, input.Command)
	case "localPlayback":
		plan, _, err := engine.downloads.localPlan(input.Drama.ID, input.Index)
		return plan, err
	case "catalog":
		return engine.nativeCatalog(ctx, input)
	case "cached":
		if !validNativeCategory(canonicalProviderSource(input.Source), input.Category) {
			return nil, errors.New("内容分类无效")
		}
		return engine.nativeCached(nativeCatalogKey(input.Source, input.Category)), nil
	case "categories":
		items, err := engine.nativeCategories(ctx, input.Source, input.Force)
		return map[string]any{"items": items}, err
	case "sourceStatus":
		return engine.sourceStatus(input.Source), nil
	case "sourceJob":
		return engine.startSourceTask(input.Source, input.Command, input.Drama)
	case "cancelSourceJob":
		return engine.cancelSourceTask(input.Source), nil
	case "cover":
		return engine.loadCover(ctx, input.Drama, input.Force)
	case "prepareCover":
		return engine.prepareCover(ctx, input.Drama)
	case "detail":
		return engine.nativeDetail(ctx, input.Drama)
	case "metadata":
		return engine.nativeMetadata(ctx, input.Drama)
	case "resolve", "fallback":
		playback, finish, err := engine.nativeBeginPlayback(ctx, input.Sequence)
		if err != nil {
			return nil, err
		}
		defer finish()
		if input.Action == "fallback" {
			return engine.nativeNextPlayback(playback, input.Session)
		}
		return engine.nativeResolve(playback, input)
	case "cancelPlayback":
		engine.nativeCancelPlayback(input.Sequence)
		return true, nil
	case "release":
		engine.nativeReleasePlayback(input.Session)
		return true, nil
	case "danmaku":
		// 0.8.11：按播放会话 + 时间窗拉红果弹幕，见 app_danmaku.go。
		return engine.nativeDanmaku(ctx, input)
	default:
		return nil, errors.New("不支持的应用操作")
	}
}

// nativeCatalog 是目录拉取的统一入口，负责处理 host 级退避（0.8.10）。
//
// 用户反馈：红果源偶发一直转圈不可播放，手机下拉刷新一直提示"查找新剧"
// 但永远不成功，去设置里点一次「检查链接」就好了。
//
// 根因是限流器：请求被 Cloudflare / 403 拦过一次后，会给该 host 挂上
// 最长 30 分钟的退避，此后所有普通请求**在 acquire 阶段就直接失败**，
// 客户端看到的只有一个不会结束的转圈。「检查链接」能救回来，是因为它带
// Force 并重新竞速镜像，天然绕开了被退避的 host。
//
// 现在三件事保证这种状态不会长期存在：
//  1. Force（下拉刷新 / 重试 / 检查链接）先清退避，等价于"重新试一次"；
//  2. 普通请求失败后**自动清退避并重试一次**，不必等用户去设置里点检查；
//  3. 重试只做一次，站源真挂了也不会把请求量翻倍。
//
// 第 2 条是 0.8.10 第二批反馈补的：用户澄清"红果不是永远失败，只是有时候
// 会失败，失败后一直卡着"。只对退避类失败重试覆盖不到真正偶发的超时 /
// 连接重置，而那正是"有时候"的主要来源 —— 「检查链接」之所以能救，
// 本质上就是"清退避 + 重新走一遍"，这里把同样的动作补到普通目录加载上。
func (engine *nativeEngine) nativeCatalog(ctx context.Context, input nativeInput) (nativeCatalogResult, error) {
	limiter := engine.downloader.limiter
	if input.Force && limiter != nil {
		limiter.clearBackoffs()
	}
	result, err := engine.nativeCatalogOnce(ctx, input)
	if err == nil {
		return result, nil
	}
	// 用户已经离开 / 上下文到期时不再重试，避免白白多打一轮请求。
	if limiter == nil || ctx.Err() != nil {
		return result, err
	}
	limiter.clearBackoffs()
	return engine.nativeCatalogOnce(ctx, input)
}

func (engine *nativeEngine) nativeCatalogOnce(ctx context.Context, input nativeInput) (nativeCatalogResult, error) {
	source := canonicalProviderSource(input.Source)
	if !isHuangguoProviderSource(source) {
		return nativeCatalogResult{}, errors.New("请选择有效站源")
	}
	category := strings.TrimSpace(input.Category)
	if !validNativeCategory(source, category) {
		return nativeCatalogResult{}, errors.New("内容分类无效")
	}
	cacheKey := nativeCatalogKey(source, category)
	if err := ctx.Err(); err != nil {
		return nativeCatalogResult{}, err
	}
	page := max(1, input.Page)
	query := strings.TrimSpace(input.Query)
	if query == "" {
		if retried, err := engine.retryCatalogSave(); retried {
			cached := engine.nativeCached(cacheKey)
			if err != nil || len(cached.Items) > 0 {
				return cached, nil
			}
		}
	}
	if query == "" && page == 1 && !input.Force {
		if cached := engine.nativeCached(cacheKey); cached.Fresh {
			return cached, nil
		}
	}
	d := engine.downloader
	result := nativeCatalogResult{Items: []nativeDrama{}, Page: page}
	if query != "" && source == sourceHongguo {
		entry, err := d.searchHongguoDramas(ctx, query)
		if err != nil {
			return result, err
		}
		for _, drama := range entry.Dramas {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.Warning, result.Page = entry.Warning, 1
		if entry.Limited && result.Warning == "" {
			result.Warning = "已显示当前可获取的匹配结果，使用更完整的剧名可继续查找"
		}
		return result, nil
	}
	if query != "" && source == sourceHuangju {
		items, more, err := d.fetchHuangjuCatalogPage(ctx, page, "", query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" && source == sourceDuanju51 {
		items, more, err := d.fetchDuanju51CatalogPage(ctx, page, "", query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" && source == sourceYeguo {
		items, more, err := d.fetchYeguoCatalogPage(ctx, page, query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" && source == sourceXiangjiao {
		items, more, err := d.fetchXiangjiaoCatalogPage(ctx, page, query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" && source == sourceEZDMW {
		items, more, err := d.fetchEZDMWCatalogPage(ctx, page, "", query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	// 青空次元（0.8.15）：搜索走同一个目录接口，只是换路由与参数。
	if query != "" && source == sourceSorani {
		items, more, err := d.fetchSoraniCatalogPage(ctx, page, "", query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" {
		if profile, found := maccmsProfileFor(source); found && profile.SearchURL != nil {
			items, more, err := d.fetchMaccmsCatalogPage(ctx, source, page, query)
			if err != nil {
				return result, err
			}
			for _, drama := range items {
				result.Items = append(result.Items, nativeNormalize(drama))
			}
			result.HasMore = more
			return result, nil
		}
	}
	if query != "" {
		engine.mu.Lock()
		items := append([]nativeDrama{}, engine.catalogs[source]...)
		engine.mu.Unlock()
		for _, item := range items {
			text := hongguoSearchText(item.Title + " " + item.Description + " " + strings.Join(item.Tags, " "))
			matches := true
			for _, word := range strings.Fields(query) {
				matches = matches && strings.Contains(text, hongguoSearchText(word))
			}
			if matches {
				result.Items = append(result.Items, item)
			}
		}
		result.LocalSearch = true
		return result, nil
	}
	unlock, lockErr := engine.lockSourceCatalog(ctx, source)
	if lockErr != nil {
		return nativeCatalogResult{}, lockErr
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var items []Drama
	var err error
	switch source {
	case sourceHongguo:
		engine.mu.Lock()
		known := make(map[string]bool, len(engine.catalogs[cacheKey]))
		for _, drama := range engine.catalogs[cacheKey] {
			known[drama.ID] = true
		}
		engine.mu.Unlock()
		ctx = context.WithValue(ctx, libraryKnownKey{}, known)
		if page > 1 {
			ctx = context.WithValue(ctx, libraryMoreKey{}, true)
		}
		items, err = d.fetchHongguoAppCatalogCategory(ctx, category)
		state := d.hongguoCatalogSnapshot()
		result.hongguo = state
		result.HasMore = hongguoCatalogHasMore(state)
		if category != "" && state != nil {
			result.HasMore = !state.Feeds["category:"+category].Exhausted
		}
		if len(items) == 0 && err != nil && ctx.Err() == nil && (category == "" || category == "short_play") {
			var totalPages int
			items, totalPages, err = d.fetchHongguoCategoryPage(ctx, "real-drama?page="+strconv.Itoa(page), "真人剧")
			result.HasMore = page < totalPages
		}
	case sourceHuangdou:
		client := newHuangdouAPIClient(d)
		var decoded any
		err = client.call(ctx, "/drama/list", map[string]any{"page": strconv.Itoa(page), "page_size": "30"}, &decoded)
		if err == nil {
			rows := huangdouList(decoded)
			for _, row := range rows {
				if drama := huangdouDramaFromMap(row); drama.ID != "" {
					items = append(items, drama)
				}
			}
			result.HasMore = len(rows) >= 30
		}
	case sourceHuangguoAI:
		items, result.HasMore, err = d.fetchHuangguoAICatalogPage(ctx, page, category)
	case sourceHuangju:
		items, result.HasMore, err = d.fetchHuangjuCatalogPage(ctx, page, category, "")
	case sourceDuanju51:
		items, result.HasMore, err = d.fetchDuanju51CatalogPage(ctx, page, category, "")
	case sourceYeguo:
		items, result.HasMore, err = d.fetchYeguoCatalogPage(ctx, page, "")
	case sourceXiangjiao:
		items, result.HasMore, err = d.fetchXiangjiaoCatalogPage(ctx, page, "")
	case sourceEZDMW:
		items, result.HasMore, err = d.fetchEZDMWCatalogPage(ctx, page, category, "")
	case sourceSorani:
		items, result.HasMore, err = d.fetchSoraniCatalogPage(ctx, page, category, "")
	case sourceHuaguo, sourceWangguo, sourceFaguo, sourceWuguo:
		items, result.HasMore, err = d.fetchMaccmsCatalogPage(ctx, source, page, "")
	case sourceHuangguoVideo:
		address := fmt.Sprintf("%s/videos?page=%d", d.providerBaseURL(source), page)
		if category != "" {
			address += "&category=" + url.QueryEscape(category)
		}
		var body string
		body, err = d.fetchProviderText(ctx, address, d.providerBaseURL(source)+"/")
		if err == nil {
			items = parseHuangguoVideoCards(body, address)
		}
		result.HasMore = len(items) >= 20
	case sourceCloudFront:
		items, result.HasMore, err = d.fetchLegacyCatalogCategoryPage(ctx, page, category)
	}
	if err != nil && len(items) == 0 {
		return result, err
	}
	if len(items) == 0 && page == 1 && source != sourceHuangju {
		return result, errors.New("站源暂未返回剧集，请稍后刷新")
	}
	if err != nil {
		result.Warning = publicError(err).Error()
	}
	seen := map[string]bool{}
	for _, drama := range items {
		if drama.ID == "" || seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		result.Items = append(result.Items, nativeNormalize(drama))
	}
	engine.saveCatalogCache(cacheKey, &result)
	return result, nil
}

func (engine *nativeEngine) nativeDetail(ctx context.Context, drama nativeDrama) (any, error) {
	source, sourceID, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return nil, errors.New("剧集信息无效，请刷新剧库")
	}
	var title string
	var raw Drama
	var chapters []Chapter
	var err error
	switch source {
	case sourceHuangguoAI:
		raw, chapters, err = engine.downloader.fetchHuangguoAIDetail(ctx, sourceID)
	case sourceHuangguoVideo:
		raw, chapters, err = engine.downloader.fetchHuangguoVideoDetail(ctx, sourceID)
	case sourceCloudFront:
		raw, chapters, err = engine.downloader.fetchLegacyDetail(ctx, sourceID)
	case sourceHuangju:
		raw, chapters, err = engine.downloader.fetchHuangjuDetail(ctx, sourceID)
	case sourceDuanju51:
		raw, chapters, err = engine.downloader.fetchDuanju51Detail(ctx, sourceID)
	case sourceYeguo:
		raw, chapters, err = engine.downloader.fetchYeguoDetail(ctx, sourceID)
	case sourceXiangjiao:
		raw, chapters, err = engine.downloader.fetchXiangjiaoDetail(ctx, sourceID)
	case sourceEZDMW:
		raw, chapters, err = engine.downloader.fetchEZDMWDetail(ctx, sourceID)
	case sourceSorani:
		raw, chapters, err = engine.downloader.fetchSoraniDetail(ctx, sourceID)
	case sourceHuaguo, sourceWangguo, sourceFaguo, sourceWuguo:
		raw, chapters, err = engine.downloader.fetchMaccmsDetail(ctx, source, sourceID)
	default:
		title, chapters, err = engine.downloader.GetHuangguoChapters(ctx, source, sourceID)
	}
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, errors.New("该剧暂时没有可播放的分集")
	}
	if title != "" && title != "短剧" {
		drama.Title = title
	}
	if raw.ID == drama.ID {
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
	}
	if source == sourceHongguo {
		raw := engine.downloader.hongguoCachedDrama(Drama{ID: drama.ID, Title: drama.Title, Source: source})
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
	}
	if source == sourceHuangdou {
		if row, err := engine.downloader.huangdouDetail(ctx, sourceID); err == nil {
			// 详情接口对试看剧不带 VIP 标记；若刚才的探询已有结论，
			// 用它回写 Drama.VIP，否则界面会一直显示“VIP 状态待补齐”。
			var vipOverride *bool
			if known, hasVIP := engine.downloader.huangdouVIPProbeResult(sourceID); known {
				vipOverride = &hasVIP
			}
			fresh := nativeNormalize(huangdouDramaFromMapVIP(row, vipOverride))
			if fresh.ID == drama.ID {
				drama = mergeNativeDrama(drama, fresh)
			}
		}
	}
	drama.Source, drama.SourceID, drama.Episodes = source, sourceID, len(chapters)
	if source == sourceHuangju {
		drama.Episodes = max(drama.Episodes, nativeNormalize(raw).Episodes)
	}
	warning := ""
	if err := engine.saveDetailMetadata(drama); err != nil {
		warning = err.Error()
	}
	return map[string]any{"drama": drama, "chapters": chapters, "warning": warning}, nil
}

func (engine *nativeEngine) nativeResolve(ctx context.Context, input nativeInput) (nativePlan, error) {
	if _, _, valid := splitProviderDramaID(input.Drama.ID); !valid {
		return nativePlan{}, errors.New("剧集信息无效")
	}
	if !input.Force && engine.downloads != nil {
		if plan, found, err := engine.downloads.localPlan(input.Drama.ID, input.Index); found || err != nil {
			if input.Action != "prepareHandoff" || !errors.Is(err, errNativeLocalFile) {
				return plan, err
			}
		}
	}
	task := Task{DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Chapter: input.Chapter, Index: input.Index}
	media, err := engine.downloader.resolveProviderMedia(ctx, task)
	// 0.8.10：被 host 退避挡住时自动重试一次（与 `nativeCatalog` 同一套理由）。
	// 否则一次 403 就会让整个站源的播放"一直转圈"，只能靠手动检查链接恢复。
	var backoff *requestBackoff
	if err != nil && errors.As(err, &backoff) && engine.downloader.limiter != nil && ctx.Err() == nil {
		engine.downloader.limiter.clearBackoffs()
		media, err = engine.downloader.resolveProviderMedia(ctx, task)
	}
	if err != nil {
		return nativePlan{}, err
	}
	choice := nativePlaybackChoices(media, input.Quality)
	// 弹幕（0.8.11）：只有红果的播放地址带 `hongguo-cenc://<videoId>`，
	// 能从中取出剧集 id / 视频 id；其余站源留空，前端据此隐藏弹幕开关。
	if seriesID, videoID, ok := hongguoPlaybackIDs(task); ok {
		choice.danmakuSeries, choice.danmakuVideo = seriesID, videoID
	}
	return engine.nativeOpenPlayback(ctx, choice)
}
