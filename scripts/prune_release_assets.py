"""清掉同一个 Release 里被新一批构建取代的旧附件。

为什么需要它：同一个 tag 会连着发好几批（`+99` / `+100` / `+101`），
`softprops/action-gh-release` 只会上传、从不删除，于是 Release 页面上堆着
好几套同名 APK。客户端不受影响（`config/update.json` 按 abi 只取 build 最大的），
但**手动点进 Release 页面的人会选到旧包**，装完发现没有修复，然后来问"为什么
没生效"。

规则：同一 tag 下，build 号小于当前这版的一律删掉；解析不出 build 号的文件
（老命名 `zhenguojian-TV-v0.8.6-all.apk`、`SHA256SUMS.txt`）**一律不动** ——
那些不是"同一版本的旧批次"，删了就是丢历史。

用法（CI 里由 build.yml 调用）：

    python3 scripts/prune_release_assets.py --repo owner/name --tag v0.8.20 --keep-build 101
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

# 文件名形如 `zhenguojianTV-0.8.20+101-all.apk`。只认 `+<数字>-` 这一段，
# 老命名没有 build 号，自然匹配不上。
BUILD_PATTERN = re.compile(r'\+(\d+)-')
API = 'https://api.github.com'


def build_of(name: str) -> int | None:
    match = BUILD_PATTERN.search(name)
    return int(match.group(1)) if match else None


def assets_to_prune(names, keep_build: int) -> list[str]:
    """挑出该删的附件名。

    只删「是 apk、且能解析出 build 号、且 build 号比当前小」的；
    其余（非 apk、解析不出 build 号、build 号更大）全部保留。
    """
    victims: list[str] = []
    for name in names:
        if not str(name).lower().endswith('.apk'):
            continue
        build = build_of(name)
        if build is None or build >= keep_build:
            continue
        victims.append(name)
    return victims


def _request(url: str, token: str, method: str = 'GET'):
    request = urllib.request.Request(url, method=method)
    request.add_header('Authorization', f'Bearer {token}')
    request.add_header('Accept', 'application/vnd.github+json')
    request.add_header('User-Agent', 'zhenguojianTV-prune')
    return urllib.request.urlopen(request, timeout=60)


def _list_assets(repo: str, tag: str, token: str) -> list[dict]:
    with _request(f'{API}/repos/{repo}/releases/tags/{tag}', token) as response:
        release = json.loads(response.read().decode('utf-8'))
    assets: list[dict] = []
    page = 1
    while True:
        url = f"{API}/repos/{repo}/releases/{release['id']}/assets?per_page=100&page={page}"
        with _request(url, token) as response:
            batch = json.loads(response.read().decode('utf-8'))
        if not batch:
            return assets
        assets.extend(batch)
        if len(batch) < 100:
            return assets
        page += 1


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='删除同 tag 下被取代的旧 APK 附件')
    parser.add_argument('--repo', required=True, help='owner/name')
    parser.add_argument('--tag', required=True, help='Release tag，例如 v0.8.20')
    parser.add_argument('--keep-build', type=int, required=True, help='要保留的 build 号')
    parser.add_argument('--dry-run', action='store_true', help='只打印不删除')
    args = parser.parse_args(argv)

    token = os.environ.get('GITHUB_TOKEN') or ''
    if not token:
        print('缺少 GITHUB_TOKEN，跳过清理', file=sys.stderr)
        return 0

    try:
        assets = _list_assets(args.repo, args.tag, token)
    except urllib.error.HTTPError as exc:
        print(f'读不到 {args.tag} 的附件（HTTP {exc.code}），跳过清理', file=sys.stderr)
        return 0

    by_name = {asset['name']: asset for asset in assets}
    victims = assets_to_prune(list(by_name), args.keep_build)
    if not victims:
        print(f'{args.tag} 没有需要清理的旧附件（保留 build {args.keep_build}）')
        return 0

    failed = 0
    for name in victims:
        asset_id = by_name[name]['id']
        if args.dry_run:
            print(f'[dry-run] 会删除 {name}')
            continue
        try:
            with _request(f'{API}/repos/{args.repo}/releases/assets/{asset_id}', token, method='DELETE') as _:
                pass
            print(f'[prune] 删除 {name}')
        except urllib.error.HTTPError as exc:
            failed += 1
            print(f'[warn] 删除 {name} 失败：HTTP {exc.code}', file=sys.stderr)

    print(f'{args.tag}: 清理 {len(victims) - failed} 个旧附件，保留 build {args.keep_build}')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
