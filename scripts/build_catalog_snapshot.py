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
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
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
    """nonce 前置的 AES-256-GCM 密文。"""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    key = key or derive_key()
    nonce = os.urandom(NONCE_SIZE)
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


def previous_entries(output_dir: Path) -> dict:
    """读出上一次的索引，只保留文件还在的站源（0.8.19）。

    为什么需要：CI 跑在境外，某些站源某一轮可能整体抓不到。如果每次都从零
    重建索引，一次失败就会把那个站源从快照里抹掉，所有客户端立刻退回现场抓取。
    所以"这一轮没产出的站源"沿用上一轮的文件与 sha256。
    """
    path = output_dir / "index.json"
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    sources = data.get("sources")
    if not isinstance(sources, dict):
        return {}
    kept = {}
    for source, entry in sources.items():
        if not isinstance(entry, dict):
            continue
        file = str(entry.get("file") or "")
        if file and (output_dir / file).is_file():
            kept[str(source)] = entry
    return kept


def build(input_dir: Path, output_dir: Path) -> dict:
    """把 input_dir 下的 <source>.json 全部加密，写出 index.json 并返回它。"""
    sources_dir = output_dir / "sources"
    sources_dir.mkdir(parents=True, exist_ok=True)
    key = derive_key()
    # 先继承上一轮的站源，再覆盖本轮真正产出成功的。
    entries: dict[str, dict] = previous_entries(output_dir)
    produced: set[str] = set()
    latest = ""
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
        # 只留 App 需要的字段，顺手把 id 为空的脏数据剔掉。
        cleaned = [item for item in items if isinstance(item, dict) and item.get("id")]
        if not cleaned:
            print(f"[warn] 跳过 {path.name}: 剧集缺少 id", file=sys.stderr)
            continue
        body = json.dumps(
            {
                "source": source,
                "generatedAt": payload.get("generatedAt") or _now(),
                "items": cleaned,
            },
            ensure_ascii=False,
            separators=(",", ":"),
        ).encode("utf-8")
        if len(body) > MAX_SOURCE_BYTES:
            print(f"[warn] 跳过 {path.name}: 体积超过上限", file=sys.stderr)
            continue
        blob = encrypt(body, key)
        name = f"sources/{source}.json.enc"
        (output_dir / name).write_bytes(blob)
        generated = str(payload.get("generatedAt") or _now())
        latest = max(latest, generated)
        entries[source] = {
            "file": name,
            "sha256": hashlib.sha256(blob).hexdigest(),
            "count": len(cleaned),
            "generatedAt": generated,
        }
        produced.add(source)
        print(f"[ok] {source}: {len(cleaned)} 条 -> {name} ({len(blob)} 字节)")

    for source in sorted(set(entries) - produced):
        print(f"[keep] {source}: 本轮未产出，沿用上一轮快照")

    # 索引的生成时间取所有站源里最新的那个（含沿用项）：
    # 客户端用它判断"这一版我灌过没有"。
    for entry in entries.values():
        latest = max(latest, str(entry.get("generatedAt") or ""))
    index = {
        "version": VERSION,
        "generatedAt": latest or _now(),
        "algorithm": ALGORITHM,
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
    args = parser.parse_args(argv)
    index = build(Path(args.input), Path(args.output))
    if not index["sources"]:
        print("没有任何站源被写入，快照为空", file=sys.stderr)
        return 1
    print(f"index.json: {len(index['sources'])} 个站源，生成时间 {index['generatedAt']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
