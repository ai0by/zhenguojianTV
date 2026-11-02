package core

import "testing"

// id 生成规则是远端同步合并的唯一依据，必须钉住三件事：
//  1. 站源前缀永远是规范名（别名 `huangguo` 要变成 `huangguoai`）；
//  2. sourceID 的写法归一（尾斜杠 / `?` 查询串 / `#` 锚点 / 首尾引号）；
//  3. 归一化幂等 —— 已经是干净 id 的条目一个字节都不能变，否则老用户的
//     下载记录、历史记录会突然对不上。
func TestProviderDramaIDNormalizesSourceID(t *testing.T) {
	cases := []struct{ source, sourceID, want string }{
		{"hongguo", "7701671804561017918", "hongguo:7701671804561017918"},
		// 尾斜杠 / 前后空白 / 引号：列表页从 HTML 属性里抠出来时很常见。
		{"huangju", " yanyu-1259-1640/ ", "huangju:yanyu-1259-1640"},
		{"huangju", "\"yanyu-1259-1640\"", "huangju:yanyu-1259-1640"},
		// 追踪参数与锚点不能进 id。
		{"sorani", "927?from=list", "sorani:927"},
		{"sorani", "927#ep-3", "sorani:927"},
		{"yeguo", "/1/", "yeguo:1"},
		// 别名必须收敛到规范名，否则同一部剧会有两套 id。
		{"huangguo", "123", "huangguoai:123"},
		{"huangguoai.com", "123", "huangguoai:123"},
		{"huangguo-video", "video/beh2fjae", "huangguo-video:video/beh2fjae"},
		// 内部的 `/` 是黄果剧场 id 的一部分，不能被吃掉。
		{"huangguo-video", "series/ofyszq8z/", "huangguo-video:series/ofyszq8z"},
		// HTML 实体要还原（id 是从属性 / 内联 JSON 里抠出来的）。
		{"yeguo", "a&amp;b", "yeguo:a&b"},
		// 黄豆的 `rp_` 前缀是站点自己的写法，providerDramaID 不管；
		// 归一化只动"写法层面"的东西，不改各站源的业务规则。
		{"huangdou", "rp_6a9ec60be53e713f3c6feccf", "huangdou:rp_6a9ec60be53e713f3c6feccf"},
	}
	for _, c := range cases {
		if got := providerDramaID(c.source, c.sourceID); got != c.want {
			t.Errorf("providerDramaID(%q, %q) = %q，期望 %q", c.source, c.sourceID, got, c.want)
		}
	}
}

func TestProviderDramaIDIsIdempotent(t *testing.T) {
	// 已经干净的 id 再走一遍必须一模一样：线上 119219 条快照的 id 都是这种形态，
	// 只要有一条会变，就等于让所有客户端下次同步时多出一份重复剧。
	clean := []struct{ source, sourceID string }{
		{"hongguo", "7701671804561017918"},
		{"wangguo", "50134"},
		{"huaguo", "83400"},
		{"huangguoai", "982"},
		{"huangguo-video", "video/beh2fjae"},
		{"huangdou", "6a9ec60be53e713f3c6feccf"},
		{"cloudfront", "6acb3232bc28aa7e48532a58"},
		{"sorani", "927"},
		{"hema", "41000115794"},
		{"ezdmw", "1385"},
		{"yeguo", "251"},
		{"51duanju", "14"},
	}
	for _, c := range clean {
		once := providerDramaID(c.source, c.sourceID)
		if twice := providerDramaID(c.source, normalizeProviderSourceID(c.sourceID)); twice != once {
			t.Errorf("%s 的 id 不幂等：%q -> %q", c.source, once, twice)
		}
		if source, sourceID, ok := splitProviderDramaID(once); !ok || source != c.source || sourceID != c.sourceID {
			t.Errorf("生成的 id 解析不回去：%q -> (%q, %q, %v)", once, source, sourceID, ok)
		}
	}
}

func TestProviderChapterIDKeepsSourceIDIntact(t *testing.T) {
	// 章节 id 是 `站源:剧集id:集号` 三段式，中间那段不能被归一出空格或斜杠，
	// 否则 HasPrefix 判断会失效、选集点开拿不到播放地址。
	got := providerChapterID("hema", "41000115794", "592904343")
	if got != "hema:41000115794:592904343" {
		t.Fatalf("章节 id 拼错：%q", got)
	}
	prefix := providerChapterID("hema", "41000115794", "")
	if got[:len(prefix)] != prefix {
		t.Fatalf("章节 id 前缀对不上：%q 不以 %q 开头", got, prefix)
	}
}
