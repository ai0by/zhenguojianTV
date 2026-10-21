from pathlib import Path

source_path = Path('scripts/apply_verified_fullplay_filter.py')
source = source_path.read_text()
old = '''def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old not in text:
        raise RuntimeError(f'missing block: {label}')
    return text.replace(old, new, 1)
'''
new = '''def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old in text:
        return text.replace(old, new, 1)
    # Some patch literals intentionally use raw Python strings so Go regexp
    # backslashes survive. Normalize only indentation/newline escapes for matching.
    normalized_old = old.replace('\\\\t', '\\t').replace('\\\\n', '\\n')
    normalized_new = new.replace('\\\\t', '\\t').replace('\\\\n', '\\n')
    if normalized_old in text:
        return text.replace(normalized_old, normalized_new, 1)
    raise RuntimeError(f'missing block: {label}')
'''
if old not in source:
    raise SystemExit('replace_once helper not found')
source = source.replace(old, new, 1)
exec(compile(source, str(source_path), 'exec'), {'__name__': '__main__'})
