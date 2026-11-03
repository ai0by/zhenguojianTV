import 'dart:async';

import 'package:flutter/foundation.dart';

import 'core_bridge.dart';
import 'danmaku_models.dart';
import 'models.dart';

class DanmakuController extends ChangeNotifier {
  DanmakuController(this.repository, {DateTime Function()? now})
    : _now = now ?? DateTime.now;

  final AppRepository repository;
  final DateTime Function() _now;
  final _pages = <int, DanmakuPage>{};
  final _failures = <int, DateTime>{};

  /// 每个时间窗连续失败了几次（0.8.19），决定退避时长。
  final _failureCounts = <int, int>{};

  /// 连续失败几次才把"弹幕加载失败"提示亮出来（0.8.22）。
  ///
  /// 取 2：第一次失败几乎全是瞬时的，静默重试通常就成功；让用户先看到
  /// "失败"再点一下又好了，等于白打扰一次。
  static const _retryHintThreshold = 2;

  /// 失败后自己排的那一次重试（0.8.21）。
  ///
  /// 以前重试完全靠播放器的位置 tick 驱动：缓冲卡住、暂停、切后台时位置
  /// 不再推进，tick 一停，一次偶发失败就再没人去重试 —— 用户看到的就是
  /// "这一集的弹幕一直没出来"，退出重进才有。
  Timer? _retryTimer;
  PlaybackPlan? _plan;
  int _generation = 0;
  int? _pendingStart;
  bool _closed = false;
  bool _enabled = true;
  bool _available = false;
  bool _foreground = true;
  bool _playing = false;
  bool _buffering = false;
  bool _seeking = false;
  int positionMs = 0;
  int durationMs = 0;
  double rate = 1;
  int motionRevision = 0;
  int dataRevision = 0;
  List<DanmakuItem> items = const [];
  DateTime? _anchorTime;
  int _anchorPosition = 0;
  double _anchorRate = 1;
  bool _anchorMoving = false;

  bool get supported =>
      _plan != null &&
      !_plan!.local &&
      _plan!.session.isNotEmpty &&
      _plan!.danmakuId.isNotEmpty;
  bool get visible =>
      !_closed &&
      supported &&
      _enabled &&
      _available &&
      _foreground &&
      !_seeking &&
      durationMs > 0 &&
      positionMs < durationMs;
  bool get moving => visible && _playing && !_buffering;
  bool get loading => _pendingStart != null;

  /// 当前这一窗连续失败到值得打扰用户（0.8.22）。
  ///
  /// 0.8.21 是"失败一次就亮提示"，真机上反馈很吵：这类失败绝大多数是瞬时
  /// 的（服务端某一发没给数据、签名没中），后台一两秒后就自己好了，用户点的
  /// 那一刻往往是"点了就好"——说明本来就不用点。现在要求同一窗**连续失败
  /// 两次**才亮，第一次只在内部静默重试。
  ///
  /// 上半段仍保留 0.8.21 修正过的口径：只看当前这一窗，不看历史失败。
  bool get canRetry {
    if (!visible) return false;
    final page = _covering(positionMs);
    final start =
        page?.startMs ?? positionMs ~/ danmakuWindowMs * danmakuWindowMs;
    return (_failureCounts[start] ?? 0) >= _retryHintThreshold;
  }
  String get status {
    if (!_enabled) return '弹幕已关闭';
    if (_plan?.local == true) return '本地播放不加载弹幕';
    if (_plan != null && !supported) return '本集暂不支持弹幕';
    if (canRetry) return '弹幕加载失败，可重试';
    // 第一次失败仍在内部重试，对外还是"正在加载"，不制造焦虑。
    if (loading || _failureCounts.isNotEmpty && _pages.isEmpty) {
      return '正在加载弹幕';
    }
    if (_pages.isNotEmpty) {
      return items.isEmpty ? '这段暂无弹幕' : '已载入 ${items.length} 条弹幕';
    }
    return '等待播放';
  }

  void setPlan(PlaybackPlan? plan) {
    if (_closed || identical(_plan, plan)) return;
    _cancel();
    _plan = plan;
    _pages.clear();
    _failures.clear();
    _failureCounts.clear();
    _rebuildItems();
    _seeking = false;
    _available = false;
    positionMs = durationMs = 0;
    _resetMotion();
    notifyListeners();
  }

  void setEnabled(bool enabled) {
    if (_closed || _enabled == enabled) return;
    _enabled = enabled;
    _cancel();
    _failures.clear();
    _failureCounts.clear();
    _pages.clear();
    _rebuildItems();
    _resetMotion();
    notifyListeners();
    _ensureWindow();
  }

  void update({
    required Duration position,
    required Duration duration,
    required double speed,
    required bool playing,
    required bool buffering,
    required bool foreground,
    required bool available,
    bool discontinuity = false,
  }) {
    if (_closed) return;
    final wasVisible = visible, wasMoving = moving;
    final previousRate = rate;
    final nextPosition = position.inMilliseconds.clamp(0, danmakuMaxDurationMs);
    final nextDuration = duration.inMilliseconds;
    final elapsed = _anchorTime == null
        ? 0
        : _now()
              .difference(_anchorTime!)
              .inMilliseconds
              .clamp(0, danmakuMaxDurationMs);
    final expected =
        _anchorPosition + (_anchorMoving ? elapsed * _anchorRate : 0);
    final drift = !_seeking && (nextPosition - expected).abs() > 600;
    // 时长抖动不算"换片源"（0.8.21）。
    //
    // 以前只要 duration 差 1 毫秒就把所有已缓存的窗口清空、重发请求。真实
    // 播放中时长偶尔会被播放器修正一点点（元数据时长 ↔ 实际探测时长），
    // 用户看到的就是"弹幕突然全没了，过一会儿才重新飘"。1 秒以内当作同一
    // 个时长，超过才重新开始。
    final durationChanged =
        nextDuration != durationMs &&
        durationMs > 0 &&
        (nextDuration - durationMs).abs() > 1000;
    final jumped =
        !_seeking &&
        (discontinuity ||
            nextPosition < positionMs ||
            nextPosition - positionMs > 2000);
    if (durationChanged) {
      _cancel();
      _pages.clear();
      _failures.clear();
      _failureCounts.clear();
      _rebuildItems();
    } else if (jumped) {
      _cancel();
    }
    if (!_seeking) positionMs = nextPosition;
    durationMs = nextDuration > 0 && nextDuration <= danmakuMaxDurationMs
        ? nextDuration
        : 0;
    rate = speed.isFinite && speed > 0 ? speed : 1;
    _playing = playing;
    _buffering = buffering;
    _foreground = foreground;
    _available = available;
    if (wasVisible && !visible) _cancel();
    if (drift ||
        jumped ||
        durationChanged ||
        wasVisible != visible ||
        wasMoving != moving ||
        previousRate != rate) {
      _resetMotion();
    }
    notifyListeners();
    _ensureWindow();
  }

  void beginSeek() {
    if (_closed) return;
    _cancel();
    _seeking = true;
    _resetMotion();
    notifyListeners();
  }

  void endSeek(Duration position) {
    if (_closed) return;
    _seeking = false;
    positionMs = position.inMilliseconds.clamp(0, danmakuMaxDurationMs);
    _resetMotion();
    notifyListeners();
    _ensureWindow();
  }

  void retry() {
    if (_closed || !visible) return;
    _failures.clear();
    _failureCounts.clear();
    _ensureWindow();
    notifyListeners();
  }

  void _resetMotion() {
    motionRevision++;
    _anchorTime = _now();
    _anchorPosition = positionMs;
    _anchorRate = rate;
    _anchorMoving = moving;
  }

  void _cancel() {
    _retryTimer?.cancel();
    _retryTimer = null;
    _generation++;
    if (_pendingStart != null) {
      _pendingStart = null;
      unawaited(repository.cancelDanmaku().catchError((Object _) {}));
    }
  }

  DanmakuPage? _covering(int position) {
    DanmakuPage? found;
    for (final page in _pages.values) {
      if (page.startMs <= position &&
          position < page.nextMs &&
          (found == null || page.startMs > found.startMs)) {
        found = page;
      }
    }
    return found;
  }

  void _ensureWindow() {
    if (!visible || _pendingStart != null) return;
    // 0.8.19：缓冲中也要允许**首次**取弹幕。
    //
    // 以前 `_buffering` 一律挡住，可起播那几秒恰好就在缓冲 —— 等缓冲结束
    // 才发请求，用户看到的就是"弹幕过两秒才飘出来"。已经有页时不抢带宽，
    // 仍按原来的节奏走。
    if (_buffering && _pages.isNotEmpty) return;
    final page = _covering(positionMs);
    var start = positionMs ~/ danmakuWindowMs * danmakuWindowMs;
    if (page == null && _pages.containsKey(start)) start = positionMs;
    if (page != null) {
      // 0.8.21：不再要求"正在播放"才预取下一窗。
      //
      // 以前暂停在窗口末尾（比如 28 秒）时不预取，用户一按继续，开头几秒
      // 正好是空的 —— 等新窗口回来弹幕已经飘过一段了。预取只是读数据，
      // 暂停时做掉最划算；位置不会再自己往前走，也不会连发。
      if (positionMs < page.nextMs - danmakuLifetimeMs ||
          page.nextMs >= durationMs ||
          _covering(page.nextMs) != null) {
        return;
      }
      start = page.nextMs;
    }
    final retryAt = _failures[start];
    if (retryAt != null && _now().isBefore(retryAt)) return;
    unawaited(_load(start));
  }

  Future<void> _load(int start) async {
    final ticket = _generation;
    final plan = _plan!;
    final duration = durationMs;
    _pendingStart = start;
    notifyListeners();
    try {
      final page = await repository.danmaku(
        plan,
        startMs: start,
        durationMs: duration,
      );
      if (_closed || ticket != _generation) return;
      if (page.episodeId != plan.danmakuId ||
          page.startMs != start ||
          page.nextMs <= start ||
          page.nextMs > duration ||
          page.items.length > 90 ||
          page.items.any(
            (item) => item.timeMs < start || item.timeMs >= page.nextMs,
          )) {
        throw const FormatException('弹幕时间范围无效');
      }
      _pages[start] = page;
      _failures.remove(start);
      _failureCounts.remove(start);
      _retryTimer?.cancel();
      _retryTimer = null;
      final nearest = _pages.keys.toList()
        ..sort(
          (a, b) => (a - positionMs).abs().compareTo((b - positionMs).abs()),
        );
      for (final key in nearest.skip(6)) {
        _pages.remove(key);
      }
      _rebuildItems();
    } catch (_) {
      if (_closed || ticket != _generation) return;
      // 0.8.22：退避改成 1.2 → 2.4 → 4.8 → 9.6 → 15 秒。
      //
      // 0.8.19 是 2 → 4 → 8 → 15。首次 2 秒偏长：这类失败基本是瞬时抖动，
      // 早 0.8 秒重试，用户几乎看不到空窗；后续仍逐步拉长，最多还是 15 秒。
      final failures = (_failureCounts[start] ?? 0) + 1;
      _failureCounts[start] = failures;
      final delay = Duration(
        milliseconds: (1200 * (1 << (failures - 1).clamp(0, 4))).clamp(
          1200,
          15000,
        ),
      );
      _failures[start] = _now().add(delay);
      // 0.8.21：自己排一次重试，不再只靠播放位置 tick 驱动。
      _scheduleRetry(delay);
      while (_failures.length > 8) {
        final key = _failures.keys.first;
        _failures.remove(key);
        _failureCounts.remove(key);
      }
    } finally {
      if (!_closed && ticket == _generation) {
        _pendingStart = null;
        notifyListeners();
        _ensureWindow();
      }
    }
  }

  /// 排一次"自己醒来再试"的定时器（0.8.21）。
  ///
  /// 失败重试以前完全依附于播放器位置 tick：暂停、缓冲卡死、切到后台时位置
  /// 不推进，tick 就停了，一次偶发失败之后再没人去重试 —— 用户看到的就是
  /// "这一集弹幕一直没加载上"，退出重进才有。这里独立排一个定时器，时间到
  /// 就重新走 [_ensureWindow]（它自己会判断窗口、退避与可见性）。
  void _scheduleRetry(Duration delay) {
    _retryTimer?.cancel();
    _retryTimer = Timer(delay, () {
      _retryTimer = null;
      if (_closed) return;
      notifyListeners();
      _ensureWindow();
    });
  }

  void _rebuildItems() {
    final unique = <String, DanmakuItem>{};
    final pages = _pages.values.toList()
      ..sort((a, b) => a.startMs.compareTo(b.startMs));
    for (final page in pages) {
      for (final item in page.items) {
        unique.putIfAbsent(item.id, () => item);
      }
    }
    final sorted = unique.values.toList()
      ..sort((a, b) => a.timeMs.compareTo(b.timeMs));
    items = List.unmodifiable(sorted);
    dataRevision++;
  }

  @override
  void dispose() {
    _closed = true;
    _cancel();
    super.dispose();
  }
}
