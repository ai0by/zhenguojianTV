#!/usr/bin/env python3
"""把多轮 `catalogdump` 的结果按站源取并集（0.8.20）。

为什么需要（2026-11-02 实测）：红果的 App 分类接口**每次返回的切片不一样**。
同一天用同一份代码跑四轮，分别拿到 1331 / 2803 / 3217 / 3595 条，两两交集
只有 1200~2100 —— 也就是说服务端每次都"看起来翻到底了"，但给出的却是不同
子集。任何单轮结果都不能当成全站目录，用户那边本机反而比远端多就是这么来的。

并集是收敛的：实测第 1 轮 2803 → 加上第 2 轮 3903 → 第 3 轮不再新增。

用法：

    python3 scripts/merge_catalog_dumps.py \
        --input /tmp/catalog-dump-passes --output /tmp/catalog-dump

`--input` 是**父目录**，里面每个子目录是一轮（`pass-1` / `pass-2` / ...，
按名字里的数字排序，不是字典序 —— 否则 `pass-10` 会排到 `pass-2` 前面）。
子目录里的 `<source>.json` 结构同 `catalogdump` 输出。

顺序必须确定：轮次按数字升序、轮内按站点返回顺序，同一组输入永远得到同一份
输出 → 同一份密文 → index 的 digest 不变 → 客户端不用重下。
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path


def _pass_order(path: Path) -> tuple[int, str]:
    """按目录名里的数字排序，没有数字的排最后（保持稳定）。"""
    match = re.search(r"(\d+)", path.name)
    return (int(match.group(1)) if match else 1 << 30, path.name)


def union_passes(dirs: list[Path]) -> dict[str, list]:
    """把每一轮的结果按站源并起来，按 id 去重，先出现者在前。"""
    merged: dict[str, list] = {}
    seen: dict[str, set[str]] = {}
    for directory in dirs:
        for path in sorted(directory.glob("*.json")):
            try:
                payload = json.loads(path.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as exc:
                print(f"[warn] 跳过 {path}: {exc}", file=sys.stderr)
                continue
            items = payload.get("items")
            if not isinstance(items, list):
                continue
            source = str(payload.get("source") or path.stem).strip()
            if not source:
                continue
            bucket = merged.setdefault(source, [])
            ids = seen.setdefault(source, set())
            for item in items:
                if not isinstance(item, dict):
                    continue
                ident = str(item.get("id") or "")
                if not ident or ident in ids:
                    continue
                ids.add(ident)
                bucket.append(item)
    return merged


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="合并多轮剧集目录导出")
    parser.add_argument("--input", required=True, help="父目录，每个子目录是一轮")
    parser.add_argument("--output", required=True, help="合并后的输出目录")
    parser.add_argument(
        "--baseline",
        default="",
        help="可选：上一轮快照的导出目录，其条目追加在末尾（给没有多轮抓取的站源保底）",
    )
    args = parser.parse_args(argv)

    parent = Path(args.input)
    if not parent.is_dir():
        print(f"输入目录不存在：{parent}", file=sys.stderr)
        return 2
    passes = sorted((p for p in parent.iterdir() if p.is_dir()), key=_pass_order)
    if not passes:
        print(f"{parent} 下没有任何轮次目录", file=sys.stderr)
        return 2

    merged = union_passes(passes)
    if args.baseline:
        base = Path(args.baseline)
        if base.is_dir():
            for source, items in union_passes([base]).items():
                bucket = merged.setdefault(source, [])
                ids = {str(item.get("id") or "") for item in bucket}
                for item in items:
                    ident = str(item.get("id") or "")
                    if ident and ident not in ids:
                        ids.add(ident)
                        bucket.append(item)

    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    total = 0
    for source in sorted(merged):
        items = merged[source]
        if not items:
            continue
        total += len(items)
        body = json.dumps(
            {"source": source, "generatedAt": stamp, "items": items},
            ensure_ascii=False,
            separators=(",", ":"),
        )
        (output / f"{source}.json").write_text(body, encoding="utf-8")
        print(f"[ok] {source}: {len(items)} 条（{len(passes)} 轮并集）")
    print(f"合计 {len(merged)} 个站源 / {total} 条 -> {output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
