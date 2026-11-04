package core

import (
	"math/rand"
	"time"
)

// 站源请求的重试节奏。原实现是 attempt 秒的线性退避（1s、2s、3s…），
// 既不能快速放弃明显不通的域名，又会在网络抖动时让一批任务同时重试。
// 这里改为指数退避并加入抖动：0.8s、1.6s、3.2s…，上限 8s，抖动 ±25%。
const (
	providerRetryBaseDelay = 800 * time.Millisecond
	providerRetryMaxDelay  = 8 * time.Second
)

// providerRetryDelay 返回第 attempt 次重试（attempt 从 1 开始）前的等待时长。
func providerRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	delay := providerRetryBaseDelay
	for index := 1; index < attempt; index++ {
		delay *= 2
		if delay >= providerRetryMaxDelay {
			delay = providerRetryMaxDelay
			break
		}
	}
	jitter := time.Duration(rand.Int63n(int64(delay)/2+1)) - delay/4
	if next := delay + jitter; next > 0 {
		return next
	}
	return delay
}

// providerQuickRetryDelay 用于"服务端瞬时丢弃请求"这类快速失败场景：
// 红果 App 接口的空响应通常在 200ms 内返回，此时用秒级退避会让 8 次重试
// 累计等待超过 30 秒。这里改为 150~400ms 的短抖动。
func providerQuickRetryDelay() time.Duration {
	return 150*time.Millisecond + time.Duration(rand.Int63n(int64(250*time.Millisecond)))
}

// 红果 App 接口"空正文 / 4xx"之后的退避序列（0.8.23）。
//
// 探针实测（provider_hongguo_live_probe_test.go，HONGGUO_LIVE_PROBE=1）：
// 空正文的失败不是每发独立掷骰子，而是**服务端按时间片周期性丢弃**——
// 同一时间片内的请求共享命运（并发 2 份对冲成功率 50% 反而低于单发 68.8%），
// 连续失败跨度约 0.5~1.5 秒（worst-streak 3~4 发）。因此：
//   - 150~400ms 的快速连发全部落在同一个失败窗里，救不回来还可能加重风控；
//   - 用户"点重试（1~3 秒后）就好"正是天然跨出了失败窗。
//
// 这里按递增序列跨窗：0.8 → 1.4 → 2.0 → 2.5 秒封顶，各带 ±20% 抖动错开
// 窗口边界。注册设备的 5 次重试累计退避约 6.7 秒，仍在普通 25s / 弹幕 15s
// 的单次预算内。用变量而不是常量，是为了单测能把等待缩到毫秒级（空正文
// 的重试路径在夹具里会真跑满 5 次，真等 6.7 秒无法接受）。
var hongguoAppRetryDelays = []time.Duration{
	800 * time.Millisecond,
	1400 * time.Millisecond,
	2000 * time.Millisecond,
	2500 * time.Millisecond,
}

// hongguoAppRetryDelay 返回第 step 次"空正文"重试前的等待时长。
// step 从 1 开始；超出序列后固定用最后一档。
func hongguoAppRetryDelay(step int) time.Duration {
	if step < 1 {
		step = 1
	}
	delay := providerRetryMaxDelay
	if count := len(hongguoAppRetryDelays); count > 0 {
		delay = hongguoAppRetryDelays[count-1]
		if step <= count {
			delay = hongguoAppRetryDelays[step-1]
		}
	}
	jitter := time.Duration(rand.Int63n(int64(delay)*2/5+1)) - delay/5
	if next := delay + jitter; next > 0 {
		return next
	}
	return delay
}
