package core

import "testing"

// 黄豆目录的多排序维度（0.8.24）。
//
// `/drama/list` 单排序被服务端卡在第 100 页（30 条/页 ⇒ 3000 部），而
// `category` 参数被忽略、只有 `order` 能改结果集。实测三种排序的并集是
// 5000 部，快照原来只有 3000。这里守住"维度列表"和"参数编码"这两件事 ——
// 它们一旦退化，catalogdump 就只会抓一个排序，缺口感又会回来。
func TestHuangdouOrderCategoriesCoverAllSorts(t *testing.T) {
	orders := huangdouOrderCategories()
	if len(orders) < 2 {
		t.Fatalf("至少要遍历 2 个排序才有意义，实际 %v", orders)
	}
	seen := map[string]bool{}
	for _, order := range orders {
		if seen[order] {
			t.Fatalf("排序维度重复：%q", order)
		}
		seen[order] = true
	}
	// 必须包含实测能带来新数据的 new / hot。
	for _, want := range []string{"new", "hot"} {
		if !seen[want] {
			t.Fatalf("缺少实测有效的排序 %q，实际 %v", want, orders)
		}
	}
}

// `order:<v>` 编码必须能被 validNativeCategory 放行，否则 catalogdump
// 传下去会被"内容分类无效"挡掉（2026-11-05 真踩过：分类枚举整段失效，
// 黄豆只有 3000 条）。
func TestHuangdouOrderCategoryPassesValidation(t *testing.T) {
	if !validNativeCategory(sourceHuangdou, "") {
		t.Fatal("空分类（主排序）应当放行")
	}
	for _, order := range huangdouOrderCategories() {
		if order == huangdouOrderCategoryAll {
			continue
		}
		encoded := huangdouOrderCategoryPrefix + order
		if !validNativeCategory(sourceHuangdou, encoded) {
			t.Fatalf("%q 应当通过校验", encoded)
		}
	}
	// 不在白名单里的排序值必须拒绝，避免把任意参数透给上游。
	if validNativeCategory(sourceHuangdou, huangdouOrderCategoryPrefix+"bogus") {
		t.Fatal("未知排序值不该放行")
	}
	// 不带前缀的分类对黄豆无意义（服务端会忽略），应当拒绝。
	if validNativeCategory(sourceHuangdou, "随便一个分类") {
		t.Fatal("黄豆没有真实分类，非 order: 前缀不该放行")
	}
}
