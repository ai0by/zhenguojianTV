package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"sync/atomic"
	"testing"
)

// catalogSnapshotSealForTest 用与线上同一把密钥加密一段明文，
// 保证「Python 加密 / Go 解密」这条链路在单测里也能覆盖到。
func catalogSnapshotSealForTest(t *testing.T, plain []byte) []byte {
	t.Helper()
	key := catalogSnapshotKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	for index := range nonce {
		nonce[index] = byte(index + 1)
	}
	sealed := make([]byte, 0, len(nonce)+len(plain)+aead.Overhead())
	sealed = append(sealed, nonce...)
	return aead.Seal(sealed, nonce, plain, nil)
}

// catalogSnapshotTransport 按文件名回放快照内容（jsDelivr 与加速站的路径
// 前缀不同，取 basename 匹配即可）。
func catalogSnapshotTransport(files map[string][]byte, hits *int32) sourceFixtureTransport {
	return func(request *http.Request) (*http.Response, error) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		body, ok := files[path.Base(request.URL.Path)]
		if !ok {
			return sourceFixtureResponse(request, http.StatusNotFound, "missing"), nil
		}
		return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
	}
}

func catalogSnapshotIndexForTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	return catalogSnapshotIndexForSourceForTest(t, sourceHongguo, payload)
}

func catalogSnapshotIndexForSourceForTest(t *testing.T, source string, payload []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	index, err := json.Marshal(map[string]any{
		"version":     1,
		"generatedAt": "2026-11-02T10:00:00Z",
		"algorithm":   "aes-256-gcm",
		"sources": map[string]any{
			source: map[string]any{
				"file":   "sources/" + source + ".json.enc",
				"sha256": hex.EncodeToString(sum[:]),
				"count":  2,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func catalogSnapshotPayloadForTest(t *testing.T, items []nativeDrama) []byte {
	t.Helper()
	return catalogSnapshotPayloadForSourceForTest(t, sourceHongguo, items)
}

func catalogSnapshotPayloadForSourceForTest(t *testing.T, source string, items []nativeDrama) []byte {
	t.Helper()
	plain, err := json.Marshal(catalogSnapshotPayload{
		Source:      source,
		GeneratedAt: "2026-11-02T10:00:00Z",
		Items:       items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalogSnapshotSealForTest(t, plain)
}

func TestDecryptCatalogSnapshotRoundTrip(t *testing.T) {
	plain := []byte(`{"source":"hongguo","items":[]}`)
	sealed := catalogSnapshotSealForTest(t, plain)
	got, err := decryptCatalogSnapshot(sealed)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("明文不一致：%q", got)
	}
	if _, err := decryptCatalogSnapshot(sealed[:10]); err == nil {
		t.Fatal("nonce 不完整的密文应该报错")
	}
	// 篡改一个字节：GCM 的 tag 校验必须拦住。
	broken := append([]byte{}, sealed...)
	broken[len(broken)-1] ^= 0xff
	if _, err := decryptCatalogSnapshot(broken); err == nil {
		t.Fatal("被篡改的密文应该解密失败")
	}
}

func TestNativeSyncCatalogSnapshotSeedsCatalog(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
		{ID: sourceHongguo + ":b", Source: sourceHongguo, Title: "快照剧 B"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	var hits int32
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, &hits))

	changed, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	if !changed {
		t.Fatal("首次同步应该返回 changed=true")
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 2 || items[0].Title != "快照剧 A" {
		t.Fatalf("快照没有灌进剧库：%+v", items)
	}
	// 视为刚更新过，首页应该直接吃缓存。
	if engine.catalogStates[sourceHongguo].UpdatedAt.IsZero() {
		t.Fatal("灌完快照后应标记为刚刚更新")
	}
	// 状态落盘，重开引擎不会重复下载。
	state := engine.loadCatalogSnapshotState()
	if state.GeneratedAt != "2026-11-02T10:00:00Z" {
		t.Fatalf("状态没有落盘：%+v", state)
	}

	// 同一版快照再同步一次：直接命中状态，一个请求都不该发。
	before := atomic.LoadInt32(&hits)
	changed, err = engine.nativeSyncCatalogSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("二次同步失败：%v", err)
	}
	if changed {
		t.Fatal("同一版快照不该重复灌库")
	}
	if atomic.LoadInt32(&hits) != before+1 {
		t.Fatalf("同一版快照只该发一次索引请求，实际发了 %d 次", atomic.LoadInt32(&hits)-before)
	}
}

func TestNativeSyncCatalogSnapshotKeepsLocalDetail(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	// 本地已经有详情页补过的封面与简介。
	engine.catalogs[sourceHongguo] = []nativeDrama{{
		ID:          sourceHongguo + ":a",
		Source:      sourceHongguo,
		Title:       "快照剧 A",
		Cover:       "https://example.invalid/a.jpg",
		Description: "本地详情",
		Episodes:    80,
	}}

	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 1 {
		t.Fatalf("合并后不该出现重复条目：%+v", items)
	}
	if items[0].Cover == "" || items[0].Description != "本地详情" || items[0].Episodes != 80 {
		t.Fatalf("本地补齐的字段被快照覆盖了：%+v", items[0])
	}
}

func TestNativeSyncCatalogSnapshotRejectsBadChecksum(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
	})
	index, err := json.Marshal(map[string]any{
		"version":     1,
		"generatedAt": "2026-11-02T11:00:00Z",
		"algorithm":   "aes-256-gcm",
		"sources": map[string]any{
			sourceHongguo: map[string]any{
				"file":   "sources/" + sourceHongguo + ".json.enc",
				"sha256": "00" + hex.EncodeToString(make([]byte, 31)),
				"count":  1,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"index.json":                index,
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))

	changed, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("校验失败不该冒泡成错误：%v", err)
	}
	if changed {
		t.Fatal("sha256 不符时不该灌库")
	}
	if len(engine.catalogs[sourceHongguo]) != 0 {
		t.Fatal("sha256 不符时剧库必须保持原样")
	}
}

func TestNativeSyncCatalogSnapshotToleratesMissingIndex(t *testing.T) {
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(map[string][]byte{}, nil))
	changed, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil)
	if err == nil {
		t.Fatal("索引拉不到应该返回错误，交给调用方静默处理")
	}
	if changed {
		t.Fatal("索引拉不到时不该声称灌库成功")
	}
}

func TestNativeSyncCatalogSnapshotHonorsAllowedSources(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	var hits int32
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, &hits))

	// 用户只开了红果以外的站源：这份构建里红果是唯一可用源，
	// 交不出交集就该一个字节都不下（省流量的意义就在这里）。
	changed, err := engine.nativeSyncCatalogSnapshot(context.Background(), []string{"yeguo"})
	if err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	if changed {
		t.Fatal("启用的站源与快照没有交集时不该灌库")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("只该拉一次索引，实际发了 %d 次请求", n)
	}
	if len(engine.catalogs[sourceHongguo]) != 0 {
		t.Fatal("没被启用的站源不该进剧库")
	}

	// 用户把红果打开：这次才真灌。
	changed, err = engine.nativeSyncCatalogSnapshot(context.Background(), []string{"hongguo"})
	if err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	if !changed || len(engine.catalogs[sourceHongguo]) != 1 {
		t.Fatalf("启用红果后应该灌进快照：changed=%v items=%+v", changed, engine.catalogs[sourceHongguo])
	}
}

// 快照文件是按站源分的，条目自己还带 source 字段和 `source:id` 前缀。
// 三者串了（文件被手工拼错、index 文件名写错）时，**不能**把别的站源的剧
// 塞进这个桶 —— 桶决定点开时用哪个 provider 解析播放地址，塞错就播不出来。
func TestNativeSyncCatalogSnapshotSkipsItemsFromAnotherSource(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "本源的剧"},
		// id 前缀属于野果，source 字段也写着野果 —— 混进来的脏数据。
		{ID: "yeguo:1", Source: "yeguo", Title: "串进来的剧"},
		// id 是本源的，但 source 字段写成别的站源。
		{ID: sourceHongguo + ":b", Source: "yeguo", Title: "source 写错的剧"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 1 || items[0].ID != sourceHongguo+":a" {
		t.Fatalf("跨站源的条目应该被跳过：%+v", items)
	}
	if warning := engine.catalogStates[sourceHongguo].Warning; warning == "" {
		t.Fatal("跳过条目后应该留下警告，否则用户只看到\"少了几部\"")
	}
}

// `站源:` 这种只有前缀没有尾巴的 id、以及根本没有前缀的裸 id，既解析不出
// provider（点开黑屏），也没有稳定的去重键 —— 一律不许进库。
func TestNativeSyncCatalogSnapshotSkipsMalformedIDs(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "正常的剧"},
		{ID: sourceHongguo + ":", Source: sourceHongguo, Title: "只有前缀没有尾巴"},
		{ID: sourceHongguo + ":   ", Source: sourceHongguo, Title: "尾巴全是空格"},
		{ID: "no-prefix", Source: sourceHongguo, Title: "裸 id"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 1 || items[0].ID != sourceHongguo+":a" {
		t.Fatalf("畸形 id 应该被跳过：%+v", items)
	}
	if warning := engine.catalogStates[sourceHongguo].Warning; warning == "" {
		t.Fatal("跳过畸形条目后应该留下警告")
	}
}

// 别名（`huangguoduanju.com` → `hongguo`）和空 source 都要归一成本桶的站源，
// 否则同一部剧会因为写法不同在桶里留下两份、点开时又找不到 provider。
func TestNativeSyncCatalogSnapshotNormalizesItemSource(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Title: "source 为空的剧"},
		{ID: sourceHongguo + ":b", Source: "hongguoduanju.com", Title: "用别名的剧"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 2 {
		t.Fatalf("别名不该被当成别的站源丢掉：%+v", items)
	}
	for _, item := range items {
		if item.Source != sourceHongguo {
			t.Fatalf("source 没有归一：%+v", item)
		}
	}
	if warning := engine.catalogStates[sourceHongguo].Warning; warning != "" {
		t.Fatalf("别名不该触发警告：%s", warning)
	}
}

// 索引里写的是别名（`huangguo`）、桶用的是规范名（`huangguoai`）时，
// 条目 id 的前缀必须跟着改写成桶的写法，否则同一部剧会和现场抓到的
// `huangguoai:123` 在同一个桶里各留一份 —— 按 id 去重根本挡不住。
func TestNativeSyncCatalogSnapshotRewritesAliasIDPrefix(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previous }()

	payload := catalogSnapshotPayloadForSourceForTest(t, "huangguo", []nativeDrama{
		{ID: "huangguo:123", Source: "huangguo", SourceID: "123", Title: "别名前缀的剧"},
	})
	files := map[string][]byte{
		// 索引 key 故意写成别名，文件也按别名命名。
		"index.json":        catalogSnapshotIndexForSourceForTest(t, "huangguo", payload),
		"huangguo.json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), []string{sourceHuangguoAI}); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHuangguoAI]
	if len(items) != 1 {
		t.Fatalf("别名站源应该落进规范名的桶：%+v", engine.catalogs)
	}
	if items[0].ID != sourceHuangguoAI+":123" || items[0].Source != sourceHuangguoAI {
		t.Fatalf("id 前缀 / source 没有归一：%+v", items[0])
	}
	// 归一之后，本地再抓到同一部剧（规范名 id）就能正常合并成一条。
	engine.catalogs[sourceHuangguoAI] = mergeNativeCatalog(items,
		[]nativeDrama{{ID: sourceHuangguoAI + ":123", Source: sourceHuangguoAI, Cover: "https://example.invalid/x.jpg"}})
	if len(engine.catalogs[sourceHuangguoAI]) != 1 {
		t.Fatalf("归一后仍出现重复：%+v", engine.catalogs[sourceHuangguoAI])
	}
}

func TestCoveredBy(t *testing.T) {
	if !coveredBy(nil, nil) {
		t.Fatal("空需求应该视为已覆盖")
	}
	if coveredBy([]string{"hongguo"}, []string{"hongguo", "yeguo"}) {
		t.Fatal("缺一个站源就不算覆盖")
	}
	if !coveredBy([]string{"yeguo", "hongguo"}, []string{"hongguo"}) {
		t.Fatal("多出来的站源不影响覆盖判断")
	}
}

// 0.8.20：远端同步灌库走的是「并集」，并集就必须按 id 去重。
//
// 两个来源都可能带重复 id：① 快照是多轮并集 + 增量 `--merge` 叠出来的，
// 脚本一旦出错就会写进两份；② 本地剧库本身是分页抓的，翻页重叠也会重复。
// 合并时按 id 收一遍，重复的字段取并（空字段回落到先出现的那份）。
func TestMergeNativeCatalogDedupesRepeatedIDs(t *testing.T) {
	merged := mergeNativeCatalog([]nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "剧 A"},
		{ID: sourceHongguo + ":b", Source: sourceHongguo, Title: "剧 B"},
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Cover: "https://example.invalid/a.jpg"},
	}, nil)
	if len(merged) != 2 {
		t.Fatalf("first 内部的重复 id 没有去掉：%+v", merged)
	}
	if merged[0].ID != sourceHongguo+":a" || merged[0].Title != "剧 A" || merged[0].Cover == "" {
		t.Fatalf("重复条目应该合并字段而不是丢弃：%+v", merged[0])
	}
	if merged[1].ID != sourceHongguo+":b" {
		t.Fatalf("顺序被打乱了：%+v", merged)
	}

	// 两边都有同一个 id：只留一份，且新值优先。
	merged = mergeNativeCatalog(
		[]nativeDrama{{ID: sourceHongguo + ":a", Title: "旧标题"}},
		[]nativeDrama{{ID: sourceHongguo + ":a", Title: "新标题"}, {ID: sourceHongguo + ":a", Description: "补的简介"}},
	)
	if len(merged) != 1 {
		t.Fatalf("跨两个切片的重复 id 没有去掉：%+v", merged)
	}
	if merged[0].Title != "新标题" || merged[0].Description != "补的简介" {
		t.Fatalf("重复条目字段没有合并：%+v", merged[0])
	}

	// 没有 id 的条目无法判重，原样保留 —— 宁可多一条也不要吞数据。
	merged = mergeNativeCatalog([]nativeDrama{{Title: "无 id 一"}, {Title: "无 id 二"}}, nil)
	if len(merged) != 2 {
		t.Fatalf("空 id 不该被当成同一个：%+v", merged)
	}
}

// 快照文件里如果写了同一个 id 两次，灌进剧库后只能剩一条。
func TestNativeSyncCatalogSnapshotDedupesRepeatedItems(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Cover: "https://example.invalid/a.jpg"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, nil))
	if _, err := engine.nativeSyncCatalogSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	items := engine.catalogs[sourceHongguo]
	if len(items) != 1 {
		t.Fatalf("快照里的重复条目应该被合并成一条：%+v", items)
	}
	if items[0].Title != "快照剧 A" || items[0].Cover == "" {
		t.Fatalf("合并后字段不全：%+v", items[0])
	}
}

// 0.8.20：站源管理页的「同步远程数据源」按钮要先知道远端有多新。
//
// 这条链路必须**只读索引**：页面每次打开都会调它，顺手下载站源文件的话，
// 光进一次设置页就要几十 MB。
func TestNativeCatalogSnapshotInfoReadsIndexOnly(t *testing.T) {
	payload := catalogSnapshotPayloadForTest(t, []nativeDrama{
		{ID: sourceHongguo + ":a", Source: sourceHongguo, Title: "快照剧 A"},
	})
	files := map[string][]byte{
		"index.json":                catalogSnapshotIndexForTest(t, payload),
		sourceHongguo + ".json.enc": payload,
	}
	var hits int32
	engine := reopenCatalogEngine(t, t.TempDir(), catalogSnapshotTransport(files, &hits))

	info, err := engine.nativeCatalogSnapshotInfo(context.Background())
	if err != nil {
		t.Fatalf("读取索引失败：%v", err)
	}
	if info.GeneratedAt != "2026-11-02T10:00:00Z" {
		t.Fatalf("远端生成时间不对：%+v", info)
	}
	if info.Counts[sourceHongguo] != 2 || len(info.Sources) != 1 {
		t.Fatalf("站源条数不对：%+v", info)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("只该发一次索引请求，实际 %d 次", atomic.LoadInt32(&hits))
	}
	// 还没灌过任何快照：页面据此显示"本机有新版本可同步"。
	if info.AppliedGeneratedAt != "" {
		t.Fatalf("未同步过不该有已应用版本：%+v", info)
	}
}
