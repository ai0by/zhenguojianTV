// catalogdump 是 CI 用的剧集目录导出工具（0.8.19）。
//
// 它只做一件事：调 `core.NativeRequest` 把每个站源的目录翻完，按站源导出成
// `{"source": "...", "generatedAt": "...", "items": [...]}` 的 JSON。
// 加密与索引由 `scripts/build_catalog_snapshot.py` 负责，App 侧读取见
// `core/app_catalog_snapshot.go`。
//
// 用法（必须在 native 目录下执行，且带 buildAllSources=true 才能覆盖全部站源）：
//
//	go run -ldflags "-X duanjuapp/native/core.buildAllSources=true" \
//	  ./cmd/catalogdump -dir /tmp/dump -out /tmp/out -pages 6
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"duanjuapp/native/core"
)

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

type catalogPage struct {
	Items   []json.RawMessage `json:"items"`
	HasMore bool              `json:"hasMore"`
	Page    int               `json:"page"`
	Warning string            `json:"warning"`
}

type sourceDump struct {
	Source      string `json:"source"`
	GeneratedAt string `json:"generatedAt"`
	// Truncated / Pages 是**给下游的判据**，不是给人看的日志（0.8.24）。
	//
	// 以前「撞到翻页上限」只打到 stderr，产物 JSON 里一点痕迹都没有，
	// `build_catalog_snapshot.py` 于是把一份截断的 dump 当成完整快照写进
	// index.json。网果就是这么坏的：国内手工 dump 时用 `-pages 1600`，
	// 站点真实 2116 页 → 快照只有 38349 条，而实际有 50767 条。
	// 境外 runner 对网果恒 403 抓不到，`previous_entries` 又把这份截断结果
	// 永久沿用，于是**永远不会自愈**，用户看到的就是"本机比远端全"。
	//
	// 写进产物之后，下游才能在覆盖旧快照之前知道"这一份本身就不全"。
	Truncated bool              `json:"truncated,omitempty"`
	Pages     int               `json:"pages,omitempty"`
	Items     []json.RawMessage `json:"items"`
}

func call(payload map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	raw := core.NativeRequest(string(body))
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("原生核心返回无法解析: %w", err)
	}
	if !env.OK {
		if env.Error == "" {
			env.Error = "未知错误"
		}
		return nil, fmt.Errorf("%s", env.Error)
	}
	return env.Data, nil
}

func main() {
	dir := flag.String("dir", "", "工作目录（存原生缓存的临时目录，必填）")
	out := flag.String("out", "", "输出目录（每个站源一个 <source>.json）")
	pages := flag.Int("pages", 6, "每个站源最多翻多少页")
	only := flag.String("sources", "", "只导出这些站源（逗号分隔，留空表示全部）")
	// 分类枚举开关（0.8.24）。全量模式必须开（否则黄剧只拿 2000/2932、
	// 河马只拿 40 个分类）；**增量模式必须关** —— 增量只翻 3 页是为了
	// "拿最新几部剧"，而分类枚举会把请求量放大几十倍（黄剧 378 个分类 ×
	// 3 页 = 1134 请求，整轮从 42 涨到 2000+），纯属自找风控。
	enumerateCategories := flag.Bool("categories", true, "是否逐分类枚举（全量开、增量关）")
	flag.Parse()

	if *dir == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "-dir 与 -out 都必须指定")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "创建工作目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}
	if _, err := call(map[string]any{"action": "initialize", "directory": *dir}); err != nil {
		fmt.Fprintf(os.Stderr, "原生核心初始化失败: %v\n", err)
		os.Exit(1)
	}

	wanted := map[string]bool{}
	for _, item := range strings.Split(*only, ",") {
		if item = strings.TrimSpace(item); item != "" {
			wanted[item] = true
		}
	}
	sources := core.CatalogSnapshotSources()
	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "没有可用站源（是否漏了 buildAllSources=true？）")
		os.Exit(1)
	}

	failed := 0
	// 失败的站源单独记名字：CI 日志里只写"3 个站源失败"没法判断是偶发
	// 还是这几站长期拦境外 IP（花果 / 网果 / 发果就是长期 403）。
	var failedSources []string
	// 撞到翻页上限的站源也单独记（0.8.20）。以前这种事完全静默 ——
	// `-pages 60` 翻到第 60 页就直接收工，看起来"成功"，实际只是前 60 页，
	// 红果 3595 部被截成 2868 部，用户那边本机反而比远端多。
	var truncatedSources []string
	for _, source := range sources {
		if len(wanted) > 0 && !wanted[source] {
			continue
		}
		items, truncated, err := dumpSource(source, *pages, *enumerateCategories)
		if err != nil {
			// 单个站源失败不终止整体：快照少一个站源，App 侧会继续现场抓取。
			fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", source, err)
			failed++
			failedSources = append(failedSources, source)
			continue
		}
		if truncated {
			fmt.Fprintf(os.Stderr,
				"[warn] %s: 翻到第 %d 页上限还没到底（已抓 %d 条），快照不完整 —— 把 -pages 调大\n",
				source, *pages, len(items))
			truncatedSources = append(truncatedSources, source)
		}
		payload := sourceDump{
			Source:      source,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Truncated:   truncated,
			Pages:       *pages,
			Items:       items,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] %s: 序列化失败 %v\n", source, err)
			failed++
			failedSources = append(failedSources, source)
			continue
		}
		path := filepath.Join(*out, source+".json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] %s: 写文件失败 %v\n", source, err)
			failed++
			failedSources = append(failedSources, source)
			continue
		}
		fmt.Printf("[ok] %s: %d 条 -> %s\n", source, len(items), path)
	}
	if failed > 0 {
		fmt.Printf("完成，%d 个站源失败：%s\n", failed, strings.Join(failedSources, ", "))
	}
	if len(truncatedSources) > 0 {
		fmt.Printf("完成，%d 个站源被翻页上限截断：%s\n", len(truncatedSources), strings.Join(truncatedSources, ", "))
	}
}

// dumpSource 翻完一个站源的目录。
//
// 第二个返回值表示"撞到翻页上限还没到底"——数据仍然写出去（总比沿用更旧的
// 强），但调用方必须打日志，否则残缺快照会被当成完整快照发布。
//
// 第三批（0.8.24）：**除主排序外还要逐个分类再翻一遍**。
//
// 为什么必须这么做 —— 这是「手机端比远端全」的另一半原因：
//
// 黄剧的 `/dramas` 单排序最多只服务到第 100 页（要第 101 页它把 page 夹回
// 100），20 条/页 ⇒ 全站单一排序只能拿 2000 部，而它 `total` 报的是 2932、
// `adult` 一个分类自己就有 1441 部。黄豆更狠：第 98~100 页是 15 个分类
// **各 2 条**的均匀配额，100 页 × 30 条 = 3000 就是流量上限。
//
// 而 App 端浏览分类时走的是 `source|category` 这个 cacheKey，结果会
// `mergeNativeCatalog` 回 base 桶（app_catalog_cache.go:252）—— 用户随手点
// 几个分类，本机目录就比远端快照多出几百上千部。这不是客户端"抓得更全"，
// 是远端**只抓了一个维度**。
//
// 所以这里对支持分类的源，主排序之后再按分类各翻一遍取并集。
func dumpSource(source string, pages int, enumerateCategories bool) ([]json.RawMessage, bool, error) {
	seen := map[string]bool{}
	items := make([]json.RawMessage, 0, 256)
	truncated := false

	// 第一阶段：主排序（不带 category）。
	collected, more, err := dumpCatalogPages(source, "", pages, seen, items)
	if err != nil {
		return nil, false, err
	}
	items = collected
	if more {
		truncated = true
	}

	// 增量模式到此为止：它要的是"最新几部剧"，主排序前几页就是全部新剧；
	// 逐分类枚举只会把请求量放大几十倍，纯属自找风控。
	if !enumerateCategories {
		return items, truncated, nil
	}

	// 第二阶段：分类枚举。拿不到分类列表就跳过（多数 maccms 站没有）。
	categories, err := catalogCategories(source)
	if err != nil || len(categories) == 0 {
		return items, truncated, nil
	}
	before := len(items)
	for _, category := range categories {
		if category == "" {
			continue
		}
		collected, more, err := dumpCatalogPages(source, category, pages, seen, items)
		if err != nil {
			// 单个分类失败不该毁掉整站：主排序的结果已经拿到，分类只是补充。
			// 但要记下来，否则"分类没抓全"又变成静默截断。
			fmt.Fprintf(os.Stderr, "[warn] %s: 分类 %q 抓取失败（已抓到 %d 条）：%v\n",
				source, category, len(items), err)
			continue
		}
		items = collected
		if more {
			// 分类内也撞上限 —— 只影响这一维，但确实表示还有没抓到的。
			fmt.Fprintf(os.Stderr, "[warn] %s: 分类 %q 撞到第 %d 页上限\n", source, category, pages)
		}
	}
	if added := len(items) - before; added > 0 {
		fmt.Fprintf(os.Stderr, "[info] %s: 分类枚举补充了 %d 条（共 %d 个分类）\n",
			source, added, len(categories))
	}
	return items, truncated, nil
}

// dumpCatalogPages 翻完一个 source（可带 category）的分页，把新条目追加进 items。
//
// seen / items 由调用方持有并在多次调用间复用，这样"主排序 + 多个分类"天然
// 按 id 去重。返回的 more 表示"撞到 pages 上限还没到底"。
func dumpCatalogPages(source, category string, pages int, seen map[string]bool, items []json.RawMessage) ([]json.RawMessage, bool, error) {
	exhausted := false
	for page := 1; page <= pages; page++ {
		payload := map[string]any{
			"action": "catalog",
			"source": source,
			"page":   page,
			// 第一页强制刷新，后面按原生侧的分页状态走。
			"force": page == 1,
		}
		if category != "" {
			payload["category"] = category
		}
		raw, err := call(payload)
		if err != nil {
			// **翻到一半失败必须当成失败**（0.8.20）。
			//
			// 原先只要抓到过一页就把已有条目当成功返回，于是「第 1 页成功、
			// 第 2 页被 403」的花果会以 36 条（真实目录 2000+）覆盖掉上一轮
			// 的完整快照。快照残缺比稍微陈旧更糟：用户会以为这个站只剩几十部。
			// 报错后 `previous_entries` 会沿用上一轮的完整文件。
			if len(items) > 0 {
				return items, true, fmt.Errorf(
					"%s第 %d 页失败（已抓到 %d 条，视为不完整）：%w", catalogScope(category), page, len(items), err)
			}
			return items, true, err
		}
		var result catalogPage
		if err := json.Unmarshal(raw, &result); err != nil {
			return items, true, fmt.Errorf("%s第 %d 页返回无法解析：%w", catalogScope(category), page, err)
		}
		added := 0
		for _, item := range result.Items {
			key := dramaID(item)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, item)
			added++
		}
		// 没有新增或没有下一页就收工，避免在只返回同一批数据的站源上空转。
		// 这是**正常收尾**，不是失败。
		if !result.HasMore || added == 0 {
			exhausted = true
			break
		}
	}
	return items, !exhausted, nil
}

// catalogScope 用于错误信息，区分"主排序"与"某个分类"。
func catalogScope(category string) string {
	if category == "" {
		return ""
	}
	return fmt.Sprintf("分类 %q ", category)
}

// catalogCategories 取一个站源的分类列表（拿不到就返回空，调用方跳过分类枚举）。
//
// 只有部分站源能枚举分类：黄剧有 `/categories`、黄豆的 category 字段是
// 剧集自带的标签（不是可用筛选条件，实测传任何值都返回同一批数据），
// 苹果CMS 那几个没有分类接口。所以这里失败是**常态**，不能报错。
func catalogCategories(source string) ([]string, error) {
	raw, err := call(map[string]any{
		"action": "categories",
		"source": source,
		// 要**未截断**的完整分类表：UI 那条 chip 条只放得下几十个，抓目录却
		// 必须按全部分类各翻一遍，否则漏掉的分类下的剧永远进不了快照。
		"allCategories": true,
	})
	if err != nil {
		return nil, err
	}
	// 原生侧的 categories 响应是 `{"items":[{"id","name"},…]}`（见
	// app_runtime.go 的 `case "categories"`），不是裸数组。
	//
	// 而且 **catalog 的 category 参数要的是 id**（黄剧就是 slug，如 "adult"），
	// 不是显示名 —— 用 name 传进去，站点会当成不存在的分类返回空列表。
	var payload struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(payload.Items))
	for _, entry := range payload.Items {
		id := strings.TrimSpace(entry.ID)
		// 「全部」那条是 Name="全部" + 空 ID，等价于不带 category，跳过。
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func dramaID(item json.RawMessage) string {
	var probe struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(item, &probe) != nil {
		return ""
	}
	return probe.ID
}
