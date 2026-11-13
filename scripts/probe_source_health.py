#!/usr/bin/env python3
"""站源存活性巡检（0.8.30 之后新增）。

只做连通性与页面结构诊断，**不输出页面正文**：这些站源的列表页会带大量
与诊断无关的文案，打进 CI 日志既没用也难读。这里只报告状态码、耗时、
字节数、脚本标签名与结构特征命中次数。

判定口径（很重要）：
  - `OK`     ：至少一个探测路径返回 200；
  - `4XX-ONLY`：域名有 HTTP 响应，但根路径只回 4xx。对 `*-api` 这类
    纯接口域名这是**正常**的，不代表站源挂了；
  - `PROXY-ERROR`：本机代理自己回了 502 / 隧道失败，压根没连到目标站，
    不能据此判定站源死亡，换 `--no-proxy` 复核；
  - `DOWN`   ：所有探测都没有拿到 HTTP 响应（DNS / 连接 / 超时 / 5xx）。

用法：
    python3 scripts/probe_source_health.py                 # 巡检全部站源
    python3 scripts/probe_source_health.py --focus 51duanju
    python3 scripts/probe_source_health.py --no-proxy     # 绕过系统代理直连复核
    python3 scripts/probe_source_health.py --strict       # 有 DOWN / PROXY-ERROR 则非零退出
    python3 scripts/probe_source_health.py --candidates   # 额外探测 51 旧路径与候选域名
    python3 scripts/probe_source_health.py --json out.json

CI 里会把表格写进 $GITHUB_STEP_SUMMARY。
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MIRRORS = ROOT / "config" / "source_mirrors.json"
USER_AGENT = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/125.0 Safari/537.36"
)
MAX_BODY = 2_000_000

# 51 短剧 2026-11 改版后的真实路由（SSR + hg-* 模板）。默认就探这几条，
# 因为只探首页无法区分「站点活着但解析器路径全错」和「域名真的挂了」。
DUANJU51_PATHS = [
    "/",
    "/ranks/hot/",
    "/ranks/recommend/",
    "/ranks/potential/",
    "/newest/",
    "/ai-duanju/",
    "/video/14194/",
    "/video/14194/ep-3/",
    "/search/?keyword=%E6%B2%89%E9%BB%98",
]

# 排查改版时才探：改版前 Nuxt 时期的旧路径，用来证明「是路径变了不是域名死了」。
DUANJU51_LEGACY_PATHS = [
    "/rank/",
    "/rank/week/",
    "/new-theater-category/",
    "/play/14194/1/",
    "/search/test/",
]

CANDIDATE_51_HOSTS = [
    "https://51duanju.com",
    "https://www.51duanju.com",
    "https://51dj.cc",
    "https://51duanju.app",
]

# 只统计特征出现次数，不取字段值；新旧两套结构都列出来，方便对比改版。
STRUCTURE_KEYS = [
    # 新版 SSR / hg-* 模板
    "hg-drama-card",
    "hg-rank-item",
    "hg-card-grid",
    "hg-web-play__ep",
    "videoInitialData",
    "epPlaySrcs",
    "videoSrc",
    "coverSrc",
    "trackingEpisodes",
    "episodeDurationLabels",
    "auth_key",
    "m3u8",
    "hg-pager",
    # 旧版 Nuxt / devalue
    "__NUXT_DATA__",
    "playlet_id",
    "episodeAll",
    "video_url",
    "video_id",
    "total_serial",
    "cover_img",
    "drama_name",
]


PROXY_ERROR_MARKERS = (
    "tunnel connection failed",
    "proxyerror",
    "bad gateway",
    "cannot connect to proxy",
)


def is_proxy_error(error: str) -> bool:
    lowered = (error or "").lower()
    return any(marker in lowered for marker in PROXY_ERROR_MARKERS)


def http_get(url: str, timeout: float = 15.0, opener=None) -> dict:
    request = urllib.request.Request(
        url,
        headers={
            "User-Agent": USER_AGENT,
            "Accept": "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8",
            "Accept-Language": "zh-CN,zh;q=0.9",
        },
    )
    started = time.monotonic()
    try:
        open_request = opener.open if opener is not None else urllib.request.urlopen
        with open_request(request, timeout=timeout) as response:
            body = response.read(MAX_BODY)
            return {
                "ok": True,
                "status": response.status,
                "elapsedMs": int((time.monotonic() - started) * 1000),
                "size": len(body),
                "finalUrl": response.geturl(),
                "html": body.decode("utf-8", "replace"),
            }
    except urllib.error.HTTPError as error:
        # 4xx/5xx 也是「主机有响应」，只是这个路径不给内容；要单独区分。
        return {
            "ok": False,
            "status": error.code,
            "elapsedMs": int((time.monotonic() - started) * 1000),
            "error": "HTTP " + str(error.code),
            "html": "",
        }
    except Exception as error:  # noqa: BLE001 - 巡检要把网络异常也记下来
        return {
            "ok": False,
            "status": None,
            "elapsedMs": int((time.monotonic() - started) * 1000),
            "error": type(error).__name__ + ": " + str(error)[:120],
            "html": "",
        }


def structure_report(html: str) -> dict:
    if not html:
        return {}
    nuxt = re.search(
        r'<script[^>]*\bid=["\']__NUXT_DATA__["\'][^>]*>(.*?)</script>',
        html,
        re.S | re.I,
    )
    initial = re.search(
        r'<script[^>]*\bid=["\']videoInitialData["\'][^>]*>(.*?)</script>',
        html,
        re.S | re.I,
    )
    paths = sorted(
        set(
            re.findall(
                r'href=["\'](/[a-z0-9\-/]*\d+/?(?:\?[a-z0-9=&%\-]*)?)["\']',
                html,
                re.I,
            )
        )
    )
    hits = {key: html.count(key) for key in STRUCTURE_KEYS if key in html}
    return {
        "hasNuxtData": nuxt is not None,
        "nuxtDataBytes": len(nuxt.group(1)) if nuxt else 0,
        "hasVideoInitialData": initial is not None,
        "videoInitialDataBytes": len(initial.group(1)) if initial else 0,
        "scriptIds": re.findall(r'<script[^>]*\bid=["\']([^"\']+)["\']', html, re.I)[:12],
        "scriptSrcs": re.findall(r'<script[^>]*\bsrc=["\']([^"\']+)["\']', html, re.I)[:8],
        "keyHits": hits,
        "numericPaths": paths[:8],
        "is404": bool(re.search(r"页面未找到|404|Not Found", html[:4000], re.I)),
    }


def load_sources() -> dict:
    document = json.loads(MIRRORS.read_text(encoding="utf-8"))
    return document.get("sources", {})


def probe_source(source: str, origins: list, include_candidates: bool, opener=None) -> dict:
    if source == "51duanju":
        paths = list(DUANJU51_PATHS)
        if include_candidates:
            paths += DUANJU51_LEGACY_PATHS
        probes = [(origin, path) for origin in origins for path in paths]
        if include_candidates:
            probes += [(host, "/") for host in CANDIDATE_51_HOSTS]
    else:
        probes = [(origin, "/") for origin in origins]

    def run(item):
        origin, path = item
        result = http_get(origin.rstrip("/") + path, opener=opener)
        report = {
            "source": source,
            "url": origin.rstrip("/") + path,
            "ok": result["ok"],
            "status": result.get("status"),
            "elapsedMs": result.get("elapsedMs"),
            "size": result.get("size", 0),
            "error": result.get("error"),
        }
        if source == "51duanju" and result["html"]:
            report["structure"] = structure_report(result["html"])
        return report

    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        results = list(pool.map(run, probes))

    alive_origins = sorted(
        {
            "/".join(r["url"].split("/")[:3])
            for r in results
            if r["ok"] and r.get("status") == 200
        }
    )
    # 4xx 也是「主机活着」，但不能算站源可用。
    responded = [r for r in results if r.get("status") is not None and r["status"] < 500]
    errors = [r.get("error") or "" for r in results if r.get("error")]
    if alive_origins:
        verdict = "OK"
    elif responded:
        verdict = "4XX-ONLY"
    elif errors and all(is_proxy_error(error) for error in errors):
        verdict = "PROXY-ERROR"
    else:
        verdict = "DOWN"
    return {
        "source": source,
        "verdict": verdict,
        "alive": bool(alive_origins),
        "originsAlive": alive_origins,
        "httpResponses": len(responded),
        "probeCount": len(results),
        "probes": results,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--focus", default="", help="只巡检某个站源 id")
    parser.add_argument("--strict", action="store_true", help="有站源完全不可达时返回非零")
    parser.add_argument("--json", default="", help="把完整结果写成 JSON")
    parser.add_argument("--candidates", action="store_true", help="额外探测候选域名与 51 旧路径")
    parser.add_argument("--no-proxy", action="store_true", help="绕过系统代理，直连复核")
    args = parser.parse_args()

    opener = None
    if args.no_proxy:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    sources = load_sources()
    if args.focus:
        sources = {key: value for key, value in sources.items() if key == args.focus}
        if not sources:
            print("unknown source: " + args.focus)
            return 2

    results = [probe_source(name, origins, args.candidates, opener) for name, origins in sources.items()]
    for item in results:
        print(f"[{item['verdict']:<8}] {item['source']}: alive_origins={len(item['originsAlive'])} "
              f"http_responses={item['httpResponses']}/{item['probeCount']}")
        for probe in item["probes"]:
            detail = ""
            if probe.get("structure"):
                struct = probe["structure"]
                hits = struct["keyHits"]
                detail = (
                    f" nuxt={struct['hasNuxtData']}/{struct['nuxtDataBytes']}B"
                    f" initialData={struct['hasVideoInitialData']}/{struct['videoInitialDataBytes']}B"
                    f" ids={struct['scriptIds'][:4]}"
                    f" hits={hits}"
                    f" paths={struct['numericPaths'][:3]}"
                    f" is404={struct['is404']}"
                )
            print(
                f"    {probe.get('status') or '-':>4} {probe['elapsedMs']:>6}ms"
                f" {probe['size']:>8}B  {probe['url']}"
                f"{' ' + probe['error'] if probe.get('error') else ''}{detail}"
            )

    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        lines = [
            "## 站源存活性巡检", "",
            "`OK` = 至少一条 200；`4XX-ONLY` = 主机有响应但路径不给内容（纯 API 域名常见）；"
            "`PROXY-ERROR` = 代理自身失败，需直连复核；`DOWN` = 完全拿不到 HTTP 响应。", "",
            "| 站源 | 判定 | 200 域名数 | HTTP 响应 / 探测数 |",
            "| --- | --- | --- | --- |",
        ]
        lines += [
            f"| {item['source']} | {item['verdict']} | {len(item['originsAlive'])} | "
            f"{item['httpResponses']} / {item['probeCount']} |"
            for item in results
        ]
        focus51 = next((item for item in results if item["source"] == "51duanju"), None)
        if focus51:
            lines += ["", "### 51 短剧结构命中", "", "| 路径 | 状态 | 结构特征 |", "| --- | --- | --- |"]
            for probe in focus51["probes"]:
                struct = probe.get("structure") or {}
                hits = ", ".join(f"{k}×{v}" for k, v in sorted((struct.get("keyHits") or {}).items()))
                lines.append(f"| `{probe['url']}` | {probe.get('status') or '-'} | {hits or '—'} |")
        Path(summary).write_text("\n".join(lines) + "\n", encoding="utf-8")

    if args.json:
        Path(args.json).write_text(
            json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8"
        )

    down = [item["source"] for item in results if item["verdict"] == "DOWN"]
    proxy = [item["source"] for item in results if item["verdict"] == "PROXY-ERROR"]
    limited = [item["source"] for item in results if item["verdict"] == "4XX-ONLY"]
    if down:
        print("\nDOWN SOURCES: " + ", ".join(down))
    if proxy:
        print("PROXY-ERROR SOURCES (retry with --no-proxy): " + ", ".join(proxy))
    if limited:
        print("4XX-ONLY SOURCES (not necessarily broken): " + ", ".join(limited))
    return 1 if args.strict and (down or proxy) else 0


if __name__ == "__main__":
    sys.exit(main())
