import json
import tempfile
import unittest
from pathlib import Path

from merge_catalog_dumps import _pass_order, main, union_passes


def _dump(directory: Path, source: str, items: list[dict]) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    (directory / f'{source}.json').write_text(
        json.dumps(
            {'source': source, 'generatedAt': '2026-11-02T02:00:00Z', 'items': items},
            ensure_ascii=False,
        ),
        encoding='utf-8',
    )


class MergeCatalogDumpsTests(unittest.TestCase):
    def test_pass_order_is_numeric_not_lexicographic(self):
        """`pass-10` 必须排在 `pass-2` 后面，否则并集顺序每轮都变。"""
        names = [Path('pass-2'), Path('pass-10'), Path('pass-1')]
        ordered = [path.name for path in sorted(names, key=_pass_order)]
        self.assertEqual(ordered, ['pass-1', 'pass-2', 'pass-10'])

    def test_union_merges_slices_from_every_pass(self):
        """红果每轮给的切片不同，并集才是全站。

        实测：同一天四轮 1331 / 2803 / 3217 / 3595 条，两两交集才一千多。
        单轮当全量就会少掉一大半。
        """
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'pass-1', 'hongguo', [{'id': 'a'}, {'id': 'b'}])
            _dump(root / 'pass-2', 'hongguo', [{'id': 'c'}, {'id': 'a'}])
            merged = union_passes([root / 'pass-1', root / 'pass-2'])
            self.assertEqual([item['id'] for item in merged['hongguo']], ['a', 'b', 'c'])

    def test_union_is_stable_across_runs(self):
        """同一组输入必须得到同一份明文 —— 否则 digest 每轮都变。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'pass-1', 'hongguo', [{'id': 'a'}, {'id': 'b'}])
            _dump(root / 'pass-2', 'hongguo', [{'id': 'b'}, {'id': 'c'}])
            first = union_passes([root / 'pass-1', root / 'pass-2'])
            second = union_passes([root / 'pass-1', root / 'pass-2'])
            self.assertEqual(first, second)

    def test_union_drops_items_without_id(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'pass-1', 'hongguo', [{'id': 'a'}, {'id': ''}, {'title': '脏数据'}])
            merged = union_passes([root / 'pass-1'])
            self.assertEqual([item['id'] for item in merged['hongguo']], ['a'])

    def test_union_keeps_sources_only_some_passes_saw(self):
        """某一轮失败导致某站源缺失时，别的轮次的结果不能被丢掉。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'pass-1', 'hongguo', [{'id': 'a'}])
            _dump(root / 'pass-2', 'yeguo', [{'id': 'y'}])
            merged = union_passes([root / 'pass-1', root / 'pass-2'])
            self.assertEqual(set(merged), {'hongguo', 'yeguo'})

    def test_main_writes_one_file_per_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'passes' / 'pass-1', 'hongguo', [{'id': 'a'}])
            _dump(root / 'passes' / 'pass-2', 'hongguo', [{'id': 'b'}])
            out = root / 'out'
            code = main(['--input', str(root / 'passes'), '--output', str(out)])
            self.assertEqual(code, 0)
            payload = json.loads((out / 'hongguo.json').read_text(encoding='utf-8'))
            self.assertEqual([item['id'] for item in payload['items']], ['a', 'b'])
            self.assertEqual(payload['source'], 'hongguo')
            # 明文里不能带时间戳式的字段给下游比较（generatedAt 只给人工看，
            # build_catalog_snapshot 会忽略它）。
            self.assertIn('generatedAt', payload)

    def test_main_rejects_missing_input(self):
        with tempfile.TemporaryDirectory() as temporary:
            code = main(['--input', str(Path(temporary) / 'nope'), '--output', temporary])
            self.assertEqual(code, 2)

    def test_baseline_appends_previous_items(self):
        """给没有多轮抓取的站源保底：上一轮导出里的条目追加在末尾。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _dump(root / 'passes' / 'pass-1', 'hongguo', [{'id': 'a'}])
            _dump(root / 'base', 'hongguo', [{'id': 'a'}, {'id': 'old'}])
            out = root / 'out'
            code = main(
                ['--input', str(root / 'passes'), '--output', str(out), '--baseline', str(root / 'base')]
            )
            self.assertEqual(code, 0)
            payload = json.loads((out / 'hongguo.json').read_text(encoding='utf-8'))
            self.assertEqual([item['id'] for item in payload['items']], ['a', 'old'])


if __name__ == '__main__':
    unittest.main()
