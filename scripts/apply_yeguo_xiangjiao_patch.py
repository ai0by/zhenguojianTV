from pathlib import Path
import json


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, got {count}: {old[:80]!r}")
    p.write_text(text.replace(old, new, 1), encoding="utf-8")


# 1) Provider source routing / canonical IDs.
replace_once(
    "native/core/provider_huangguo.go",
    "case sourceHuangguoAI, sourceHuangguoVideo, sourceHuangdou, sourceHongguo, sourceHuangju, sourceDuanju51, sourceCloudFront:",
    "case sourceHuangguoAI, sourceHuangguoVideo, sourceHuangdou, sourceHongguo, sourceHuangju, sourceDuanju51, sourceYeguo, sourceXiangjiao, sourceCloudFront:",
)
replace_once(
    "native/core/provider_huangguo.go",
    '''\tcase "51duanju", "51短剧", "51dj", "blue.xdrgcewe.cc":\n\t\treturn sourceDuanju51\n\tcase "cloudfront":''',
    '''\tcase "51duanju", "51短剧", "51dj", "blue.xdrgcewe.cc":\n\t\treturn sourceDuanju51\n\tcase "yeguo", "野果", "野果短剧", "blue.ekzhadks.cc", "api.ekzhadks.cc":\n\t\treturn sourceYeguo\n\tcase "xiangjiao", "香蕉", "香蕉短剧", "xiangjiaoai.ai", "xiangjiao01.com":\n\t\treturn sourceXiangjiao\n\tcase "cloudfront":''',
)

# 2) Downloader instance caches: Yeguo crypto config + Xiangjiao guest session.
replace_once(
    "native/core/app_runtime.go",
    '''\tpreviewMu             sync.Mutex\n\tpreviewSessions       map[string]*huangguoPreviewSession\n}''',
    '''\tpreviewMu             sync.Mutex\n\tpreviewSessions       map[string]*huangguoPreviewSession\n\tyeguoCryptoMu         sync.Mutex\n\tyeguoCrypto           yeguoCryptoConfig\n\tyeguoCryptoExpires    time.Time\n\txiangjiaoSessionMu    sync.Mutex\n\txiangjiaoDeviceID     string\n\txiangjiaoAccessToken  string\n\txiangjiaoAccessExpires time.Time\n}''',
)

# 3) Online search for Yeguo. Xiangjiao intentionally uses its hot catalog as local search.
needle = '''\tif query != "" && source == sourceDuanju51 {\n\t\titems, more, err := d.fetchDuanju51CatalogPage(ctx, page, "", query)\n\t\tif err != nil {\n\t\t\treturn result, err\n\t\t}\n\t\tfor _, drama := range items {\n\t\t\tresult.Items = append(result.Items, nativeNormalize(drama))\n\t\t}\n\t\tresult.HasMore = more\n\t\treturn result, nil\n\t}\n'''
insert = needle + '''\tif query != "" && source == sourceYeguo {\n\t\titems, more, err := d.fetchYeguoCatalogPage(ctx, page, query)\n\t\tif err != nil {\n\t\t\treturn result, err\n\t\t}\n\t\tfor _, drama := range items {\n\t\t\tresult.Items = append(result.Items, nativeNormalize(drama))\n\t\t}\n\t\tresult.HasMore = more\n\t\treturn result, nil\n\t}\n'''
replace_once("native/core/app_runtime.go", needle, insert)

# 4) Catalog + detail dispatch.
replace_once(
    "native/core/app_runtime.go",
    '''\tcase sourceDuanju51:\n\t\titems, result.HasMore, err = d.fetchDuanju51CatalogPage(ctx, page, category, "")\n\tcase sourceHuangguoVideo:''',
    '''\tcase sourceDuanju51:\n\t\titems, result.HasMore, err = d.fetchDuanju51CatalogPage(ctx, page, category, "")\n\tcase sourceYeguo:\n\t\titems, result.HasMore, err = d.fetchYeguoCatalogPage(ctx, page, "")\n\tcase sourceXiangjiao:\n\t\titems, result.HasMore, err = d.fetchXiangjiaoCatalogPage(ctx, page, "")\n\tcase sourceHuangguoVideo:''',
)
replace_once(
    "native/core/app_runtime.go",
    '''\tcase sourceDuanju51:\n\t\traw, chapters, err = engine.downloader.fetchDuanju51Detail(ctx, sourceID)\n\tdefault:''',
    '''\tcase sourceDuanju51:\n\t\traw, chapters, err = engine.downloader.fetchDuanju51Detail(ctx, sourceID)\n\tcase sourceYeguo:\n\t\traw, chapters, err = engine.downloader.fetchYeguoDetail(ctx, sourceID)\n\tcase sourceXiangjiao:\n\t\traw, chapters, err = engine.downloader.fetchXiangjiaoDetail(ctx, sourceID)\n\tdefault:''',
)

# 5) Categories: both new sources currently expose a safe "全部" entry.
replace_once(
    "native/core/app_categories.go",
    '''\tcase sourceDuanju51:\n\t\treturn append(all, duanju51Categories...), nil\n\t}''',
    '''\tcase sourceDuanju51:\n\t\treturn append(all, duanju51Categories...), nil\n\tcase sourceYeguo, sourceXiangjiao:\n\t\treturn all, nil\n\t}''',
)

# 6) Base URL / host recognition and real-time playback session resolution.
replace_once(
    "native/core/provider_media.go",
    '''\tcase sourceDuanju51:\n\t\tfallback = duanju51BaseURL\n\tdefault:''',
    '''\tcase sourceDuanju51:\n\t\tfallback = duanju51BaseURL\n\tcase sourceYeguo:\n\t\tfallback = yeguoBaseURL\n\tcase sourceXiangjiao:\n\t\tfallback = xiangjiaoBaseURL\n\tdefault:''',
)
replace_once(
    "native/core/provider_media.go",
    '''\tcase duanju51MirrorHost(host):\n\t\treturn sourceDuanju51\n\tdefault:''',
    '''\tcase duanju51MirrorHost(host):\n\t\treturn sourceDuanju51\n\tcase yeguoMirrorHost(host) || host == "api.ekzhadks.cc":\n\t\treturn sourceYeguo\n\tcase xiangjiaoMirrorHost(host):\n\t\treturn sourceXiangjiao\n\tdefault:''',
)
replace_once(
    "native/core/provider_media.go",
    '''\tmedia := providerMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, d.providerBaseURL(chapter.Source)+"/")}\n\tif chapter.Source == sourceHuangdou {''',
    '''\tmedia := providerMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, d.providerBaseURL(chapter.Source)+"/")}\n\tif chapter.Source == sourceYeguo && strings.HasPrefix(chapter.VideoURL, "yeguo-play://") {\n\t\tvar err error\n\t\tmedia, err = d.resolveYeguoPlayback(ctx, task)\n\t\tif err != nil {\n\t\t\treturn providerMedia{}, err\n\t\t}\n\t}\n\tif chapter.Source == sourceXiangjiao && strings.HasPrefix(chapter.VideoURL, "xiangjiao-play://") {\n\t\tvar err error\n\t\tmedia, err = d.resolveXiangjiaoPlayback(ctx, task)\n\t\tif err != nil {\n\t\t\treturn providerMedia{}, err\n\t\t}\n\t}\n\tif chapter.Source == sourceHuangdou {''',
)

# 7) Flutter source registry and search behavior.
replace_once(
    "lib/models.dart",
    "bool get onlineSearch => id == 'hongguo' || id == 'huangju' || id == '51duanju';",
    "bool get onlineSearch => id == 'hongguo' || id == 'huangju' || id == '51duanju' || id == 'yeguo';",
)
replace_once(
    "lib/models.dart",
    "bool get pagedSearch => id == 'huangju' || id == '51duanju';",
    "bool get pagedSearch => id == 'huangju' || id == '51duanju' || id == 'yeguo';",
)
replace_once(
    "lib/models.dart",
    '''    SourceSite('51duanju', '51短剧', '首页 · 榜单 · 剧场'),\n    SourceSite('huangguo-video', '黄果剧场', '视频剧集'),''',
    '''    SourceSite('51duanju', '51短剧', '首页 · 榜单 · 剧场'),\n    SourceSite('yeguo', '野果短剧', '推荐 · 搜索 · 完整分集'),\n    SourceSite('xiangjiao', '香蕉短剧', '热播 · 正片会话'),\n    SourceSite('huangguo-video', '黄果剧场', '视频剧集'),''',
)

# 8) Remote mirror config can be changed independently of APK releases.
config_path = Path("config/source_mirrors.json")
doc = json.loads(config_path.read_text(encoding="utf-8"))
sources = doc.setdefault("sources", {})
sources["yeguo"] = [
    "https://blue.ekzhadks.cc",
    "https://yeguodj.com",
    "https://ygdj16.com",
    "https://hoamqvgal.cc",
]
sources["xiangjiao"] = [
    "https://xiangjiaoai.ai",
    "https://xiangjiao01.com",
]
config_path.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

# 9) Fix map[any]any traversal: maps are not comparable and cannot be used as map keys.
replace_once(
    "native/core/provider_yeguo.go",
    '''func walkYeguoMaps(value any, visit func(map[string]any)) {\n\tseen := map[any]bool{}\n\tvar walk func(any)\n''',
    '''func walkYeguoMaps(value any, visit func(map[string]any)) {\n\tvar walk func(any)\n''',
)
replace_once(
    "native/core/provider_yeguo.go",
    '''\t\tcase map[any]any:\n\t\t\t// devalue Map 结构极少出现在剧集对象里；仍做兼容，且避免循环。\n\t\t\tif seen[typed] {\n\t\t\t\treturn\n\t\t\t}\n\t\t\tseen[typed] = true\n\t\t\tfor _, child := range typed {\n\t\t\t\twalk(child)\n\t\t\t}\n''',
    '''\t\tcase map[any]any:\n\t\t\tfor _, child := range typed {\n\t\t\t\twalk(child)\n\t\t\t}\n''',
)

print("Yeguo/Xiangjiao source integration patch applied")
