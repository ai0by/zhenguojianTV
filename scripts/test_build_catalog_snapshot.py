import hashlib
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
    def _dump(self, directory: Path, source: str, items: list[dict]) -> None:
        (directory / f'{source}.json').write_text(
            json.dumps(
                {'source': source, 'generatedAt': '2026-11-02T02:00:00Z', 'items': items},
                ensure_ascii=False,
            ),
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


if __name__ == '__main__':
    unittest.main()
