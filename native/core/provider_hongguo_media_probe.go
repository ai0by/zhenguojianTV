package core

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 红果片源时长校验（0.8.14）。
//
// 背景（2026-10-27 实测）：红果把公开片源 `/video/public/{vid}/360p.mp4`
// 从"完整正片"换成了**固定 10.00 秒的预告片**（mp4 的 mvhd 是
// duration=10000 / timescale=1000），而 App 正片接口对游客基本只返回
// HTTP 200 + 空 body。旧链路把 360p 当作兜底，于是每一集都只播 10 秒
// 就结束 —— 用户看到的就是"解析出来都是 10 秒试看"。
//
// 这里给"公开片源"加一道硬校验：拿到地址先读头部判断真实时长，
// 明显短于正片的直接拒收，绝不再把预告片当正片交给播放器。

// hongguoPreviewMaxDuration 是"预告片"的时长上限。
//
// 红果短剧单集通常 60~120 秒（详情接口的 video_list[].duration 实测 79 秒），
// 预告固定 10 秒。取 20 秒做分界：既不误伤正常剧集，也能稳稳挡住预告。
const hongguoPreviewMaxDuration = 20 * time.Second

// hongguoMediaProbeBytes 是一次探测读取的字节数。
//
// 红果的 mp4 把 moov 放在文件最前面（实测 ftyp 32 字节 + moov 11441 字节），
// 64KB 足够覆盖 moov；读不出来就当作"无法判断"，由调用方决定放不放行。
const hongguoMediaProbeBytes = 64 << 10

var errHongguoMediaDurationUnknown = errors.New("无法从片源头部读出时长")

// hongguoMediaDurationFromPrefix 从片源头部推断时长。
// 支持 mp4（moov/mvhd）与 HLS 播放列表（累加 #EXTINF）。
func hongguoMediaDurationFromPrefix(prefix []byte) (time.Duration, error) {
	if len(prefix) < 8 {
		return 0, errHongguoMediaDurationUnknown
	}
	head := strings.TrimSpace(string(prefix[:min(len(prefix), 16)]))
	if strings.HasPrefix(head, "#EXTM3U") {
		return hongguoPlaylistDuration(string(prefix))
	}
	return hongguoMp4Duration(prefix)
}

// hongguoPlaylistDuration 累加 m3u8 里的 #EXTINF。
func hongguoPlaylistDuration(playlist string) (time.Duration, error) {
	var total time.Duration
	found := false
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		value := strings.TrimPrefix(line, "#EXTINF:")
		if index := strings.IndexByte(value, ','); index >= 0 {
			value = value[:index]
		}
		seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || seconds <= 0 {
			continue
		}
		total += time.Duration(seconds * float64(time.Second))
		found = true
	}
	if !found {
		return 0, errHongguoMediaDurationUnknown
	}
	return total, nil
}

// hongguoMp4Duration 走顶层 box，找到 moov 里的 mvhd。
func hongguoMp4Duration(data []byte) (time.Duration, error) {
	offset := 0
	for offset+8 <= len(data) {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		boxType := string(data[offset+4 : offset+8])
		header := uint64(8)
		if size == 1 {
			if offset+16 > len(data) {
				break
			}
			size = binary.BigEndian.Uint64(data[offset+8:])
			header = 16
		} else if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < header {
			break
		}
		end := offset + int(size)
		if end > len(data) {
			end = len(data)
		}
		switch boxType {
		case "moov":
			if duration, ok := hongguoMoovDuration(data[offset+int(header) : end]); ok {
				return duration, nil
			}
		case "mvhd":
			if duration, ok := hongguoMvhdDuration(data[offset+int(header) : end]); ok {
				return duration, nil
			}
		}
		if size == 0 {
			break
		}
		offset += int(size)
	}
	return 0, errHongguoMediaDurationUnknown
}

// hongguoMoovDuration 在 moov 的子 box 里找 mvhd。
func hongguoMoovDuration(moov []byte) (time.Duration, bool) {
	offset := 0
	for offset+8 <= len(moov) {
		size := uint64(binary.BigEndian.Uint32(moov[offset:]))
		boxType := string(moov[offset+4 : offset+8])
		header := uint64(8)
		if size == 1 {
			if offset+16 > len(moov) {
				return 0, false
			}
			size = binary.BigEndian.Uint64(moov[offset+8:])
			header = 16
		} else if size == 0 {
			size = uint64(len(moov) - offset)
		}
		if size < header {
			return 0, false
		}
		end := offset + int(size)
		if end > len(moov) {
			end = len(moov)
		}
		if boxType == "mvhd" {
			return hongguoMvhdDuration(moov[offset+int(header) : end])
		}
		if size == 0 {
			return 0, false
		}
		offset += int(size)
	}
	return 0, false
}

// hongguoMvhdDuration 解析 mvhd 的 timescale / duration（v0 与 v1 两种布局）。
func hongguoMvhdDuration(mvhd []byte) (time.Duration, bool) {
	if len(mvhd) < 20 {
		return 0, false
	}
	version := mvhd[0]
	var timescale uint32
	var duration uint64
	if version == 1 {
		if len(mvhd) < 32 {
			return 0, false
		}
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		duration = binary.BigEndian.Uint64(mvhd[24:32])
	} else {
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	}
	if timescale == 0 || duration == 0 {
		return 0, false
	}
	return time.Duration(duration * uint64(time.Second) / uint64(timescale)), true
}

// hongguoLooksLikePreview 判断一段时长是否只够当预告。
// 0 表示"没读到"，不做判断（交给调用方放行，避免误伤）。
func hongguoLooksLikePreview(duration time.Duration) bool {
	return duration > 0 && duration < hongguoPreviewMaxDuration
}
