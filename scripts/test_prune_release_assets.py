import unittest

from prune_release_assets import assets_to_prune, build_of


class BuildNumberTests(unittest.TestCase):
    def test_parses_build_from_release_name(self):
        self.assertEqual(build_of('zhenguojianTV-0.8.20+101-all.apk'), 101)
        self.assertEqual(build_of('zhenguojianTV-0.8.20+100-arm64-v8a.apk'), 100)

    def test_old_naming_has_no_build(self):
        # 0.8.6 那批命名没有 `+build`，不能被误判成 build 0 然后删掉。
        self.assertIsNone(build_of('zhenguojian-TV-v0.8.6-all.apk'))
        self.assertIsNone(build_of('zhenguojian-0.8.20-all.apk'))
        self.assertIsNone(build_of('SHA256SUMS.txt'))


class PruneSelectionTests(unittest.TestCase):
    def test_prunes_only_older_builds(self):
        names = [
            'zhenguojianTV-0.8.20+100-all.apk',
            'zhenguojianTV-0.8.20+100-arm64-v8a.apk',
            'zhenguojianTV-0.8.20+101-all.apk',
            'zhenguojianTV-0.8.20+101-arm64-v8a.apk',
            'zhenguojianTV-0.8.20+101-armeabi-v7a.apk',
        ]
        victims = assets_to_prune(names, 101)
        self.assertEqual(
            victims,
            ['zhenguojianTV-0.8.20+100-all.apk', 'zhenguojianTV-0.8.20+100-arm64-v8a.apk'],
        )

    def test_keeps_everything_when_already_clean(self):
        names = [
            'zhenguojianTV-0.8.20+101-all.apk',
            'zhenguojianTV-0.8.20+101-arm64-v8a.apk',
            'zhenguojianTV-0.8.20+101-armeabi-v7a.apk',
        ]
        self.assertEqual(assets_to_prune(names, 101), [])

    def test_ignores_non_apk_files(self):
        # 校验文件 / 说明文件不是"旧批次"，删了就是丢东西。
        names = [
            'SHA256SUMS.txt',
            'release_body.md',
            'zhenguojianTV-0.8.20+100-all.apk',
        ]
        self.assertEqual(assets_to_prune(names, 101), ['zhenguojianTV-0.8.20+100-all.apk'])

    def test_ignores_names_without_build(self):
        # 老 tag 的附件没有 build 号：不能因为"解析不出"就当成 0 删掉。
        names = [
            'zhenguojian-TV-v0.8.6-all.apk',
            'zhenguojian-TV-v0.8.6-arm64-v8a.apk',
            'SHA256SUMS.txt',
        ]
        self.assertEqual(assets_to_prune(names, 101), [])

    def test_never_prunes_newer_builds(self):
        # 手动重跑旧 build 时（比如补发 +100），不能把已经更新的 +101 删掉。
        names = ['zhenguojianTV-0.8.20+101-all.apk']
        self.assertEqual(assets_to_prune(names, 100), [])

    def test_handles_empty_asset_list(self):
        self.assertEqual(assets_to_prune([], 101), [])


if __name__ == '__main__':
    unittest.main()
