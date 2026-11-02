#!/usr/bin/env python3
"""把 catalogdump 导出的剧集目录加密成 App 可直接消费的快照（0.8.19）。

输入：`native/cmd/catalogdump` 导出的 `<source>.json`
（结构 `{"source": ..., "generatedAt": ..., "items": [...]}`）。

输出（写到 `catalog` 分支的根目录）：

    index.json                 明文索引：版本 / 生成时间 / 每个站源的文件与 sha256
    sources/<source>.json.enc  `[12 字节 nonce][AES-256-GCM 密文]`

**加密不是安全边界**：密钥由下面的 `SECRET` 派生，而 `SECRET` 随 APK 一起分发，
有心人扒包就能解。它的作用只是"别让人随手把整份目录拖走"，同时保证客户端
能识别文件是否被中间人替换（配合 index 里的 sha256）。

密钥派生与 Go 侧 `core/app_catalog_snapshot.go` 的 `catalogSnapshotKey()` 必须一致：
`sha256(SECRET)`，AES-256-GCM，nonce 12 字节前置。

两种运行模式（0.8.20）：

    全量（默认，每天一次）   整站重抓，本轮抓到什么就写什么。
    增量（`--merge`，每 6 小时）只翻前几页，用上一轮快照把后面的剧补齐。

增量是给站点风控让路：每 2 小时整站翻 60 页太像爬虫，容易被封 IP。翻页节奏
降下来之后，靠"新剧在最前面"这个事实做合并就够了 —— 老剧不会凭空消失。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

SECRET = "zhenguojianTV/catalog-snapshot/v1"
ALGORITHM = "aes-256-gcm"
VERSION = 1
NONCE_SIZE = 12
# 单个站源快照上限，与 Go 侧 `catalogSnapshotMaxFile` 对齐。
MAX_SOURCE_BYTES = 24 << 20


def derive_key(secret: str = SECRET) -> bytes:
    """按 Go 侧同样的方式派生 32 字节密钥。"""
    return hashlib.sha256(secret.encode("utf-8")).digest()


def encrypt(plain: bytes, key: bytes | None = None) -> bytes:
    """nonce 前置的 AES-256-GCM 密文。

    **nonce 由明文派生**（`sha256(明文)[:12]`），不是随机数 —— 这是刻意的：

    随机 nonce 会让同一份内容每轮产出不同密文，`sha256` 跟着变，index.json
    每 2 小时都是一版"新快照"，所有客户端于是每轮都要重下几 MB。改成
    确定性 nonce 后，内容不变 → 密文不变 → index 不变 → CI 跳过提交、
    客户端只花一个几 KB 的索引请求。

    安全性上这里没问题：nonce 是明文的函数，不同明文不会撞同一个 nonce；
    同一明文重复加密得到同一密文，本来就是幂等操作。何况这份密钥随 APK
    分发，加密本身只是混淆。
    """
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    key = key or derive_key()
    nonce = hashlib.sha256(plain).digest()[:NONCE_SIZE]
    return nonce + AESGCM(key).encrypt(nonce, plain, None)


def decrypt(blob: bytes, key: bytes | None = None) -> bytes:
    """解密（测试与排查用；App 侧等价实现见 Go）。"""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    key = key or derive_key()
    if len(blob) <= NONCE_SIZE:
        raise ValueError("密文过短")
    return AESGCM(key).decrypt(blob[:NONCE_SIZE], blob[NONCE_SIZE:], None)


def _now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def read_previous_index(output_dir: Path) -> dict:
    """读出上一轮的 index.json（读不到就当空）。"""
    path = output_dir / "index.json"
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def previous_entries(output_dir: Path) -> dict:
    """读出上一次的索引，只保留文件还在的站源（0.8.19）。

    为什么需要：CI 跑在境外，某些站源某一轮可能整体抓不到。如果每次都从零
    重建索引，一次失败就会把那个站源从快照里抹掉，所有客户端立刻退回现场抓取。
    所以"这一轮没产出的站源"沿用上一轮的文件与 sha256。
    """
    data = read_previous_index(output_dir)
    sources = data.get("sources")
    if not isinstance(sources, dict):
        return {}
    kept = {}
    for source, entry in sources.items():
        if not isinstance(entry, dict):
            continue
        file = str(entry.get("file") or "")
        if file and (output_dir / file).is_file():
            kept[str(source)] = {
                "file": file,
                "sha256": str(entry.get("sha256") or ""),
                "count": int(entry.get("count") or 0),
            }
    return kept


def content_digest(entries: dict) -> str:
    """按站源内容算一个稳定摘要，用来判断"这一轮到底变没变"。

    只看 `file` / `sha256` / `count`，不看时间戳 —— 时间戳每轮都变，
    拿它做判断等于每轮都算"变了"。
    """
    parts = [
        f"{source}\0{entry.get('file')}\0{entry.get('sha256')}\0{entry.get('count')}"
        for source, entry in sorted(entries.items())
    ]
    return hashlib.sha256("\n".join(parts).encode("utf-8")).hexdigest()


def load_previous_items(output_dir: Path, source: str, key: bytes) -> list:
    """解密上一轮该站源的快照，取出 items（读不到就当空）。

    增量模式下 catalogdump 只翻前几页，靠这份旧数据把后面的剧补回来。
    解不开 / 解析失败都不该让整轮失败 —— 最坏情况就是这一轮退化成"只有新抓到的"。
    """
    path = output_dir / f"sources/{source}.json.enc"
    if not path.is_file():
        return []
    try:
        payload = json.loads(decrypt(path.read_bytes(), key))
    except Exception as exc:  # noqa: BLE001 - 任何异常都只降级，不中断
        print(f"[warn] {source}: 读不到上一轮快照（{exc}），本轮只写新抓到的", file=sys.stderr)
        return []
    items = payload.get("items")
    if not isinstance(items, list):
        return []
    return [item for item in items if isinstance(item, dict) and item.get("id")]


def dedupe_items(source: str, items: list) -> list:
    """发布前的最后一道保险：按 id 去重，并检查 id 前缀是否属于这个站源。

    App 侧合并是按 id 走的。文件里一旦出现重复 id，轻则列表里两张一样的卡，
    重则同一部剧的字段互相覆盖；前缀串了站源更糟 —— 桶是分站源的，条目会被
    拿去用错的 provider 解析播放地址，直接播不出来。

    导出工具（`catalogdump`）本身已经按 id 去过重，这里是兜底：它哪天回归了，
    也不该让残缺文件流到客户端。去重只保留先出现的那条，顺序不变，所以
    "内容不变则产物逐字节不变"照样成立。
    """
    seen: set[str] = set()
    kept: list = []
    duplicated = 0
    foreign = 0
    for item in items:
        ident = str(item.get("id") or "").strip()
        if not ident:
            continue
        if ident in seen:
            duplicated += 1
            continue
        seen.add(ident)
        if ":" in ident and ident.split(":", 1)[0] != source:
            foreign += 1
        kept.append(item)
    if duplicated:
        print(
            f"[warn] {source}: 文件里有 {duplicated} 条重复 id，已按 id 去重",
            file=sys.stderr,
        )
    if foreign:
        print(
            f"[warn] {source}: 有 {foreign} 条 id 的前缀不是本战源"
            "（可能是别名，也可能是串了站源）",
            file=sys.stderr,
        )
    return kept


def merge_items(fresh: list, previous: list) -> list:
    """本轮在前、上一轮独有的在后，按 id 去重。

    顺序必须确定：本轮顺序来自站点翻页（稳定），上一轮顺序来自上一版文件
    （也是确定的），所以同一组输入永远得到同一份明文 → 同一份密文 →
    index 的 digest 不变 → 客户端不用重下。任何"按时间排序""随机打散"
    都会让增量模式每轮都产出一版新快照，等于把 0.8.19 的幂等优化废掉。
    """
    merged: list = []
    seen: set[str] = set()
    for item in list(fresh) + list(previous):
        ident = str(item.get("id") or "")
        if not ident or ident in seen:
            continue
        seen.add(ident)
        merged.append(item)
    return merged


def build(input_dir: Path, output_dir: Path, merge: bool = False) -> dict:
    """把 input_dir 下的 <source>.json 全部加密，写出 index.json 并返回它。

    `merge=True`（定时增量）：本轮只翻前几页，抓到的新剧放在前面，其余沿用
    上一轮快照里的顺序。`merge=False`（默认，全量）：本轮抓到什么就写什么。

    两种模式都必须满足"内容不变则产物逐字节不变"，CI 靠 `git diff --cached`
    判断要不要提交，客户端靠 `digest` 判断要不要重下。
    """
    sources_dir = output_dir / "sources"
    sources_dir.mkdir(parents=True, exist_ok=True)
    key = derive_key()
    # 先继承上一轮的站源，再覆盖本轮真正产出成功的。
    entries: dict[str, dict] = previous_entries(output_dir)
    previous = read_previous_index(output_dir)
    produced: set[str] = set()
    for path in sorted(input_dir.glob("*.json")):
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            print(f"[warn] 跳过 {path.name}: {exc}", file=sys.stderr)
            continue
        source = str(payload.get("source") or path.stem).strip()
        items = payload.get("items")
        if not source or not isinstance(items, list) or not items:
            print(f"[warn] 跳过 {path.name}: 没有有效剧集", file=sys.stderr)
            continue
        # 只留 App 需要的字段，顺手把 id 为空的脏数据剔掉；再按 id 去重一次
        # （导出工具本该去过重，这里是发布前兜底，见 dedupe_items）。
        cleaned = dedupe_items(source, [item for item in items if isinstance(item, dict)])
        if not cleaned:
            print(f"[warn] 跳过 {path.name}: 剧集缺少 id", file=sys.stderr)
            continue
        # 增量模式：本轮只翻前几页，用上一轮快照把后面的剧补齐。
        # 合并后如果超过单文件上限，退回"只用本轮抓到的"，而不是整站跳过。
        fresh_count = len(cleaned)
        candidates = [cleaned]
        if merge:
            older = load_previous_items(output_dir, source, key)
            if older:
                candidates.insert(0, merge_items(cleaned, older))
        elif previous_count := int(entries.get(source, {}).get("count") or 0):
            # 全量模式的兜底（0.8.20）：本轮抓到的东西**上一轮全都有**，
            # 一条新剧都没有 —— 这几乎不可能是"站点真的缩水了"，更像是接口
            # 又只吐了一个子集（红果的 App 分类接口就会这样，同一天四轮分别
            # 是 1331 / 2803 / 3217 / 3595 条，两两交集才一千多）。
            #
            # 所以这种情况保留上一轮：宁可多留几部下架剧，也不能把 2000 部
            # 的站源缩成几十部 —— 用户会以为这个站只剩这些，而他本机反而更多。
            # 真·缩水（新剧有增、老剧减少）照旧按本轮写。
            older = load_previous_items(output_dir, source, key)
            known = {str(item.get("id") or "") for item in older}
            if older and all(str(item.get("id") or "") in known for item in cleaned):
                merged = merge_items(cleaned, older)
                if len(merged) > fresh_count:
                    print(
                        f"[keep] {source}: 本轮 {fresh_count} 条全是旧数据（上一轮 {previous_count} 条），"
                        f"判定为接口截断，保留上一轮 {len(merged)} 条",
                        file=sys.stderr,
                    )
                    candidates.insert(0, merged)
        # **不写时间戳**：写进去就等于每轮内容都不同，确定性 nonce 也救不回来。
        body = None
        for candidate in candidates:
            encoded = json.dumps(
                {"source": source, "items": candidate},
                ensure_ascii=False,
                separators=(",", ":"),
            ).encode("utf-8")
            if len(encoded) <= MAX_SOURCE_BYTES:
                body, cleaned = encoded, candidate
                break
        if body is None:
            print(f"[warn] 跳过 {path.name}: 体积超过上限", file=sys.stderr)
            continue
        if len(cleaned) > fresh_count:
            print(f"[merge] {source}: 本轮 {fresh_count} 条 + 上一轮补齐 {len(cleaned) - fresh_count} 条")
        blob = encrypt(body, key)
        name = f"sources/{source}.json.enc"
        (output_dir / name).write_bytes(blob)
        entries[source] = {
            "file": name,
            "sha256": hashlib.sha256(blob).hexdigest(),
            "count": len(cleaned),
        }
        produced.add(source)
        print(f"[ok] {source}: {len(cleaned)} 条 -> {name} ({len(blob)} 字节)")

    for source in sorted(set(entries) - produced):
        print(f"[keep] {source}: 本轮未产出，沿用上一轮快照")

    digest = content_digest(entries)
    # 内容没变就沿用上一版的 generatedAt：客户端靠它判断"这版我灌过没有"，
    # 每轮都换新时间戳等于每轮都让所有客户端重下一遍。
    generated = previous.get("generatedAt") if previous.get("digest") == digest else None
    index = {
        "version": VERSION,
        "generatedAt": str(generated or _now()),
        "algorithm": ALGORITHM,
        "digest": digest,
        "sources": entries,
    }
    (output_dir / "index.json").write_text(
        json.dumps(index, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    return index


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="生成加密的剧集快照")
    parser.add_argument("--input", required=True, help="catalogdump 的输出目录")
    parser.add_argument("--output", required=True, help="快照输出目录")
    parser.add_argument(
        "--merge",
        action="store_true",
        help="增量模式：本轮抓到的放前面，上一轮独有的按原顺序补齐（定时任务用）",
    )
    args = parser.parse_args(argv)
    index = build(Path(args.input), Path(args.output), merge=args.merge)
    if not index["sources"]:
        print("没有任何站源被写入，快照为空", file=sys.stderr)
        return 1
    mode = "增量" if args.merge else "全量"
    print(f"index.json: {len(index['sources'])} 个站源（{mode}），生成时间 {index['generatedAt']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
