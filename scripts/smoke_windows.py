import argparse
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
import zipfile
from pathlib import Path

from app_build import BuildVariant, add_variant_argument

root = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    add_variant_argument(parser)
    variant = BuildVariant(parser.parse_args().all_sources)
    if platform.system() != 'Windows':
        raise SystemExit('此检查需要 Windows。')
    version = re.search(r'^version:\s*(\S+)', (root / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE).group(1)
    package = root / 'dist' / 'windows' / f'{variant.slug}-{version}-windows-x64.zip'
    with tempfile.TemporaryDirectory(prefix='zhenguojian-smoke-') as temporary:
        directory = Path(temporary)
        with zipfile.ZipFile(package) as archive:
            archive.extractall(directory)
        media = directory / 'fixture.mp4'
        # ffmpeg 只用来造一段 3 秒测试片，不是产品依赖。CI 里由 MSYS2 提供；
        # 本机跑的时候可以用 ZGJ_FFMPEG 指到任意一份 ffmpeg.exe。
        ffmpeg = os.environ.get('ZGJ_FFMPEG') or shutil.which('ffmpeg')
        if not ffmpeg:
            raise SystemExit(
                '启动验收需要 ffmpeg 生成 3 秒测试片：请把 ffmpeg.exe 加入 PATH，'
                '或用 ZGJ_FFMPEG 指定路径（CI 里由 msys2 安装 mingw-w64-x86_64-ffmpeg）。'
            )
        # mpeg4 是 FFmpeg 任何精简构建都带的编码器；原来写 libx264，遇到
        # GPL-free / 精简版 ffmpeg 会直接报 Unknown encoder，验收还没开始就挂了。
        subprocess.run([ffmpeg, '-v', 'error', '-y', '-f', 'lavfi',
                        '-i', 'testsrc2=size=160x90:rate=12', '-t', '3',
                        '-c:v', 'mpeg4', '-q:v', '5', '-threads', '1', str(media)],
                       check=True)
        report = directory / 'result.json'
        try:
            result = subprocess.run([str(directory / (variant.slug + '.exe')), '--package-smoke', str(report), str(media)],
                                    cwd=directory, check=False, timeout=90)
            missing = '未生成检查结果，退出码 ' + str(result.returncode)
        except subprocess.TimeoutExpired:
            missing = '启动验收进程 90 秒未退出'
        if report.is_file():
            evidence = json.loads(report.read_text(encoding='utf-8'))
        else:
            evidence = {'ok': False, 'error': missing}
        # 无论通过与否都落盘：原来只在成功时写，失败时
        # build/windows-package-smoke.json 根本不存在，job summary 与日志里
        # 只剩一行笼统的失败提示，看不到播放器状态诊断。
        output = root / 'build' / 'windows-package-smoke.json'
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(evidence, indent=2) + '\n', encoding='utf-8')
        # CI 的 pwsh 把子进程 stdout 按 cp1252 解码，报告里一旦有中文
        # （例如 clockNote），`ensure_ascii=False` 会让 print 抛 UnicodeEncodeError，
        # 验收明明通过却以退出码 1 收场。日志统一输出纯 ASCII 转义。
        print(json.dumps(evidence, ensure_ascii=True))
        if evidence.get('ok') is not True:
            raise SystemExit('Windows package smoke failed: ' + json.dumps(evidence, ensure_ascii=True))


if __name__ == '__main__':
    main()
