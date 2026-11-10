import base64
import os
from pathlib import Path
import plistlib
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from app_build import BuildVariant
from configure_ios_branding import configure


def dart_defines(*values):
    return ','.join(base64.b64encode(value.encode()).decode() for value in values)


class AppBuildTests(unittest.TestCase):
    def test_omitted_or_false_flag_keeps_hongguo_only(self):
        for encoded in ['', dart_defines('ALL_SOURCES=false'), dart_defines('OTHER=true')]:
            variant = BuildVariant.from_dart_defines(encoded)
            self.assertFalse(variant.all_sources)
            self.assertEqual(variant.name, '红果鉴')
            self.assertEqual(variant.slug, 'hongguojian')

    def test_full_edition_decodes_among_other_flutter_defines(self):
        variant = BuildVariant.from_dart_defines(dart_defines(
            'OTHER=中文', 'ALL_SOURCES=true', 'VALUE=a=b'))
        self.assertTrue(variant.all_sources)
        self.assertEqual(variant.name, '真果鉴')
        self.assertEqual(variant.slug, 'zhenguojian')

    def test_ios_branding_can_switch_editions_without_replacing_bundle_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'Info.plist'
            original = {'CFBundleIdentifier': 'com.duanju.duanjuApp', 'CFBundleVersion': '9'}
            path.write_bytes(plistlib.dumps(original))
            for enabled in [True, False]:
                configure(path, dart_defines('ALL_SOURCES=' + str(enabled).lower()))
                actual = plistlib.loads(path.read_bytes())
                self.assertEqual(actual['CFBundleDisplayName'], '真果鉴' if enabled else '红果鉴')
                self.assertEqual(actual['CFBundleName'], 'zhenguojian' if enabled else 'hongguojian')
                for key, value in original.items():
                    self.assertEqual(actual[key], value)

    @unittest.skipUnless(shutil.which('cmake'), 'CMake is unavailable')
    def test_windows_reads_defines_from_flutter_tool_environment(self):
        branding = Path(__file__).resolve().parents[1] / 'windows/runner/app_branding.cmake'
        for flags, expected in [
            ([], '红果鉴'),
            # 0.8.10 起全源版的显示名是「真果鉴TV」，与 lib/app_build.dart 的
            # appName 保持一致；红果鉴那条分发线不动。
            (['ALL_SOURCES=true'], '真果鉴TV'),
            (['OTHER=true', 'ALL_SOURCES=false'], '红果鉴'),
            (['ALL_SOURCES=true', 'ALL_SOURCES=false'], '红果鉴'),
        ]:
            with self.subTest(flags=flags), tempfile.TemporaryDirectory() as temporary:
                script = Path(temporary) / 'check.cmake'
                result = Path(temporary) / 'name.txt'
                script.write_text(
                    'list(APPEND FLUTTER_TOOL_ENVIRONMENT "OTHER=1" "DART_DEFINES=' + dart_defines(*flags) + '")\n'
                    'include("' + branding.as_posix() + '")\n'
                    'file(WRITE "' + result.as_posix() + '" "${APP_DISPLAY_NAME}")\n',
                    encoding='utf-8',
                )
                subprocess.run(['cmake', '-P', str(script)], check=True, capture_output=True)
                self.assertEqual(result.read_text(encoding='utf-8'), expected)

    def test_android_and_windows_propagate_one_edition_to_core_flutter_and_package(self):
        root = Path(__file__).resolve().parent
        for target in ['android', 'windows']:
            for enabled in [False, True]:
                with self.subTest(target=target, all_sources=enabled):
                    script = root / f'build_{target}.py'
                    arguments = [str(script)] + (['--all-sources'] if enabled else [])
                    with mock.patch.object(sys, 'argv', arguments), \
                            mock.patch.dict(os.environ, {'PATH': '/tools'}, clear=True), \
                            mock.patch('shutil.which', return_value='/tools/flutter'), \
                            mock.patch('subprocess.run') as run:
                        runpy.run_path(str(script), run_name='__main__')
                    calls = [call.args[0] for call in run.call_args_list]
                    native = next(call for call in calls if any(str(arg).endswith('build_native.py') for arg in call))
                    flutter = next(call for call in calls if 'build' in call)
                    package = next(call for call in calls if any(str(arg).endswith('package_release.py') for arg in call))
                    self.assertEqual('--all-sources' in native, enabled)
                    self.assertEqual('--all-sources' in package, enabled)
                    self.assertIn('--dart-define=ALL_SOURCES=' + str(enabled).lower(), flutter)
                    self.assertIn('core.buildAllSources=' + str(enabled).lower(), BuildVariant(enabled).linker_flags)

    def test_android_tv_universal_builds_split_and_all_apks(self):
        root = Path(__file__).resolve().parent
        script = root / 'build_android.py'
        arguments = [str(script), '--all-sources', '--tv', '--universal']
        with mock.patch.object(sys, 'argv', arguments), \
                mock.patch.dict(os.environ, {'PATH': '/tools'}, clear=True), \
                mock.patch('shutil.which', return_value='/tools/flutter'), \
                mock.patch('subprocess.run') as run:
            runpy.run_path(str(script), run_name='__main__')

        calls = [call.args[0] for call in run.call_args_list]
        flutter_builds = [call for call in calls if call[:3] == ['/tools/flutter', 'build', 'apk']]
        self.assertEqual(len(flutter_builds), 2)
        split = next(call for call in flutter_builds if '--split-per-abi' in call)
        universal = next(call for call in flutter_builds if '--split-per-abi' not in call)
        for call in (split, universal):
            self.assertIn('--dart-define=ALL_SOURCES=true', call)
            self.assertIn('android-arm64,android-arm', call)
        # 0.8.10：通用包必须**先**出，且只打 ARM 两套 ABI。
        # 先出是为了让 app-release.apk 一定是这次的产物（随后的分包构建
        # 只写 app-<abi>-release.apk）；ABI 白名单是为了不把依赖 AAR 带来的
        # 半套 lib/x86_64 打进通用包 —— 那会让安装器判"解析软件包失败"。
        self.assertIs(flutter_builds[0], universal)
        universal_env = next(
            call.kwargs['env'] for call in run.call_args_list
            if call.args[0] == universal)
        self.assertEqual(
            universal_env.get('ORG_GRADLE_PROJECT_universalAbiFilters'),
            'arm64-v8a,armeabi-v7a')
        # 通用包 versionCode 要抬高：Flutter 给分包加了 1000/2000/3000 偏移，
        # 通用包不加就会比分包低，装过再装被判版本降级。
        self.assertEqual(
            universal_env.get('ORG_GRADLE_PROJECT_universalVersionCodeOffset'),
            '4000')
        split_env = next(
            call.kwargs['env'] for call in run.call_args_list
            if call.args[0] == split)
        self.assertNotIn('ORG_GRADLE_PROJECT_universalAbiFilters', split_env)
        self.assertNotIn('ORG_GRADLE_PROJECT_universalVersionCodeOffset', split_env)

        package = next(call for call in calls if any(str(arg).endswith('package_release.py') for arg in call))
        self.assertIn('--universal', package)
        self.assertEqual(package.count('--abi'), 2)
        self.assertIn('arm64-v8a', package)
        self.assertIn('armeabi-v7a', package)


if __name__ == '__main__':
    unittest.main()
