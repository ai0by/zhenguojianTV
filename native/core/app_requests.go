package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

type nativeReadRequest struct {
	sequence int64
	cancel   context.CancelFunc
	updated  time.Time
}

func validNativeRead(input nativeInput) bool {
	return input.Sequence > 0 && len(input.Session) > 0 && len(input.Session) <= 128 &&
		strings.IndexFunc(input.Session, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("-_:.", r))
		}) < 0
}

func (engine *nativeEngine) beginRead(ctx context.Context, input nativeInput) (context.Context, func(), error) {
	if !validNativeRead(input) {
		return nil, nil, errors.New("请求标记无效")
	}
	engine.readMu.Lock()
	defer engine.readMu.Unlock()
	engine.prepareReadsLocked()
	previous := engine.reads[input.Session]
	if input.Sequence <= previous.sequence {
		return nil, nil, context.Canceled
	}
	if previous.cancel != nil {
		previous.cancel()
	}
	if engine.readCount >= 16 || len(engine.reads) >= 128 && previous.sequence == 0 {
		return nil, nil, errors.New("正在处理的请求较多，请稍后重试")
	}
	work, cancel := context.WithCancel(ctx)
	engine.reads[input.Session] = nativeReadRequest{sequence: input.Sequence, cancel: cancel, updated: time.Now()}
	engine.readCount++
	finish := func() {
		cancel()
		engine.readMu.Lock()
		defer engine.readMu.Unlock()
		engine.readCount--
		if request := engine.reads[input.Session]; request.sequence == input.Sequence {
			request.cancel, request.updated = nil, time.Now()
			engine.reads[input.Session] = request
		}
	}
	return work, finish, nil
}

func (engine *nativeEngine) prepareReadsLocked() {
	if engine.reads == nil {
		engine.reads = map[string]nativeReadRequest{}
	}
	// session 里带集号：Dart 侧预载用的是 "preload-<集号>"，每切一集就新增
	// 一条；而 read 完成后 finish 只清 cancel、并不删条目，于是连播越久
	// engine.reads 越大（按集号线性累积）。这些后台预载/弹幕/目录请求还和
	// 前台取流共用同一个 limiter，旧策略 15 分钟才清一次远远不够。这里把
	// 过期窗口收到 2 分钟，并在条目数超过 96 时按 updated 把最旧的一批
	// 直接删到 64 以内（无论是否过期），避免长连播后 map 无限膨胀。
	for key, request := range engine.reads {
		if request.cancel == nil && time.Since(request.updated) > 2*time.Minute {
			delete(engine.reads, key)
		}
	}
	if len(engine.reads) > 96 {
		// 只裁剪已完成的条目：进行中的 read 带 cancel / sequence，删掉会
		// 影响 input.Sequence <= previous.sequence 的取消判断；readCount
		// 上限 16 保证进行中不超过 16 条，删已完成的足够降到 64。
		type readEntry struct {
			key     string
			updated time.Time
		}
		finished := make([]readEntry, 0, len(engine.reads))
		for key, request := range engine.reads {
			if request.cancel == nil {
				finished = append(finished, readEntry{key: key, updated: request.updated})
			}
		}
		sort.Slice(finished, func(i, j int) bool {
			return finished[i].updated.Before(finished[j].updated)
		})
		for _, entry := range finished {
			if len(engine.reads) <= 64 {
				break
			}
			delete(engine.reads, entry.key)
		}
	}
}

func (engine *nativeEngine) cancelRead(input nativeInput) {
	if !validNativeRead(input) {
		return
	}
	engine.readMu.Lock()
	defer engine.readMu.Unlock()
	engine.prepareReadsLocked()
	previous := engine.reads[input.Session]
	if input.Sequence < previous.sequence {
		return
	}
	if previous.cancel != nil {
		previous.cancel()
	}
	if len(engine.reads) < 128 || previous.sequence > 0 {
		engine.reads[input.Session] = nativeReadRequest{sequence: input.Sequence, updated: time.Now()}
	}
}
