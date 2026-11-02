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
    def test_each_encryption_uses_a_fresh_nonce(self):
        plain = b'same input'
        self.assertNotEqual(encrypt(plain)[:NONCE_SIZE], encrypt(plain)[:NONCE_SIZE])


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
            self.assertEqual(index['generatedAt'], '2026-11-02T02:00:00Z')
            entry = index['sources']['hongguo']
            # 缺 id 的脏数据必须被剔掉，否则 App 侧会灌进一条空剧。
            self.assertEqual(entry['count'], 1)
            blob = (output / entry['file']).read_bytes()
            self.assertEqual(hashlib.sha256(blob).hexdigest(), entry['sha256'])
            payload = json.loads(decrypt(blob))
            self.assertEqual(payload['source'], 'hongguo')
            self.assertEqual(len(payload['items']), 1)
            # 索引必须真的落盘（CI 直接提交这个文件）。
            on_disk = json.loads((output / 'index.json').read_text(encoding='utf-8'))
            self.assertEqual(on_disk['sources']['hongguo']['sha256'], entry['sha256'])

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
