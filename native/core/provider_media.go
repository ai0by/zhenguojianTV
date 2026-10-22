package core

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type providerMedia struct {
	credentials *providerMediaCredentials
	URL         string
	Referer     string
	Duration    time.Duration
	Playlist    string
	HLSKey      []byte
	CENCKey     []byte
	Quality     int
	Variants    []providerMedia
}

func (d *Downloader) providerBaseURL(source string) string {
	source = canonicalProviderSource(source)
	var configured, fallback string
	switch source {
	case sourceHuangguoAI:
		configured, fallback = d.cfg.HuangguoAIURL, huangguoAIBaseURL
	case sourceHuangguoVideo:
		configured, fallback = d.cfg.HuangguoVideoURL, huangguoVideoBaseURL
	case sourceHuangdou:
		configured, fallback = d.cfg.HuangdouURL, huangdouBaseURL
	case sourceHongguo:
		configured, fallback = d.cfg.HongguoURL, hongguoBaseURL
	case sourceHuangju:
		configured, fallback = d.cfg.HuangjuURL, huangjuBaseURL
	case sourceDuanju51:
		fallback = duanju51BaseURL
	case sourceYeguo:
		fallback = yeguoBaseURL
	case sourceXiangjiao:
		fallback = xiangjiaoBaseURL
	case sourceEZDMW:
		fallback = ezdmwBaseURL
	default:
		fallback = "https://d2pypzndaqisk.cloudfront.net"
	}
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	// 远程镜像优先于编译进 APK 的内置地址；这里只读缓存，绝不发起网络请求，
	// 因为 providerBaseURL 会在构造每个地址时被同步调用。
	if origin := cachedProviderMirrorOrigin(source); origin != "" {
		return strings.TrimRight(origin, "/")
	}
	return strings.TrimRight(fallback, "/")
}

func providerSourceForURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "huangguoai.com" || strings.HasSuffix(host, ".ediayikma.cc") || strings.HasSuffix(host, ".agdkczeyx.cc"):
		return sourceHuangguoAI
	case host == "huangguo.video" || host == "huangguoai.fit":
		return sourceHuangguoVideo
	case huangdouMirrorHost(host):
		return sourceHuangdou
	case host == "hongguoduanju.com" || host == "www.hongguoduanju.com":
		return sourceHongguo
	case host == "huangju.net" || host == "www.huangju.net" || host == "api.huangju.net":
		return sourceHuangju
	case duanju51MirrorHost(host):
		return sourceDuanju51
	case yeguoMirrorHost(host) || host == "api.ekzhadks.cc":
		return sourceYeguo
	case xiangjiaoMirrorHost(host):
		return sourceXiangjiao
	case ezdmwMirrorHost(host):
		return sourceEZDMW
	default:
		// 远程镜像配置里登记的备用域名（红果、黄剧等站源经常换域名）。
		return providerSourceForHost(host)
	}
}

func (d *Downloader) providerURLCandidates(raw string) []string {
	source := providerSourceForURL(raw)
	if source == "" {
		return []string{raw}
	}
	parsed, _ := url.Parse(raw)
	d.providerMu.Lock()
	preferred := d.providerHosts[source]
	d.providerMu.Unlock()
	var candidates []string
	seen := map[string]bool{}
	add := func(candidate string) {
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	add(rehostProviderURL(parsed, preferred))
	configured := d.providerBaseURL(source)
	add(rehostProviderURL(parsed, configured))
	var mirrors []string
	switch source {
	case sourceHuangguoAI:
		mirrors = huangguoAIBaseMirrors
	case sourceHuangdou:
		mirrors = huangdouBaseMirrors
	case sourceDuanju51:
		mirrors = duanju51BaseMirrors
	case sourceYeguo:
		mirrors = yeguoBaseMirrors
	case sourceXiangjiao:
		mirrors = xiangjiaoBaseMirrors
	case sourceEZDMW:
		mirrors = ezdmwBaseMirrors
	}
	for _, origin := range mirrors {
		add(rehostProviderURL(parsed, origin))
	}
	return candidates
}

// providerPageURL 把章节里保存的页面地址对齐到当前站源基址。
// 旧任务存的 PageURL 可能是上一代域名，而站源已经换到新镜像；主机不对齐的话
// 请求会打到已下线/被拦的地址。路径与查询保持不变。
func (d *Downloader) providerPageURL(raw, source string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	base, err := url.Parse(d.providerBaseURL(source))
	if err != nil || base.Host == "" || strings.EqualFold(base.Host, parsed.Host) {
		return raw
	}
	if rehosted := rehostProviderURL(parsed, base.Scheme+"://"+base.Host); rehosted != "" {
		return rehosted
	}
	return raw
}

func (d *Downloader) resolveProviderMedia(ctx context.Context, task Task) (providerMedia, error) {
	chapter := task.Chapter
	chapter.Source = canonicalProviderSource(chapter.Source)
	if chapter.Source == "" {
		chapter.Source = sourceFromDramaID(task.DramaID)
	}
	if chapter.Source == sourceCloudFront {
		return d.resolveLegacyMedia(ctx, task)
	}
	if chapter.Source == sourceHuangju {
		return d.resolveHuangjuMedia(ctx, task)
	}
	if strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return d.resolveHongguoMedia(ctx, task)
	}
	if chapter.PageURL == "" && (chapter.Source == sourceHuangguoAI || chapter.Source == sourceHuangguoVideo) {
		if source, sourceID, valid := splitProviderDramaID(task.DramaID); valid {
			_, chapters, err := d.GetHuangguoChapters(ctx, source, sourceID)
			if err != nil {
				return providerMedia{}, fmt.Errorf("刷新旧任务播放地址失败: %w", err)
			}
			matched := false
			for _, fresh := range chapters {
				if fresh.ID == chapter.ID {
					chapter = fresh
					matched = true
					break
				}
			}
			if !matched {
				return providerMedia{}, fmt.Errorf("原章节已变化，请更新该合集后重新下载")
			}
		}
	}
	media := providerMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, d.providerBaseURL(chapter.Source)+"/")}
	if chapter.Source == sourceYeguo && strings.HasPrefix(chapter.VideoURL, "yeguo-play://") {
		var err error
		media, err = d.resolveYeguoPlayback(ctx, task)
		if err != nil {
			return providerMedia{}, err
		}
	}
	if chapter.Source == sourceXiangjiao && strings.HasPrefix(chapter.VideoURL, "xiangjiao-play://") {
		var err error
		media, err = d.resolveXiangjiaoPlayback(ctx, task)
		if err != nil {
			return providerMedia{}, err
		}
	}
	if chapter.Source == sourceEZDMW && strings.HasPrefix(chapter.VideoURL, "ezdmw-play://") {
		var err error
		media, err = d.resolveEZDMWPlayback(ctx, task)
		if err != nil {
			return providerMedia{}, err
		}
	}
	if chapter.Source == sourceHuangdou {
		_, sourceID, valid := splitProviderDramaID(task.DramaID)
		if valid {
			sequence, err := strconv.Atoi(chapter.EpisodeString(task.Index))
			if err != nil || sequence < 1 {
				return providerMedia{}, fmt.Errorf("黄豆集数无效")
			}
			client := newHuangdouAPIClient(d)
			media, err = d.resolveHuangdouPlayback(ctx, client, sourceID, sequence)
			if err != nil {
				return providerMedia{}, err
			}
			media.Referer = client.host + "/home"
		}
	}
	if chapter.PageURL != "" && (chapter.Source == sourceHuangguoAI || chapter.Source == sourceHuangguoVideo) {
		responses := &playbackResponseURLs{}
		pageContext := context.WithValue(ctx, playbackResponseURLsKey{}, responses)
		// 存量任务里保存的 PageURL 可能指向已经失效或被 WAF 拦下的旧域名
		// （黄果旧站 huangguo.video 现在直接 404，黄果 AI 又频繁换镜像）。
		// 先按当前站源基址重新拼接主机，失败再退回原地址，否则换域名之后
		// 这些老任务会一直播放失败。
		pageURL := d.providerPageURL(chapter.PageURL, chapter.Source)
		body, err := d.fetchProviderText(pageContext, pageURL, media.Referer)
		if err != nil && pageURL != chapter.PageURL {
			pageURL = chapter.PageURL
			body, err = d.fetchProviderText(pageContext, pageURL, media.Referer)
		}
		if err != nil {
			return providerMedia{}, err
		}
		if actual, ok := responses.values.Load(pageURL); ok {
			pageURL = actual.(string)
		}
		if chapter.Source == sourceHuangguoAI {
			media.URL = parseAIVideoURL(body, pageURL)
		} else {
			media.URL = parseDataHLS(body, pageURL)
		}
		media.Referer = pageURL
	}
	if !isProviderHTTPMediaURL(media.URL) {
		return providerMedia{}, fmt.Errorf("%s 未返回有效播放地址，请刷新章节或确认站点访问权限", chapter.Source)
	}
	parsed, _ := url.Parse(media.URL)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") || (chapter.Source == sourceEZDMW && ezdmwPlaylistURL(media.URL)) {
		playlist, finalURL, err := d.fetchMediaPlaylist(ctx, media.URL, media.Referer)
		if err != nil {
			return providerMedia{}, fmt.Errorf("获取播放列表失败: %w", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
			return providerMedia{}, fmt.Errorf("站点未返回有效 M3U8，可能需要登录或链接已失效")
		}
		if duration := m3u8Duration(playlist); duration > 0 {
			media.Duration = duration
		}
		media.Playlist = playlist
		media.URL = finalURL
	}
	return media, nil
}
