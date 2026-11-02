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
	Source      string            `json:"source"`
	GeneratedAt string            `json:"generatedAt"`
	Items       []json.RawMessage `json:"items"`
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
	for _, source := range sources {
		if len(wanted) > 0 && !wanted[source] {
			continue
		}
		items, err := dumpSource(source, *pages)
		if err != nil {
			// 单个站源失败不终止整体：快照少一个站源，App 侧会继续现场抓取。
			fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", source, err)
			failed++
			failedSources = append(failedSources, source)
			continue
		}
		payload := sourceDump{Source: source, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Items: items}
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
}

func dumpSource(source string, pages int) ([]json.RawMessage, error) {
	seen := map[string]bool{}
	items := make([]json.RawMessage, 0, 256)
	for page := 1; page <= pages; page++ {
		raw, err := call(map[string]any{
			"action": "catalog",
			"source": source,
			"page":   page,
			// 第一页强制刷新，后面按原生侧的分页状态走。
			"force": page == 1,
		})
		if err != nil {
			// **翻到一半失败必须当成失败**（0.8.20）。
			//
			// 原先只要抓到过一页就把已有条目当成功返回，于是「第 1 页成功、
			// 第 2 页被 403」的花果会以 36 条（真实目录 2000+）覆盖掉上一轮
			// 的完整快照。快照残缺比稍微陈旧更糟：用户会以为这个站只剩几十部。
			// 报错后 `previous_entries` 会沿用上一轮的完整文件。
			if len(items) > 0 {
				return nil, fmt.Errorf(
					"第 %d 页失败（已抓到 %d 条，视为不完整）：%w", page, len(items), err)
			}
			return nil, err
		}
		var result catalogPage
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("第 %d 页返回无法解析：%w", page, err)
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
			break
		}
	}
	return items, nil
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
