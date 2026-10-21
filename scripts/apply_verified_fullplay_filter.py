from pathlib import Path


def replace_func(text: str, signature: str, replacement: str) -> str:
    start = text.index(signature)
    brace = text.index('{', start)
    depth = 0
    in_string = None
    escaped = False
    i = brace
    while i < len(text):
        ch = text[i]
        if in_string:
            if in_string == '`':
                if ch == '`':
                    in_string = None
            else:
                if escaped:
                    escaped = False
                elif ch == '\\':
                    escaped = True
                elif ch == in_string:
                    in_string = None
        else:
            if ch in ('"', "'", '`'):
                in_string = ch
            elif ch == '{':
                depth += 1
            elif ch == '}':
                depth -= 1
                if depth == 0:
                    return text[:start] + replacement.rstrip() + text[i + 1 :]
        i += 1
    raise RuntimeError(f'unclosed function: {signature}')


def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old not in text:
        raise RuntimeError(f'missing block: {label}')
    return text.replace(old, new, 1)


# -----------------------------------------------------------------------------
# Xiangjiao: a drama is visible only after every episode gets a server-authorized
# full playback session. Metadata flags are not sufficient for certification.
# -----------------------------------------------------------------------------
p = Path('native/core/provider_xiangjiao.go')
s = p.read_text()

s = replace_func(
    s,
    'func (d *Downloader) xiangjiaoDramaFullyPlayable(ctx context.Context, sourceID string) bool {',
    r'''func (d *Downloader) xiangjiaoDramaFullyPlayable(ctx context.Context, sourceID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false
	}
	if cached, ok := xiangjiaoPlayableCache.Load(sourceID); ok {
		entry := cached.(xiangjiaoPlayableCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.playable
		}
		xiangjiaoPlayableCache.Delete(sourceID)
	}

	raw, err := d.xiangjiaoJSON(ctx, http.MethodGet, "/api/dramas/"+url.PathEscape(sourceID)+"/episodes", nil, "")
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	var episodeIDs []string
	walkXiangjiaoMaps(xiangjiaoPayloadData(raw), func(row map[string]any) {
		id := xiangjiaoText(row, "id", "episode_id", "episodeId")
		if id == "" || seen[id] {
			return
		}
		index := xiangjiaoInt(row, "episode_number", "episodeNumber")
		if index <= 0 && xiangjiaoText(row, "drama_id", "dramaId") == "" {
			return
		}
		seen[id] = true
		episodeIDs = append(episodeIDs, id)
	})
	if len(episodeIDs) == 0 {
		return false
	}

	playable := true
	for _, episodeID := range episodeIDs {
		if _, err := d.xiangjiaoFullPlaybackManifest(ctx, sourceID, episodeID); err != nil {
			playable = false
			break
		}
	}
	ttl := 30 * time.Minute
	if !playable {
		ttl = 2 * time.Minute
	}
	xiangjiaoPlayableCache.Store(sourceID, xiangjiaoPlayableCacheEntry{
		playable: playable,
		expires:  time.Now().Add(ttl),
	})
	return playable
}''',
)

s = replace_once(
    s,
    '''\tfor i := range items {
\t\tif items[i].VIP != nil && *items[i].VIP {
\t\t\tcontinue
\t\t}
\t\twg.Add(1)''',
    '''\tfor i := range items {
\t\twg.Add(1)''',
    'xiangjiao catalog VIP skip',
)
s = s.replace('context.WithTimeout(ctx, 5*time.Second)', 'context.WithTimeout(ctx, 12*time.Second)')

# Ensure direct/history opens also obey the certification rule.
detail_sig = 'func (d *Downloader) fetchXiangjiaoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {'
detail_start = s.index(detail_sig)
detail_end = s.index('func newXiangjiaoDeviceID()', detail_start)
detail = s[detail_start:detail_end]
detail = replace_once(
    detail,
    '''\tif sourceID == "" {
\t\treturn Drama{}, nil, fmt.Errorf("香蕉剧集 ID 为空")
\t}
''',
    '''\tif sourceID == "" {
\t\treturn Drama{}, nil, fmt.Errorf("香蕉剧集 ID 为空")
\t}
\tprobeCtx, probeCancel := context.WithTimeout(ctx, 12*time.Second)
\tfullyPlayable := d.xiangjiaoDramaFullyPlayable(probeCtx, sourceID)
\tprobeCancel()
\tif !fullyPlayable {
\t\treturn Drama{}, nil, fmt.Errorf("香蕉该剧未通过全集正片校验，已隐藏")
\t}
''',
    'xiangjiao detail certification',
)
detail = detail.replace(
    '''\tfor _, episode := range episodes {
\t\tif episode.locked {
\t\t\treturn Drama{}, nil, fmt.Errorf("香蕉该剧存在受限分集，已按完整可播放策略隐藏")
\t\t}
\t}
''',
    '',
)
detail = detail.replace('locked: xiangjiaoEpisodeLocked(row),', 'locked: false,')
s = s[:detail_start] + detail + s[detail_end:]

s = replace_func(
    s,
    'func (d *Downloader) resolveXiangjiaoPlayback(ctx context.Context, task Task) (providerMedia, error) {',
    r'''func (d *Downloader) xiangjiaoFullPlaybackManifest(ctx context.Context, dramaID, episodeID string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := d.xiangjiaoGuestToken(ctx, attempt > 0)
		if err != nil {
			return "", err
		}
		payload := map[string]any{"episode_id": episodeID}
		if strings.TrimSpace(dramaID) != "" {
			payload["drama_id"] = strings.TrimSpace(dramaID)
		}
		decoded, err := d.xiangjiaoJSON(ctx, http.MethodPost, "/api/playback/sessions", payload, token)
		if err != nil {
			if apiErr, ok := err.(*xiangjiaoHTTPError); ok {
				if apiErr.Status == http.StatusUnauthorized && attempt == 0 {
					d.clearXiangjiaoGuestToken()
					continue
				}
				if apiErr.Status == http.StatusForbidden || strings.EqualFold(apiErr.Code, "PAYMENT_REQUIRED") {
					return "", fmt.Errorf("香蕉该集需要会员或购买，服务端拒绝游客正片播放")
				}
			}
			return "", err
		}
		data, _ := xiangjiaoPayloadData(decoded).(map[string]any)
		mode := strings.ToLower(xiangjiaoText(data, "play_mode", "playMode"))
		if mode != "full" {
			return "", fmt.Errorf("香蕉服务端未返回 full 正片会话")
		}
		media := xiangjiaoMap(data, "media")
		manifest := xiangjiaoText(media, "manifest_url", "manifestUrl", "url")
		if manifest == "" {
			return "", fmt.Errorf("香蕉正片会话未返回 manifest_url")
		}
		if strings.HasPrefix(manifest, "/") {
			manifest = strings.TrimRight(d.providerBaseURL(sourceXiangjiao), "/") + manifest
		}
		return manifest, nil
	}
	return "", fmt.Errorf("香蕉游客会话刷新失败")
}

func (d *Downloader) resolveXiangjiaoPlayback(ctx context.Context, task Task) (providerMedia, error) {
	episodeID := xiangjiaoMarkerEpisode(task.Chapter.VideoURL)
	if episodeID == "" {
		return providerMedia{}, fmt.Errorf("香蕉分集播放标识无效，请刷新详情")
	}
	_, dramaID, validDrama := splitProviderDramaID(task.DramaID)
	if !validDrama {
		dramaID = ""
	}
	manifest, err := d.xiangjiaoFullPlaybackManifest(ctx, dramaID, episodeID)
	if err != nil {
		return providerMedia{}, err
	}
	return providerMedia{URL: manifest, Referer: firstNonEmpty(task.Chapter.Referer, d.providerBaseURL(sourceXiangjiao)+"/")}, nil
}''',
)

p.write_text(s)


# -----------------------------------------------------------------------------
# Yeguo: current live Nuxt build wraps AES key/IV literals (key:t(`...`)).
# Discover those dynamically, then certify every non-ad episode via /playlet/play.
# -----------------------------------------------------------------------------
p = Path('native/core/provider_yeguo.go')
s = p.read_text()
s = replace_once(s, '\t"strings"\n\t"time"', '\t"strings"\n\t"sync"\n\t"time"', 'yeguo sync import')
s = s.replace('yeguoMaxChunkScan = 32', 'yeguoMaxChunkScan = 96')
s = replace_once(
    s,
    r'''\tyeguoJSRefPattern      = regexp.MustCompile(`["']((?:\./|/)[^"']+\.js(?:\?[^"']*)?)["']`)
\tyeguoKeyPattern        = regexp.MustCompile(`(?i)["']?key["']?\s*:\s*["']([^"']{16})["']`)
\tyeguoIVPattern         = regexp.MustCompile(`(?i)["']?iv["']?\s*:\s*["']([^"']{16})["']`)''',
    r'''\tyeguoJSRefPattern       = regexp.MustCompile(`["']([^"']+\.js(?:\?[^"']*)?)["']`)
\tyeguoKeyWrappedPattern = regexp.MustCompile("(?i)\\bkey\\s*:\\s*[A-Za-z_$][A-Za-z0-9_$]*\\(\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
\tyeguoKeyPattern        = regexp.MustCompile("(?i)\\bkey\\s*:\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
\tyeguoIVWrappedPattern  = regexp.MustCompile("(?i)\\biv\\s*:\\s*[A-Za-z_$][A-Za-z0-9_$]*\\(\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")
\tyeguoIVPattern         = regexp.MustCompile("(?i)\\biv\\s*:\\s*[`\\\"']([^`\\\"']{16})[`\\\"']")''',
    'yeguo JS/crypto regex',
)

# Every catalog path is filtered, including the fast Nuxt homepage path.
s = s.replace('return items, true, nil', 'return d.filterYeguoFullyPlayable(ctx, items), true, nil', 1)
s = s.replace('return items, len(items) >= yeguoPageSize, nil', 'return d.filterYeguoFullyPlayable(ctx, items), len(items) >= yeguoPageSize, nil', 1)
s = replace_once(
    s,
    'return yeguoFilterDramas(yeguoDramas(root), query), false, nil',
    'items := yeguoFilterDramas(yeguoDramas(root), query)\n\t\t\treturn d.filterYeguoFullyPlayable(ctx, items), false, nil',
    'yeguo search fallback filter',
)

# Direct/history detail must be certified too. Certified episodes are presented as normal playable episodes.
y_detail_sig = 'func (d *Downloader) fetchYeguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {'
y_start = s.index(y_detail_sig)
y_end = s.index('func randomHex32()', y_start)
y_detail = s[y_start:y_end]
y_detail = replace_once(
    y_detail,
    '''\tif sourceID == "" {
\t\treturn Drama{}, nil, fmt.Errorf("野果剧集 ID 为空")
\t}
''',
    '''\tif sourceID == "" {
\t\treturn Drama{}, nil, fmt.Errorf("野果剧集 ID 为空")
\t}
\tprobeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
\tfullyPlayable := d.yeguoDramaFullyPlayable(probeCtx, sourceID)
\tprobeCancel()
\tif !fullyPlayable {
\t\treturn Drama{}, nil, fmt.Errorf("野果该剧未通过全集正片校验，已隐藏")
\t}
''',
    'yeguo detail certification',
)
y_detail = y_detail.replace('locked:         yeguoLocked(row),', 'locked:         false,')
y_detail = y_detail.replace('metaLocked := meta != nil && yeguoLocked(meta)', 'metaLocked := false')
s = s[:y_start] + y_detail + s[y_end:]

# Prevent concurrent catalog probes from each rescanning the whole Nuxt graph.
s = replace_func(
    s,
    'func (d *Downloader) yeguoCryptoConfig(ctx context.Context, force bool) (yeguoCryptoConfig, error) {',
    r'''func (d *Downloader) yeguoCryptoConfig(ctx context.Context, force bool) (yeguoCryptoConfig, error) {
	d.yeguoCryptoMu.Lock()
	defer d.yeguoCryptoMu.Unlock()
	if !force && d.yeguoCrypto.Key != "" && time.Now().Before(d.yeguoCryptoExpires) {
		return d.yeguoCrypto, nil
	}
	config, err := d.discoverYeguoCrypto(ctx)
	if err != nil {
		return yeguoCryptoConfig{}, err
	}
	d.yeguoCrypto = config
	d.yeguoCryptoExpires = time.Now().Add(yeguoCryptoTTL)
	return config, nil
}''',
)

s = replace_func(
    s,
    'func (d *Downloader) discoverYeguoCrypto(ctx context.Context) (yeguoCryptoConfig, error) {',
    r'''func (d *Downloader) discoverYeguoCrypto(ctx context.Context) (yeguoCryptoConfig, error) {
	var lastErr error
	for _, base := range d.providerBaseCandidates(ctx, sourceYeguo, yeguoBaseMirrors) {
		home := strings.TrimRight(base, "/") + "/"
		body, err := d.fetchProviderText(ctx, home, home)
		if err != nil {
			lastErr = err
			continue
		}
		queue := make([]string, 0, 96)
		for _, match := range yeguoScriptSrcPattern.FindAllStringSubmatch(body, -1) {
			if len(match) > 1 {
				if script := resolveProviderReference(home, match[1]); script != "" {
					queue = append(queue, script)
				}
			}
		}
		seen := map[string]bool{}
		for len(queue) > 0 && len(seen) < yeguoMaxChunkScan {
			batch := make([]string, 0, 8)
			for len(queue) > 0 && len(batch) < 8 && len(seen) < yeguoMaxChunkScan {
				current := queue[0]
				queue = queue[1:]
				if seen[current] {
					continue
				}
				parsed, parseErr := url.Parse(current)
				homeURL, homeErr := url.Parse(home)
				if parseErr != nil || homeErr != nil || !strings.EqualFold(parsed.Hostname(), homeURL.Hostname()) {
					continue
				}
				seen[current] = true
				batch = append(batch, current)
			}
			if len(batch) == 0 {
				continue
			}
			type result struct {
				url  string
				body string
				err  error
			}
			results := make(chan result, len(batch))
			for _, address := range batch {
				go func(address string) {
					chunk, fetchErr := d.fetchProviderText(ctx, address, home)
					results <- result{url: address, body: chunk, err: fetchErr}
				}(address)
			}
			for range batch {
				item := <-results
				if item.err != nil {
					lastErr = item.err
					continue
				}
				if strings.Contains(item.body, "sign_key") || strings.Contains(strings.ToLower(item.body), "pkcs7") || strings.Contains(strings.ToUpper(item.body), "CBC") {
					key := ""
					if match := yeguoKeyWrappedPattern.FindStringSubmatch(item.body); len(match) > 1 {
						key = match[1]
					} else if match := yeguoKeyPattern.FindStringSubmatch(item.body); len(match) > 1 {
						key = match[1]
					}
					iv := ""
					if match := yeguoIVWrappedPattern.FindStringSubmatch(item.body); len(match) > 1 {
						iv = match[1]
					} else if match := yeguoIVPattern.FindStringSubmatch(item.body); len(match) > 1 {
						iv = match[1]
					}
					if len(key) == 16 && len(iv) == 16 {
						return yeguoCryptoConfig{Key: key, IV: iv}, nil
					}
				}
				for _, match := range yeguoJSRefPattern.FindAllStringSubmatch(item.body, -1) {
					if len(match) < 2 {
						continue
					}
					if next := resolveProviderReference(item.url, match[1]); next != "" && !seen[next] {
						queue = append(queue, next)
					}
				}
			}
		}
	}
	if lastErr != nil {
		return yeguoCryptoConfig{}, fmt.Errorf("野果前端加密配置发现失败: %w", lastErr)
	}
	return yeguoCryptoConfig{}, fmt.Errorf("野果前端加密配置已变化，请更新站源适配")
}''',
)

insert_at = s.index('func yeguoFindMediaURL(value any) string {')
helpers = r'''type yeguoPlayableCacheEntry struct {
	playable bool
	expires  time.Time
}

var yeguoPlayableCache sync.Map

func yeguoEpisodeIDs(value any) []string {
	seen := map[string]bool{}
	var ids []string
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if episodes, ok := typed["episodes"].([]any); ok {
				for _, item := range episodes {
					row, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
						continue
					}
					id := yeguoText(row, "id", "episode_id", "episodeId")
					if id != "" && !seen[id] {
						seen[id] = true
						ids = append(ids, id)
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return ids
}

func yeguoPlaybackMap(value any) map[string]any {
	if row, ok := value.(map[string]any); ok {
		if yeguoText(row, "video_url", "video_url_h265", "play_url", "manifest_url", "url") != "" {
			return row
		}
		for _, child := range row {
			if found := yeguoPlaybackMap(child); found != nil {
				return found
			}
		}
	}
	if list, ok := value.([]any); ok {
		for _, child := range list {
			if found := yeguoPlaybackMap(child); found != nil {
				return found
			}
		}
	return nil
}

func (d *Downloader) yeguoFullPlaybackURL(ctx context.Context, dramaID, episodeID string) (string, error) {
	attempts := []map[string]string{
		{"playlet_id": dramaID, "video_id": dramaID, "episode_id": episodeID},
		{"video_id": dramaID, "episode_id": episodeID},
		{"episode_id": episodeID},
	}
	var lastErr error
	for _, params := range attempts {
		decoded, err := d.yeguoAPICall(ctx, "/api/playlet/play", params)
		if err != nil {
			lastErr = err
			continue
		}
		row := yeguoPlaybackMap(decoded)
		if row == nil {
			lastErr = fmt.Errorf("野果正片接口未返回播放对象")
			continue
		}
		if returnedDrama := yeguoText(row, "playlet_id", "video_id"); returnedDrama != "" && returnedDrama != dramaID {
			lastErr = fmt.Errorf("野果返回剧集与请求不一致")
			continue
		}
		if returnedEpisode := yeguoText(row, "id", "episode_id", "episodeId"); returnedEpisode != "" && returnedEpisode != episodeID {
			lastErr = fmt.Errorf("野果返回分集与请求不一致")
			continue
		}
		if adv, exists := yeguoBool(row, "is_adv", "isAdv"); exists && adv {
			lastErr = fmt.Errorf("野果返回的是广告而非正片")
			continue
		}
		mediaURL := yeguoText(row, "video_url", "video_url_h265", "play_url", "manifest_url", "url")
		if !strings.HasPrefix(mediaURL, "http://") && !strings.HasPrefix(mediaURL, "https://") {
			lastErr = fmt.Errorf("野果正片接口未返回有效播放地址")
			continue
		}
		return mediaURL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("野果正片播放地址获取失败")
	}
	return "", lastErr
}

func (d *Downloader) yeguoDramaFullyPlayable(ctx context.Context, sourceID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false
	}
	if cached, ok := yeguoPlayableCache.Load(sourceID); ok {
		entry := cached.(yeguoPlayableCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.playable
		}
		yeguoPlayableCache.Delete(sourceID)
	}
	detail, err := d.yeguoAPICall(ctx, "/api/playlet/detail", map[string]string{
		"video_id": sourceID,
		"id": sourceID,
		"episode_id": "0",
		"related_limit": "0",
	})
	if err != nil {
		return false
	}
	episodeIDs := yeguoEpisodeIDs(detail)
	if len(episodeIDs) == 0 {
		return false
	}
	playable := true
	for _, episodeID := range episodeIDs {
		if _, err := d.yeguoFullPlaybackURL(ctx, sourceID, episodeID); err != nil {
			playable = false
			break
		}
	}
	ttl := 30 * time.Minute
	if !playable {
		ttl = 2 * time.Minute
	}
	yeguoPlayableCache.Store(sourceID, yeguoPlayableCacheEntry{playable: playable, expires: time.Now().Add(ttl)})
	return playable
}

func (d *Downloader) filterYeguoFullyPlayable(ctx context.Context, items []Drama) []Drama {
	if len(items) == 0 {
		return items
	}
	allowed := make([]bool, len(items))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			allowed[index] = d.yeguoDramaFullyPlayable(probeCtx, items[index].SourceID)
		}(i)
	}
	wg.Wait()
	filtered := make([]Drama, 0, len(items))
	for i, drama := range items {
		if allowed[i] {
			vip := false
			drama.VIP = &vip
			filtered = append(filtered, drama)
		}
	}
	return filtered
}

'''
s = s[:insert_at] + helpers + s[insert_at:]

s = replace_func(
    s,
    'func (d *Downloader) resolveYeguoPlayback(ctx context.Context, task Task) (providerMedia, error) {',
    r'''func (d *Downloader) resolveYeguoPlayback(ctx context.Context, task Task) (providerMedia, error) {
	episodeID := yeguoMarkerEpisode(task.Chapter.VideoURL)
	if episodeID == "" {
		return providerMedia{}, fmt.Errorf("野果分集播放标识无效，请刷新详情")
	}
	_, dramaID, valid := splitProviderDramaID(task.DramaID)
	if !valid || dramaID == "" {
		return providerMedia{}, fmt.Errorf("野果剧集信息无效")
	}
	mediaURL, err := d.yeguoFullPlaybackURL(ctx, dramaID, episodeID)
	if err != nil {
		return providerMedia{}, err
	}
	return providerMedia{URL: mediaURL, Referer: firstNonEmpty(task.Chapter.PageURL, d.providerBaseURL(sourceYeguo)+"/")}, nil
}''',
)

p.write_text(s)
