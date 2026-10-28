import 'dart:async';

import 'package:flutter/foundation.dart';

import 'core_bridge.dart';
import 'models.dart';

/// 播放预载 / 预缓存。
///
/// 两层分工（0.8.12 补齐第二层）：
///   1. [prepareAround] / [prepare]：只解析**相邻集**的播放地址
///      （下一集 + 上一集），顺手预热开头几 MB 到内存。
///      目标是"切过去第一帧快"。播放会话一换就没了。
///   2. [schedulePrecache]：把后面 N 集的**全部分片**落到磁盘
///      （`native/core/app_precache.go`），目标是"切过去整集都不用等"。
///      与播放会话解耦，切集、退出播放页都不丢；容量由 LRU 上限兜住。
///
/// 0.8.15：预载从"单槽（只准备下一集）"改成"多槽（上一集 + 下一集）"。
///
/// 用户反馈"切上一集比切下一集明显更卡"。根因是上一集从来没被预载过 ——
/// 切过去要现场走完整条解析链路（详情 → 取流 → 拉 m3u8 → 校验时长），
/// 网络往返全部暴露在切集那一刻；而切下一集时预载通常已经就绪。
/// 现在两个方向都提前准备好，切上一集也是零网络。
///
/// 预缓存仍是**串行**的：一次只处理一集，避免和正在播放的流抢带宽。
class PlaybackPreloader extends ChangeNotifier {
  PlaybackPreloader(this.repository, {DateTime Function()? now})
    : _now = now ?? DateTime.now;

  final AppRepository repository;
  final DateTime Function() _now;

  /// 槽上限。正常只会同时存在"上一集 + 下一集"两槽，
  /// 留一点余量给"连播下一部"那一路（它会准备另一部剧的第 1 集）。
  static const int _maxSlots = 4;

  /// 已解析好 / 正在解析的预载槽，键见 [_key]。
  final Map<String, _PreloadSlot> _slots = {};

  /// 供 [status] 展示用的"下一集"身份；没有下一集时为空。
  String _nextIdentity = '';

  bool _closed = false;

  // —— 预缓存（0.8.12）——
  final List<_PrecacheTarget> _queue = [];
  String _queueSignature = '';
  int _queueQuality = 0;
  int _precacheGeneration = 0;
  int _precacheBytes = 0;
  bool _precaching = false;

  void _release(PlaybackPlan plan) =>
      unawaited(repository.release(plan.session).catchError((Object _) {}));

  String get status {
    final slot = _nextIdentity.isEmpty ? null : _slots[_nextIdentity];
    if (slot != null && slot.plan != null && _now().isBefore(slot.expires)) {
      final plan = slot.plan!;
      final bytes = plan.prefetchedBytes;
      return plan.local
          ? '下一集已下载'
          : bytes > 0
          ? '下一集已准备 · 预取 ${(bytes / 1024).ceil()} KB'
          : '下一集播放地址已准备';
    }
    if (slot != null && (slot.loading || slot.delay != null)) {
      return '正在准备下一集';
    }
    return slot != null && slot.error.isNotEmpty
        ? '下一集预加载暂不可用，切集时会重新获取'
        : '播放后段提前准备下一集';
  }

  /// 本轮预缓存已经写入的字节数（供调试 / 状态展示）。
  int get precachedBytes => _precacheBytes;

  String _key(Drama drama, Episode episode, int quality, bool online) =>
      '${_prefix(drama)}${episode.number}\u0000$quality\u0000$online';

  /// 同一部剧的槽共有的前缀（`prepareAround` 靠它只清理本剧的邻居）。
  String _prefix(Drama drama) => '${drama.id}\u0000';

  /// 准备当前集**两侧**的播放地址（0.8.15）。
  ///
  /// [episodes] 是当前剧的完整分集列表，[index] 是正在播的集下标。
  /// 只准备紧邻的两集：更远的集等真的切过去再说，否则会同时占掉一堆
  /// 播放会话，反而把正在播的那一路挤掉（原生侧会话上限只有 12 个）。
  void prepareAround(
    Drama drama,
    List<Episode> episodes,
    int index, {
    int quality = 0,
    bool online = false,
  }) {
    if (_closed || episodes.isEmpty) return;
    final wanted = <String>{};
    final targets = <Episode>[];
    if (index + 1 >= 0 && index + 1 < episodes.length) {
      targets.add(episodes[index + 1]);
    }
    if (index - 1 >= 0 && index - 1 < episodes.length) {
      targets.add(episodes[index - 1]);
    }
    for (final episode in targets) {
      wanted.add(_key(drama, episode, quality, online));
    }
    _nextIdentity = index + 1 >= 0 && index + 1 < episodes.length
        ? _key(drama, episodes[index + 1], quality, online)
        : '';
    // 只留这两集：换集之后旧的邻居就没人要了，留着只是白占一个播放会话。
    // **只清本剧的槽**：`prepare` 还可能为"下一部剧的第 1 集"建了槽，
    // 那个不能被这里的清理顺手丢掉，否则每秒一次 `_syncPreload`
    // 会把下一部的预载反复建了又删。
    _evictExcept(_prefix(drama), wanted);
    for (final episode in targets) {
      _prepareOne(drama, episode, quality, online);
    }
  }

  /// 准备单集的播放地址（保留给"连播下一部"那一路用）。
  void prepare(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) {
    if (_closed) return;
    _nextIdentity = _key(drama, episode, quality, online);
    _prepareOne(drama, episode, quality, online);
  }

  void _prepareOne(Drama drama, Episode episode, int quality, bool online) {
    final identity = _key(drama, episode, quality, online);
    final slot = _slots.putIfAbsent(identity, _PreloadSlot.new);
    if (slot.plan != null && !_now().isBefore(slot.expires)) {
      final expired = slot.plan!;
      slot.plan = null;
      _release(expired);
    }
    if (slot.plan != null ||
        slot.loading ||
        slot.delay != null ||
        _now().isBefore(slot.retryAt)) {
      return;
    }
    if (_slots.length > _maxSlots) _evictOldest(keep: identity);
    final ticket = slot.generation;
    final settled = Completer<void>();
    slot.settled = settled;
    // 0.7.13：去掉 600ms 延迟（用户反馈"下一集还要等"），提前发起预载。
    slot.delay = Timer(Duration.zero, () async {
      slot.delay = null;
      try {
        if (_closed || ticket != slot.generation) return;
        slot.loading = true;
        notifyListeners();
        final plan = await repository.preload(
          drama,
          episode,
          quality: quality,
          online: online,
        );
        if (_closed || ticket != slot.generation) {
          if (plan != null) await repository.release(plan.session);
          return;
        }
        if (plan == null || plan.url.isEmpty) {
          throw AppFailure('未取得预加载结果');
        }
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
        slot.plan = plan;
        slot.expires = expires;
        slot.readyAt = _now();
        slot.error = '';
      } catch (failure) {
        if (_closed || ticket != slot.generation) return;
        slot.error = failure.toString();
        slot.retryAt = _now().add(const Duration(seconds: 30));
      } finally {
        if (!_closed && ticket == slot.generation) {
          slot.loading = false;
          notifyListeners();
        }
        if (identical(slot.settled, settled)) slot.settled = null;
        if (!settled.isCompleted) settled.complete();
      }
    });
    notifyListeners();
  }

  /// 只保留这一部剧的预载槽（"下一部"没了的时候把它的预载清掉）。
  ///
  /// 与 [clear] 的区别：这里**不动本剧的上一集/下一集**，
  /// 否则"最后一集 + 没有下一部"时会把刚备好的上一集一起丢掉。
  void retainDrama(Drama drama) {
    if (_closed) return;
    final prefix = _prefix(drama);
    final stale = _slots.keys.where((key) => !key.startsWith(prefix)).toList();
    for (final key in stale) {
      final slot = _slots.remove(key);
      if (slot != null) _discard(slot);
    }
    if (_nextIdentity.isNotEmpty && !_nextIdentity.startsWith(prefix)) {
      _nextIdentity = '';
    }
  }

  /// 丢掉本剧中不在 [keep] 里的槽（换集后旧邻居就没用了）。
  void _evictExcept(String prefix, Set<String> keep) {
    final stale = _slots.keys
        .where((key) => key.startsWith(prefix) && !keep.contains(key))
        .toList();
    for (final key in stale) {
      final slot = _slots.remove(key);
      if (slot != null) _discard(slot);
    }
  }

  /// 槽数量超过上限时，按"最久没有结果"淘汰一个（在途的槽不碰）。
  void _evictOldest({required String keep}) {
    String? victim;
    DateTime? oldest;
    for (final entry in _slots.entries) {
      if (entry.key == keep || entry.value.loading) continue;
      final stamp = entry.value.readyAt;
      if (oldest == null || stamp.isBefore(oldest)) {
        oldest = stamp;
        victim = entry.key;
      }
    }
    if (victim == null) return;
    final slot = _slots.remove(victim);
    if (slot != null) _discard(slot);
  }

  /// 让一个槽彻底作废：停掉定时器、放行等待者、必要时释放播放会话。
  ///
  /// [releasePlan] 传 false 用于 [take] —— 那一份计划要交给调用方去播。
  void _discard(_PreloadSlot slot, {bool releasePlan = true}) {
    slot.generation++;
    slot.delay?.cancel();
    slot.delay = null;
    slot.loading = false;
    final pending = slot.settled;
    slot.settled = null;
    if (pending != null && !pending.isCompleted) pending.complete();
    final plan = slot.plan;
    slot.plan = null;
    if (releasePlan && plan != null) _release(plan);
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
      ..addAll(episodes.map((episode) => _PrecacheTarget(drama, episode)));
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
        final identity = _key(
          target.drama,
          target.episode,
          _queueQuality,
          false,
        );
        // 第一集往往正是 `preload` 的目标：等它落地再排队，
        // 才能复用同一个播放会话，省掉一次解析请求。
        final pending = _slots[identity]?.settled;
        if (pending != null) await pending.future;
        if (_closed || generation != _precacheGeneration) return;
        // 这一集如果正好已经解析好，直接复用它的会话与播放地址。
        final ready = _slots[identity];
        final reusable =
            ready != null && ready.plan != null && _now().isBefore(ready.expires);
        try {
          _precacheBytes += await repository.precache(
            target.drama,
            target.episode,
            quality: _queueQuality,
            session: reusable ? ready.plan!.session : '',
            entry: reusable ? ready.plan!.url : '',
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

  /// 让预缓存给播放让路（0.8.13）。
  ///
  /// 预缓存和正在播的那一路共用同一条出口，缓冲见底时继续全速下载只会
  /// 让卡顿更久 —— 用户看到的就是"看一会儿卡一下"。
  /// 这里直接取消在途的预缓存并清空队列；等缓冲恢复健康，
  /// `_syncPreload` 会带着同样的目标重新排队（签名已被清掉，
  /// 所以不会被 `schedulePrecache` 的去重逻辑挡住）。
  ///
  /// 已经落盘的分片不会丢，重新排队时会被跳过，代价只有一次重新解析。
  void pausePrecache() {
    // 队列空且没有在途下载时无事可做；`_syncPreload` 每秒会调好几次，
    // 不加这个判断就会反复打原生接口。
    if (_closed || (_queue.isEmpty && !_precaching)) return;
    _resetPrecache();
  }

  /// 这一集是否已经解析好了（**不取走**）。
  ///
  /// 用来判断"命中预载就别显示加载遮罩"。取走（[take]）必须等集数索引
  /// 算完才能做，而遮罩得在那之前决定要不要显示，所以单独开一个只读判断。
  bool hasPlan(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) {
    final slot = _slots[_key(drama, episode, quality, online)];
    if (slot == null || slot.plan == null) return false;
    if (!_now().isBefore(slot.expires)) {
      final expired = slot.plan!;
      slot.plan = null;
      _release(expired);
      return false;
    }
    return true;
  }

  /// 取走某一集已解析好的计划；没有就返回 null。
  ///
  /// 只清掉被取走的那一个槽 —— 另一个方向的邻居还要留给下一次切集用。
  PlaybackPlan? take(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) {
    final identity = _key(drama, episode, quality, online);
    final slot = _slots.remove(identity);
    // 换集后预缓存的目标会整体前移，先让原生侧停下在途的整集下载。
    _resetPrecache();
    if (slot == null) return null;
    // **先取出来再作废槽**：`_discard` 会把 `slot.plan` 置空。
    final plan = slot.plan;
    _discard(slot, releasePlan: false);
    if (plan == null || !_now().isBefore(slot.expires)) {
      if (plan != null) _release(plan);
      return null;
    }
    return plan;
  }

  /// 暂停所有在途预载（缓冲不足、离开页面时调用）。
  void pause() {
    var changed = false;
    var loading = false;
    for (final slot in _slots.values) {
      if (slot.loading || slot.delay != null) changed = true;
      if (slot.loading) loading = true;
      slot.generation++;
      slot.delay?.cancel();
      slot.delay = null;
      final pending = slot.settled;
      slot.settled = null;
      if (pending != null && !pending.isCompleted) pending.complete();
      slot.loading = false;
    }
    if (loading) {
      unawaited(repository.cancelPreload().catchError((Object _) {}));
    }
    if (changed && !_closed) notifyListeners();
  }

  void clear() {
    pause();
    for (final slot in _slots.values.toList()) {
      _discard(slot);
    }
    _slots.clear();
    _nextIdentity = '';
    _resetPrecache();
    if (!_closed) notifyListeners();
  }

  @override
  void dispose() {
    _closed = true;
    clear();
    super.dispose();
  }
}

/// 一个预载槽：对应"某一集的播放地址"。
class _PreloadSlot {
  _PreloadSlot();

  PlaybackPlan? plan;
  DateTime expires = DateTime(2000);
  DateTime retryAt = DateTime(2000);
  Timer? delay;
  Completer<void>? settled;
  int generation = 0;
  bool loading = false;
  String error = '';

  /// 最近一次拿到结果的时间，供槽满时挑淘汰对象。
  DateTime readyAt = DateTime(2000);
}

class _PrecacheTarget {
  const _PrecacheTarget(this.drama, this.episode);
  final Drama drama;
  final Episode episode;
}
