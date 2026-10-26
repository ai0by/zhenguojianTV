import 'dart:async';

import 'package:flutter/foundation.dart';

import 'core_bridge.dart';
import 'models.dart';

/// 播放预载 / 预缓存。
///
/// 两层分工（0.8.12 补齐第二层）：
///   1. [prepare]：只解析**下一集**的播放地址，顺手预热开头几 MB 到内存。
///      目标是"切过去第一帧快"。播放会话一换就没了。
///   2. [schedulePrecache]：把后面 N 集的**全部分片**落到磁盘
///      （`native/core/app_precache.go`），目标是"切过去整集都不用等"。
///      与播放会话解耦，切集、退出播放页都不丢；容量由 LRU 上限兜住。
///
/// 预缓存是**串行**的：一次只处理一集，避免和正在播放的流抢带宽。
class PlaybackPreloader extends ChangeNotifier {
  PlaybackPreloader(this.repository, {DateTime Function()? now})
    : _now = now ?? DateTime.now;

  final AppRepository repository;
  final DateTime Function() _now;
  PlaybackPlan? _ready;
  String? _identity;
  Timer? _delay;
  DateTime _expires = DateTime(2000), _retryAt = DateTime(2000);
  int _generation = 0;
  bool _closed = false, loading = false;
  String error = '';

  /// 正在进行的 `preload` 何时落地。
  ///
  /// 预缓存要复用 `preload` 解析好的会话，所以第一集得等它完成 ——
  /// 用 Completer 而不是定时轮询，测试里 `pumpEventQueue` 也能直接跑完。
  Completer<void>? _preloadSettled;

  /// 预缓存队列（0.8.12）。串行消费，一次一集。
  final List<_PrecacheTarget> _queue = [];
  String _queueSignature = '';
  int _queueQuality = 0;
  int _precacheGeneration = 0;
  int _precacheBytes = 0;
  bool _precaching = false;

  void _release(PlaybackPlan plan) =>
      unawaited(repository.release(plan.session).catchError((Object _) {}));

  String get status {
    if (_ready != null && _now().isBefore(_expires)) {
      final bytes = _ready!.prefetchedBytes;
      return _ready!.local
          ? '下一集已下载'
          : bytes > 0
          ? '下一集已准备 · 预取 ${(bytes / 1024).ceil()} KB'
          : '下一集播放地址已准备';
    }
    if (loading || _delay != null) return '正在准备下一集';
    return error.isNotEmpty ? '下一集预加载暂不可用，切集时会重新获取' : '播放后段提前准备下一集';
  }

  /// 本轮预缓存已经写入的字节数（供调试 / 状态展示）。
  int get precachedBytes => _precacheBytes;

  String _key(Drama drama, Episode episode, int quality, bool online) =>
      '${drama.id}\u0000${episode.number}\u0000$quality\u0000$online';

  void prepare(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) {
    if (_closed) return;
    final identity = _key(drama, episode, quality, online);
    if (_identity != identity) {
      clear();
      _identity = identity;
    }
    if (_ready != null && !_now().isBefore(_expires)) {
      final expired = _ready!;
      _ready = null;
      _release(expired);
    }
    if (_ready != null ||
        loading ||
        _delay != null ||
        _now().isBefore(_retryAt)) {
      return;
    }
    final ticket = _generation;
    // 提前建好"预载落地"的凭据：预缓存可能紧接着 `prepare` 就被排上，
    // 那一刻 `_delay` 还没触发，靠它才能等到会话可复用。
    _settlePreload();
    final settled = Completer<void>();
    _preloadSettled = settled;
    // 0.7.13：去掉 600ms 延迟（用户反馈"下一集还要等"），提前发起预载。
    _delay = Timer(Duration.zero, () async {
      _delay = null;
      loading = true;
      notifyListeners();
      try {
        final plan = await repository.preload(
          drama,
          episode,
          quality: quality,
          online: online,
        );
        if (_closed || ticket != _generation) {
          if (plan != null) await repository.release(plan.session);
          return;
        }
        if (plan == null || plan.url.isEmpty) throw AppFailure('未取得预加载结果');
        // 0.7.13：有效期 2 分钟 → 5 分钟，避免"停一会儿再切"时预载已过期。
        var expires = _now().add(const Duration(minutes: 5));
        if (plan.expiresAt > 0) {
          final authorizationExpiry = DateTime.fromMillisecondsSinceEpoch(
            plan.expiresAt,
          ).subtract(const Duration(seconds: 5));
          if (authorizationExpiry.isBefore(expires)) {
            expires = authorizationExpiry;
          }
        }
        if (!expires.isAfter(_now())) {
          _release(plan);
          throw AppFailure('下一集播放凭证即将过期');
        }
        _ready = plan;
        _expires = expires;
        error = '';
      } catch (failure) {
        if (_closed || ticket != _generation) return;
        error = failure.toString();
        _retryAt = _now().add(const Duration(seconds: 30));
      } finally {
        if (!_closed && ticket == _generation) {
          loading = false;
          notifyListeners();
        }
        if (identical(_preloadSettled, settled)) _preloadSettled = null;
        if (!settled.isCompleted) settled.complete();
      }
    });
    notifyListeners();
  }

  /// 安排预缓存后续若干集（0.8.12）。
  ///
  /// [episodes] 传"当前集之后"的剧集，数量由设置里的「预缓存集数」决定。
  /// 同一批目标重复调用会被签名拦掉，不会重复排队。
  void schedulePrecache(
    Drama drama,
    List<Episode> episodes, {
    int quality = 0,
  }) {
    if (_closed) return;
    if (episodes.isEmpty) {
      _resetPrecache();
      return;
    }
    final signature =
        '${drama.id}|${episodes.map((episode) => episode.number).join(',')}'
        '|$quality';
    if (signature == _queueSignature) return;
    _queueSignature = signature;
    _queueQuality = quality;
    _precacheGeneration++;
    _queue
      ..clear()
      ..addAll(
        episodes.map((episode) => _PrecacheTarget(drama, episode)),
      );
    unawaited(_drainPrecache());
  }

  /// 串行消费队列：一集处理完再下一集，中途被重置就退出。
  Future<void> _drainPrecache() async {
    if (_precaching || _closed) return;
    _precaching = true;
    try {
      while (!_closed && _queue.isNotEmpty) {
        final generation = _precacheGeneration;
        final target = _queue.removeAt(0);
        // 第一集往往正是 `preload` 的目标：等它落地再排队，
        // 才能复用同一个播放会话，省掉一次解析请求。
        final pending = _preloadSettled;
        if (pending != null) await pending.future;
        if (_closed || generation != _precacheGeneration) return;
        // 这一集如果正好是 `preload` 已经解析好的"下一集"，
        // 直接复用它的会话与播放地址，省掉一次解析请求。
        final ready = _ready;
        final reusable =
            ready != null &&
            _identity == _key(target.drama, target.episode, _queueQuality, false) &&
            _now().isBefore(_expires);
        try {
          _precacheBytes += await repository.precache(
            target.drama,
            target.episode,
            quality: _queueQuality,
            session: reusable ? ready.session : '',
            entry: reusable ? ready.url : '',
          );
        } catch (_) {
          // 预缓存失败不影响播放：切过去时会重新解析、现拉分片。
        }
        if (_closed || generation != _precacheGeneration) return;
      }
    } finally {
      _precaching = false;
      // 队列在处理途中被重置时，这里补一次，避免新目标永远不跑。
      if (!_closed && _queue.isNotEmpty) unawaited(_drainPrecache());
    }
  }

  void _resetPrecache() {
    _queue.clear();
    _queueSignature = '';
    _precacheGeneration++;
    unawaited(repository.cancelPrecache().catchError((Object _) {}));
  }

  PlaybackPlan? take(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) {
    final result =
        _identity == _key(drama, episode, quality, online) &&
            _now().isBefore(_expires)
        ? _ready
        : null;
    if (result != null) _ready = null;
    clear();
    return result;
  }

  /// 放行等待"预载落地"的预缓存。
  ///
  /// `pause()` / `clear()` 会把在途预载取消掉，此时 Completer 不会再由
  /// 预载回调完成；不主动放行的话，预缓存队列会永远卡在 await 上。
  void _settlePreload() {
    final pending = _preloadSettled;
    _preloadSettled = null;
    if (pending != null && !pending.isCompleted) pending.complete();
  }

  void pause() {
    final changed = loading || _delay != null;
    _generation++;
    _delay?.cancel();
    _delay = null;
    _settlePreload();
    if (loading) {
      unawaited(repository.cancelPreload().catchError((Object _) {}));
    }
    loading = false;
    if (changed && !_closed) notifyListeners();
  }

  void clear() {
    pause();
    final old = _ready;
    _ready = null;
    _identity = null;
    _retryAt = DateTime(2000);
    error = '';
    _resetPrecache();
    if (old != null) _release(old);
    if (!_closed) notifyListeners();
  }

  @override
  void dispose() {
    _closed = true;
    clear();
    super.dispose();
  }
}

class _PrecacheTarget {
  const _PrecacheTarget(this.drama, this.episode);
  final Drama drama;
  final Episode episode;
}
