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
	sum := sha256.Sum256(payload)
	index, err := json.Marshal(map[string]any{
		"version":     1,
		"generatedAt": "2026-11-02T10:00:00Z",
		"algorithm":   "aes-256-gcm",
		"sources": map[string]any{
			sourceHongguo: map[string]any{
				"file":   "sources/" + sourceHongguo + ".json.enc",
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
	plain, err := json.Marshal(catalogSnapshotPayload{
		Source:      sourceHongguo,
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
