import contextlib
import hashlib
import io
import json
import tempfile
import unittest
from pathlib import Path

try:  # 加密依赖不是所有环境都有，缺了就让用例跳过而不是红一片。
    import cryptography  # noqa: F401

    HAVE_CRYPTO = True
except ImportError:  # pragma: no cover - 环境相关
    HAVE_CRYPTO = False

from build_catalog_snapshot import (
    ALGORITHM,
    NONCE_SIZE,
    SECRET,
    build,
    decrypt,
    dedupe_items,
    derive_key,
    encrypt,
)


class CatalogSnapshotCryptoTests(unittest.TestCase):
    def test_key_derivation_is_stable_and_32_bytes(self):
        key = derive_key()
        self.assertEqual(len(key), 32)
        self.assertEqual(key, hashlib.sha256(SECRET.encode('utf-8')).digest())

    @unittest.skipUnless(HAVE_CRYPTO, '缺少 cryptography')
    def test_ciphertext_is_nonce_prefixed_and_round_trips(self):
        plain = b'{"source":"hongguo","items":[]}'
        blob = encrypt(plain)
        self.assertEqual(len(blob), NONCE_SIZE + len(plain) + 16)
        self.assertEqual(decrypt(blob), plain)

    @unittest.skipUnless(HAVE_CRYPTO, '缺少 cryptography')
    def test_encryption_is_deterministic(self):
        """同一份明文必须产出同一份密文。

        随机 nonce 会让每轮快照的 sha256 都不同，index.json 每 2 小时
        都是一版"新快照"，所有客户端于是每轮重下几 MB。这条断言就是
        把"内容没变就不该变"钉住。
        """
        plain = b'same input'
        first, second = encrypt(plain), encrypt(plain)
        self.assertEqual(first, second)
        self.assertEqual(decrypt(first), plain)
        # 不同明文的 nonce 不能撞（nonce 是明文的函数，但也不能退化成常量）。
        self.assertNotEqual(encrypt(b'other input')[:NONCE_SIZE], first[:NONCE_SIZE])


@unittest.skipUnless(HAVE_CRYPTO, '缺少 cryptography')
class CatalogSnapshotBuildTests(unittest.TestCase):
    def _dump(self, directory: Path, source: str, items: list[dict], truncated: bool = False, pages: int = 0) -> None:
        payload = {'source': source, 'generatedAt': '2026-11-02T02:00:00Z', 'items': items}
        if truncated:
            payload['truncated'] = True
            payload['pages'] = pages
        (directory / f'{source}.json').write_text(
            json.dumps(payload, ensure_ascii=False),
            encoding='utf-8',
        )

    def test_build_writes_encrypted_files_and_matching_index(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            incoming, output = root / 'in', root / 'out'
            incoming.mkdir()
            self._dump(
                incoming,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '甲'}, {'id': '', 'title': '脏数据'}],
            )
            index = build(incoming, output)
            self.assertEqual(index['version'], 1)
            self.assertEqual(index['algorithm'], ALGORITHM)
            # generatedAt 由脚本自己生成（不再取 dump 里的时间戳 —— 那个每轮都变，
            # 会让"内容没变就不发新版"失效）。
            self.assertRegex(index['generatedAt'], r'^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$')
            self.assertEqual(len(index['digest']), 64)
            entry = index['sources']['hongguo']
            # 缺 id 的脏数据必须被剔掉，否则 App 侧会灌进一条空剧。
            self.assertEqual(entry['count'], 1)
            # 密文里不能残留时间戳（否则确定性 nonce 也救不回来）。
            blob = (output / entry['file']).read_bytes()
            self.assertEqual(hashlib.sha256(blob).hexdigest(), entry['sha256'])
            payload = json.loads(decrypt(blob))
            self.assertEqual(payload['source'], 'hongguo')
            self.assertNotIn('generatedAt', payload)
            self.assertEqual(len(payload['items']), 1)
            # 索引必须真的落盘（CI 直接提交这个文件）。
            on_disk = json.loads((output / 'index.json').read_text(encoding='utf-8'))
            self.assertEqual(on_disk['sources']['hongguo']['sha256'], entry['sha256'])

    def test_build_deduplicates_repeated_ids_in_a_dump(self):
        """同一份 dump 里出现重复 id 时，快照里只能留一条。

        导出工具本来就会按 id 去重，但它是这条链路上最容易回归的一环；
        快照一旦带重复 id，客户端按 id 合并只会互相覆盖字段，列表里还会
        出现两张一样的卡 —— 所以发布前必须再收一遍。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            incoming, output = root / 'in', root / 'out'
            incoming.mkdir()
            self._dump(
                incoming,
                'hongguo',
                [
                    {'id': 'hongguo:1', 'title': '甲'},
                    {'id': 'hongguo:2', 'title': '乙'},
                    {'id': 'hongguo:1', 'title': '甲的重复'},
                ],
            )
            index = build(incoming, output)
            self.assertEqual(index['sources']['hongguo']['count'], 2)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            ids = [item['id'] for item in payload['items']]
            self.assertEqual(ids, ['hongguo:1', 'hongguo:2'])
            # 先出现的那条胜出（顺序也保持稳定，才谈得上"内容不变就不发新版"）。
            self.assertEqual(payload['items'][0]['title'], '甲')

    def test_dedupe_items_drops_malformed_ids(self):
        """只有前缀没有尾巴、或根本没有前缀的 id 必须丢掉。

        这种条目在 App 里既解析不出站源（点开黑屏），也没有稳定的去重键 ——
        发出去就是一张点不开的卡。
        """
        items = [
            {'id': 'hongguo:1'},
            {'id': 'hongguo:'},
            {'id': 'hongguo:   '},
            {'id': 'no-prefix'},
        ]
        buffer = io.StringIO()
        with contextlib.redirect_stderr(buffer):
            kept = dedupe_items('hongguo', items)
        self.assertEqual([item['id'] for item in kept], ['hongguo:1'])
        self.assertIn('id 不完整', buffer.getvalue())

    def test_dedupe_items_flags_foreign_prefix(self):
        """id 前缀属于别的站源时只告警、不丢数据 —— 丢掉会让用户少剧。"""
        items = [
            {'id': 'hongguo:1'},
            {'id': 'yeguo:1'},
            {'id': ''},
            {'id': 'hongguo:1'},
        ]
        buffer = io.StringIO()
        with contextlib.redirect_stderr(buffer):
            kept = dedupe_items('hongguo', items)
        self.assertEqual([item['id'] for item in kept], ['hongguo:1', 'yeguo:1'])
        output = buffer.getvalue()
        self.assertIn('重复 id', output)
        self.assertIn('前缀不是本战源', output)

    def test_unchanged_content_keeps_generated_at(self):
        """内容没变时不许换 generatedAt —— 客户端靠它判断要不要重新下载。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            incoming, output = root / 'in', root / 'out'
            incoming.mkdir()
            self._dump(incoming, 'hongguo', [{'id': 'hongguo:1', 'title': '甲'}])
            first = build(incoming, output)
            # 同一份 dump 再跑一遍：索引必须逐字节一致（CI 的 `git diff --cached`
            # 靠这个判断"没有变化就跳过提交"）。
            before = (output / 'index.json').read_bytes()
            second = build(incoming, output)
            self.assertEqual(second['generatedAt'], first['generatedAt'])
            self.assertEqual(second['digest'], first['digest'])
            self.assertEqual((output / 'index.json').read_bytes(), before)

            # 把上一版的 generatedAt 改成一个明显不是"现在"的值（digest 不动）。
            # 内容再跑一遍时如果还是这个值，说明确实是"沿用"而不是"重新生成"。
            on_disk = json.loads((output / 'index.json').read_text(encoding='utf-8'))
            on_disk['generatedAt'] = '2020-01-01T00:00:00Z'
            (output / 'index.json').write_text(
                json.dumps(on_disk, ensure_ascii=False, indent=2, sort_keys=True) + '\n',
                encoding='utf-8',
            )
            third = build(incoming, output)
            self.assertEqual(third['generatedAt'], '2020-01-01T00:00:00Z')

            # 内容真变了就必须换新时间戳。
            self._dump(
                incoming,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '甲'}, {'id': 'hongguo:2', 'title': '乙'}],
            )
            fourth = build(incoming, output)
            self.assertNotEqual(fourth['digest'], first['digest'])
            self.assertNotEqual(fourth['generatedAt'], '2020-01-01T00:00:00Z')

    def test_build_skips_sources_without_items(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            incoming, output = root / 'in', root / 'out'
            incoming.mkdir()
            self._dump(incoming, 'empty', [])
            self._dump(incoming, 'broken', [{'title': '没有 id'}])
            index = build(incoming, output)
            self.assertEqual(index['sources'], {})
            self.assertFalse((output / 'sources').exists() and any((output / 'sources').iterdir()))

    def test_second_run_keeps_sources_it_could_not_fetch(self):
        """CI 跑在境外，某一轮抓不到某个站源时不能把它从快照里抹掉。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'hongguo', [{'id': 'hongguo:1', 'title': '甲'}])
            self._dump(first, 'yeguo', [{'id': 'yeguo:9', 'title': '乙'}])
            build(first, output)
            # 第二轮只有 hongguo 抓到。
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hongguo', [{'id': 'hongguo:2', 'title': '丙'}])
            index = build(second, output)
            self.assertEqual(set(index['sources']), {'hongguo', 'yeguo'})
            self.assertEqual(index['sources']['yeguo']['count'], 1)
            self.assertTrue((output / index['sources']['yeguo']['file']).is_file())

    def test_merge_appends_previous_items_after_fresh_ones(self):
        """增量模式：只翻前几页也不会把后面的剧丢掉。

        这是 0.8.20 的核心：站点风控受不了每 2 小时整站翻 60 页，于是定时
        任务只翻 3 页，剩下的靠上一轮快照补。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            full = root / 'full'
            full.mkdir()
            self._dump(
                full,
                'hongguo',
                [
                    {'id': 'hongguo:1', 'title': '甲'},
                    {'id': 'hongguo:2', 'title': '乙'},
                    {'id': 'hongguo:3', 'title': '丙'},
                ],
            )
            build(full, output)
            # 增量轮：只抓到一部新剧（真实场景里是新上架的排在最前面）。
            fresh = root / 'fresh'
            fresh.mkdir()
            self._dump(fresh, 'hongguo', [{'id': 'hongguo:4', 'title': '丁'}])
            index = build(fresh, output, merge=True)
            self.assertEqual(index['sources']['hongguo']['count'], 4)
            blob = (output / index['sources']['hongguo']['file']).read_bytes()
            payload = json.loads(decrypt(blob))
            # 新剧在前，老剧按原顺序跟在后面。
            self.assertEqual(
                [item['id'] for item in payload['items']],
                ['hongguo:4', 'hongguo:1', 'hongguo:2', 'hongguo:3'],
            )

    def test_merge_deduplicates_by_id_and_prefers_fresh_fields(self):
        """同一部剧两轮都有时只留一份，且用本轮抓到的字段（标题可能改过）。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(
                first,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '旧标题'}, {'id': 'hongguo:2', 'title': '乙'}],
            )
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hongguo', [{'id': 'hongguo:1', 'title': '新标题'}])
            index = build(second, output, merge=True)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            self.assertEqual([item['id'] for item in payload['items']], ['hongguo:1', 'hongguo:2'])
            self.assertEqual(payload['items'][0]['title'], '新标题')
            self.assertEqual(index['sources']['hongguo']['count'], 2)

    def test_merge_is_byte_stable_across_runs(self):
        """同一份输入连跑两遍，增量产物必须逐字节一致。

        否则 digest 每 6 小时都变 → 所有客户端每轮重下几 MB，
        0.8.19 的"内容不变就不发新版"直接作废。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            full = root / 'full'
            full.mkdir()
            self._dump(
                full,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '甲'}, {'id': 'hongguo:2', 'title': '乙'}],
            )
            build(full, output)
            fresh = root / 'fresh'
            fresh.mkdir()
            self._dump(fresh, 'hongguo', [{'id': 'hongguo:3', 'title': '丙'}])
            first = build(fresh, output, merge=True)
            before = (output / 'index.json').read_bytes()
            second = build(fresh, output, merge=True)
            self.assertEqual(second['digest'], first['digest'])
            self.assertEqual(second['generatedAt'], first['generatedAt'])
            self.assertEqual((output / 'index.json').read_bytes(), before)
            self.assertEqual(second['sources']['hongguo']['count'], 3)

    def test_full_mode_replaces_instead_of_merging(self):
        """全量模式必须真的全量：抓到的就是全部，不许把上一轮的残留带进来。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(
                first,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '甲'}, {'id': 'hongguo:2', 'title': '乙'}],
            )
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hongguo', [{'id': 'hongguo:9', 'title': '新'}])
            index = build(second, output, merge=False)
            self.assertEqual(index['sources']['hongguo']['count'], 1)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            self.assertEqual([item['id'] for item in payload['items']], ['hongguo:9'])

    def test_full_mode_keeps_previous_when_fresh_only_repeats_old_items(self):
        """全量轮一条新剧都没抓到 → 判定为接口截断，保留上一轮。

        红果的 App 分类接口同一天四轮分别给 1331 / 2803 / 3217 / 3595 条，
        两两交集才一千多 —— 每次都说"翻到底了"，其实是不同子集。如果哪一轮
        碰巧只吐了一个小子集，全量模式就会把 2000 部的站源缩成几十部，
        用户那边本机反而比远端多。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(
                first,
                'hongguo',
                [{'id': 'hongguo:1', 'title': '甲'}, {'id': 'hongguo:2', 'title': '乙'}],
            )
            build(first, output)
            second = root / 'second'
            second.mkdir()
            # 只抓到上一轮就有的那部，没有任何新剧。
            self._dump(second, 'hongguo', [{'id': 'hongguo:2', 'title': '乙'}])
            index = build(second, output, merge=False)
            self.assertEqual(index['sources']['hongguo']['count'], 2)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            self.assertEqual([item['id'] for item in payload['items']], ['hongguo:2', 'hongguo:1'])

    def test_full_mode_allows_genuine_shrink(self):
        """真·缩水（有新剧、老剧下架）照旧按本轮写，别被截断保护挡住。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(
                first,
                'hongguo',
                [
                    {'id': 'hongguo:1', 'title': '甲'},
                    {'id': 'hongguo:2', 'title': '乙'},
                    {'id': 'hongguo:3', 'title': '丙'},
                ],
            )
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hongguo', [{'id': 'hongguo:3', 'title': '丙'}, {'id': 'hongguo:4', 'title': '新'}])
            index = build(second, output, merge=False)
            self.assertEqual(index['sources']['hongguo']['count'], 2)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            self.assertEqual([item['id'] for item in payload['items']], ['hongguo:3', 'hongguo:4'])

    def test_merge_falls_back_when_previous_file_is_corrupt(self):
        """上一轮快照解不开时只降级成"本轮抓到的"，不能让整轮崩掉。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'hongguo', [{'id': 'hongguo:1', 'title': '甲'}])
            build(first, output)
            (output / 'sources/hongguo.json.enc').write_bytes(b'not a ciphertext')
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hongguo', [{'id': 'hongguo:2', 'title': '乙'}])
            index = build(second, output, merge=True)
            self.assertEqual(index['sources']['hongguo']['count'], 1)
            payload = json.loads(decrypt((output / index['sources']['hongguo']['file']).read_bytes()))
            self.assertEqual([item['id'] for item in payload['items']], ['hongguo:2'])

    def test_truncated_dump_does_not_shrink_snapshot(self):
        """截断的 dump 不许覆盖更全的旧快照（0.8.24）。

        这是"手机端比远端全"的主因：网果在境外 runner 恒 403，快照只能靠
        国内手工 dump；那次用了 `-pages 1600` 而站点有 2116 页，写进快照的
        是 38349 条（真实 50767 条）。旧逻辑只看"有没有新剧"，截断的 dump
        一旦带上没见过的 id 就会被当成完整结果写进去。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            # 上一轮：全量 5 条。
            self._dump(first, 'wangguo', [{'id': f'wangguo:{n}', 'title': f'剧{n}'} for n in range(1, 6)])
            build(first, output)
            second = root / 'second'
            second.mkdir()
            # 本轮：撞到翻页上限，只有 2 条（且带一个新 id 7，旧逻辑会当成"有新增"）。
            self._dump(second, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}, {'id': 'wangguo:7', 'title': '剧7'}],
                       truncated=True, pages=1600)
            index = build(second, output, merge=False)
            # 必须保留上一轮的 5 条，而不是缩成 2 条。
            self.assertEqual(index['sources']['wangguo']['count'], 5)
            payload = json.loads(decrypt((output / index['sources']['wangguo']['file']).read_bytes()))
            self.assertEqual(len(payload['items']), 5)

    def test_truncated_dump_replaces_when_it_is_larger(self):
        """截断但条数更多时仍按本轮写 —— 只要比旧的更全就换。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}])
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'wangguo', [{'id': f'wangguo:{n}', 'title': f'剧{n}'} for n in range(1, 4)],
                       truncated=True, pages=1600)
            index = build(second, output, merge=False)
            self.assertEqual(index['sources']['wangguo']['count'], 3)

    def test_truncated_flag_is_persisted_and_inherited(self):
        """截断标记要写进 index，并在本轮没产出时继续挂着。

        网果这类站源永远抓不全：标记丢了，运维就再也看不出哪个站源残缺，
        下一轮沿用这份文件时也丢了"它不完整"这个事实。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}], truncated=True, pages=1600)
            index = build(first, output)
            self.assertTrue(index['sources']['wangguo'].get('truncated'))

            # 第二轮完全没产出（境外 403），标记必须继承下来。
            empty = root / 'empty'
            empty.mkdir()
            index = build(empty, output, merge=True)
            self.assertTrue(index['sources']['wangguo'].get('truncated'))
            self.assertEqual(index['sources']['wangguo']['count'], 1)

    def test_complete_dump_clears_truncated_flag(self):
        """真的抓全了就该把标记摘掉，否则它会永远挂着误导人。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}], truncated=True, pages=1600)
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}, {'id': 'wangguo:2', 'title': '剧2'}])
            index = build(second, output, merge=False)
            self.assertFalse(index['sources']['wangguo'].get('truncated'))

    def test_incremental_partial_dump_does_not_mark_healthy_source(self):
        """增量只翻 3 页必然撞上限，不该把健康源也标成残缺（2026-11-10）。

        `catalogdump -pages 3` 下每个站源都会回 `truncated=True`，但增量有
        `--merge` 拿上一轮全量按 id 补齐，并不残缺。照搬进 index 的话，
        十几个能抓的源会永远挂着 True，运维再也分不出真正残缺的境外 403 源。
        增量模式应沿用上一版标记：健康的继续健康，真残血继续挂着。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / 'out'
            first = root / 'first'
            first.mkdir()
            self._dump(first, 'hema', [{'id': 'hema:1', 'title': '剧1'}])
            build(first, output)
            second = root / 'second'
            second.mkdir()
            self._dump(second, 'hema', [{'id': 'hema:2', 'title': '剧2'}],
                       truncated=True, pages=3)
            index = build(second, output, merge=True)
            self.assertFalse(index['sources']['hema'].get('truncated'))
            # 标记归标记，合并照旧生效：上一轮的剧不能丢。
            self.assertEqual(index['sources']['hema']['count'], 2)

            # 上一版确实残血的源，即使本轮增量产出了，标记也继续挂着。
            third = root / 'third'
            third.mkdir()
            self._dump(third, 'wangguo', [{'id': 'wangguo:1', 'title': '剧1'}],
                       truncated=True, pages=1600)
            build(third, output, merge=False)
            fourth = root / 'fourth'
            fourth.mkdir()
            self._dump(fourth, 'wangguo', [{'id': 'wangguo:2', 'title': '剧2'}],
                       truncated=True, pages=3)
            index = build(fourth, output, merge=True)
            self.assertTrue(index['sources']['wangguo'].get('truncated'))
            self.assertEqual(index['sources']['wangguo']['count'], 2)


if __name__ == '__main__':
    unittest.main()
