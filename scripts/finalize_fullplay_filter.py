from pathlib import Path

# Xiangjiao: serialize guest-session refresh so concurrent catalog certification
# reuses one self-issued guest token instead of creating several sessions.
p = Path('native/core/provider_xiangjiao.go')
s = p.read_text()
old = 'var xiangjiaoPlayableCache sync.Map\n'
new = 'var (\n\txiangjiaoPlayableCache  sync.Map\n\txiangjiaoGuestRefreshMu sync.Mutex\n)\n'
if old not in s:
    raise SystemExit('xiangjiao cache marker not found')
s = s.replace(old, new, 1)
sig = 'func (d *Downloader) xiangjiaoGuestToken(ctx context.Context, force bool) (string, error) {\n'
repl = sig + '\txiangjiaoGuestRefreshMu.Lock()\n\tdefer xiangjiaoGuestRefreshMu.Unlock()\n'
if sig not in s:
    raise SystemExit('xiangjiao guest token signature not found')
s = s.replace(sig, repl, 1)
p.write_text(s)

# Yeguo: prefer the episodes array belonging to the requested drama so related
# recommendations cannot make a fully playable drama fail certification.
p = Path('native/core/provider_yeguo.go')
s = p.read_text()
start = s.index('func yeguoEpisodeIDs(value any) []string {')
end = s.index('func yeguoPlaybackMap(value any) map[string]any {', start)
helper = '''func yeguoEpisodeIDs(value any, sourceID string) []string {
\tsourceID = strings.TrimSpace(sourceID)
\tvar fallback []any
\tvar target []any
\tvar walk func(any)
\twalk = func(current any) {
\t\tswitch typed := current.(type) {
\t\tcase map[string]any:
\t\t\tif episodes, ok := typed["episodes"].([]any); ok && len(episodes) > 0 {
\t\t\t\tif fallback == nil {
\t\t\t\t\tfallback = episodes
\t\t\t\t}
\t\t\t\tid := yeguoText(typed, "video_id", "videoId", "playlet_id", "playletId", "drama_id", "dramaId", "id")
\t\t\t\tif sourceID != "" && id == sourceID {
\t\t\t\t\ttarget = episodes
\t\t\t\t\treturn
\t\t\t\t}
\t\t\t}
\t\t\tfor _, child := range typed {
\t\t\t\tif target == nil {
\t\t\t\t\twalk(child)
\t\t\t\t}
\t\t\t}
\t\tcase []any:
\t\t\tfor _, child := range typed {
\t\t\t\tif target == nil {
\t\t\t\t\twalk(child)
\t\t\t\t}
\t\t\t}
\t\t}
\t}
\twalk(value)
\trows := target
\tif rows == nil {
\t\trows = fallback
\t}
\tseen := map[string]bool{}
\tids := make([]string, 0, len(rows))
\tfor _, item := range rows {
\t\trow, ok := item.(map[string]any)
\t\tif !ok {
\t\t\tcontinue
\t\t}
\t\tif adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
\t\t\tcontinue
\t\t}
\t\tid := yeguoText(row, "id", "episode_id", "episodeId")
\t\tif id != "" && !seen[id] {
\t\t\tseen[id] = true
\t\t\tids = append(ids, id)
\t\t}
\t}
\treturn ids
}

'''
s = s[:start] + helper + s[end:]
old_call = 'episodeIDs := yeguoEpisodeIDs(detail)'
if old_call not in s:
    raise SystemExit('yeguo episode helper call not found')
s = s.replace(old_call, 'episodeIDs := yeguoEpisodeIDs(detail, sourceID)', 1)
p.write_text(s)
