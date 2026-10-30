#!/usr/bin/env python3
"""把最新 Release 的信息写进 config/update.json（0.8.18 远程升级用）。

为什么要有这个脚本：客户端检查更新时，如果 `api.github.com` 连不上，会退回
jsDelivr 上的 `config/update.json`。这个文件必须跟每个 Release 保持同步，
否则兜底通道会给出过期的版本号；而 sha256 又必须来自 GitHub 给的 digest，
**手写必然出错**，所以交给发布流程自动生成。

用法：python3 scripts/write_update_config.py <tag>
环境变量：GITHUB_TOKEN（可选，只影响限流）、GITHUB_REPOSITORY（默认本仓库）
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request

DEFAULT_REPO = "ai0by/zhenguojianTV"
CONFIG_PATH = os.path.join("config", "update.json")
API = "https://api.github.com"

# 实测排序（0.8.18）：gh-proxy.org 最快且支持真实续传。
DEFAULT_MIRRORS = [
    "https://gh-proxy.org/{url}",
    "https://v6.gh-proxy.org/{url}",
    "https://ghproxy.net/{url}",
    "https://v4.gh-proxy.org/{url}",
    "https://gh-proxy.com/{url}",
]

# 文件名里的 ABI 后缀 → 配置里的 abi 字段。
ABI_SUFFIXES = ("arm64-v8a", "armeabi-v7a", "x86_64", "x86", "all")


def fetch_release(repo: str, tag: str) -> dict:
    url = f"{API}/repos/{repo}/releases/tags/{tag}"
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/vnd.github+json")
    token = os.environ.get("GITHUB_TOKEN", "").strip()
    if token:
        request.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.loads(response.read().decode("utf-8"))


def abi_of(name: str) -> str:
    stem = name[:-4] if name.lower().endswith(".apk") else name
    for abi in ABI_SUFFIXES:
        if stem.endswith("-" + abi):
            return abi
    return ""


def build_of(name: str) -> int:
    marker = name.find("+")
    if marker < 0:
        return 0
    digits = ""
    for char in name[marker + 1 :]:
        if char.isdigit():
            digits += char
        else:
            break
    return int(digits) if digits else 0


def summary_of(body: str) -> str:
    """取发布说明的第一行当一句话简介（去掉 markdown 标题井号）。"""
    for line in body.splitlines():
        text = line.strip().lstrip("#").strip()
        if text:
            return text[:80]
    return ""


def existing_mirrors() -> list[str]:
    try:
        with open(CONFIG_PATH, encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, ValueError):
        return list(DEFAULT_MIRRORS)
    mirrors = data.get("mirrors")
    if isinstance(mirrors, list) and mirrors:
        return [item for item in mirrors if isinstance(item, str)]
    return list(DEFAULT_MIRRORS)


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: write_update_config.py <tag>", file=sys.stderr)
        return 2
    tag = sys.argv[1].strip()
    repo = os.environ.get("GITHUB_REPOSITORY", DEFAULT_REPO).strip() or DEFAULT_REPO

    try:
        release = fetch_release(repo, tag)
    except urllib.error.HTTPError as error:
        print(f"读取 Release 失败: {error.code}", file=sys.stderr)
        return 1

    version = release.get("tag_name", tag).lstrip("vV")
    assets = []
    build = 0
    for asset in release.get("assets", []):
        name = asset.get("name", "")
        abi = abi_of(name)
        if not abi:
            continue
        if build == 0:
            build = build_of(name)
        digest = asset.get("digest") or ""
        if digest.startswith("sha256:"):
            digest = digest[len("sha256:") :]
        if not digest:
            # 没有 digest 就不写这条：宁可让客户端少一个变体，也不能给它一个
            # 假的校验值 —— 校验失败会把好包丢掉。
            print(f"跳过没有 digest 的附件: {name}", file=sys.stderr)
            continue
        assets.append(
            {
                "abi": abi,
                "name": name,
                "size": int(asset.get("size") or 0),
                "sha256": digest.lower(),
            }
        )

    if not assets:
        print("这个 Release 没有可用的 APK 附件", file=sys.stderr)
        return 1

    document = {
        "version": 1,
        "ttlSeconds": 21600,
        "latest": {
            "version": version,
            "build": build,
            "tag": tag,
            "notes": summary_of(release.get("body") or ""),
            "assets": assets,
        },
        "mirrors": existing_mirrors(),
    }

    os.makedirs(os.path.dirname(CONFIG_PATH), exist_ok=True)
    with open(CONFIG_PATH, "w", encoding="utf-8") as handle:
        json.dump(document, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    print(f"已写入 {CONFIG_PATH}: {version} (+{build}), {len(assets)} 个变体")
    return 0


if __name__ == "__main__":
    sys.exit(main())
