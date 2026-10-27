import 'dart:async';

import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_cache.dart';
import 'package:duanju_app/playback_preloader.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

const _drama = Drama(id: 'hongguo:1', source: 'hongguo', title: '甲剧', episodes: 4);

Episode _episode(int number) =>
    Episode({'id': '$number', 'title': '第$number集', 'currentEpisode': number}, number);

/// 只关心"预载 / 预缓存"这两个调用，其余能力沿用 `FixtureRepository`。
class _Repo extends FixtureRepository {
  final preloads = <String>[];
  final precaches = <String>[];
  final precacheSessions = <String>[];
  final releases = <String>[];
  int cancelPrecacheCalls = 0;

  /// 非空时 `precache` 会挂在这里，用来模拟"正在下载"。
  Completer<void>? gate;

  @override
  Future<PlaybackPlan?> preload(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) async {
    preloads.add('${drama.id}#${episode.number}');
    return PlaybackPlan(
      url: 'http://127.0.0.1:1/preload/${episode.number}',
      session: 'S${episode.number}',
    );
  }

  @override
  Future<int> precache(
    Drama drama,
    Episode episode, {
    int quality = 0,
    String session = '',
    String entry = '',
    int budget = 0,
  }) async {
    precaches.add('${drama.id}#${episode.number}');
    precacheSessions.add(session);
    final pending = gate;
    if (pending != null) await pending.future;
    return 1024;
  }

  @override
  Future<void> cancelPrecache() async {
    cancelPrecacheCalls++;
  }

  @override
  Future<void> release(String session) async => releases.add(session);
}

void main() {
  test('0.8.12：预缓存按队列串行执行，并复用已预载的会话', () async {
    final repo = _Repo();
    final preloader = PlaybackPreloader(repo);
    addTearDown(preloader.dispose);

    preloader.prepare(_drama, _episode(2));
    await pumpEventQueue();
    expect(repo.preloads, ['hongguo:1#2'], reason: '先解析下一集');

    preloader.schedulePrecache(_drama, [_episode(2), _episode(3)]);
    await pumpEventQueue();

    expect(repo.precaches, ['hongguo:1#2', 'hongguo:1#3'], reason: '按顺序缓存两集');
    expect(
      repo.precacheSessions.first,
      'S2',
      reason: '第一集应复用 preload 拿到的会话，省一次解析',
    );
    expect(repo.precacheSessions.last, '', reason: '第二集没有现成会话，交给原生侧解析');
    expect(preloader.precachedBytes, 2048);
  });

  test('0.8.12：同一批目标重复排队不会重复下载', () async {
    final repo = _Repo();
    final preloader = PlaybackPreloader(repo);
    addTearDown(preloader.dispose);

    preloader.schedulePrecache(_drama, [_episode(2), _episode(3)]);
    await pumpEventQueue();
    expect(repo.precaches.length, 2);

    preloader.schedulePrecache(_drama, [_episode(2), _episode(3)]);
    await pumpEventQueue();
    expect(repo.precaches.length, 2, reason: '签名相同应直接返回');

    preloader.schedulePrecache(_drama, [_episode(3), _episode(4)]);
    await pumpEventQueue();
    expect(repo.precaches, [
      'hongguo:1#2',
      'hongguo:1#3',
      'hongguo:1#3',
      'hongguo:1#4',
    ], reason: '目标变化后应重新排队');
  });

  test('0.8.12：清空队列会取消原生侧的预缓存', () async {
    final repo = _Repo();
    final preloader = PlaybackPreloader(repo);
    addTearDown(preloader.dispose);

    preloader.schedulePrecache(_drama, [_episode(2)]);
    await pumpEventQueue();
    final cancelsBefore = repo.cancelPrecacheCalls;

    preloader.schedulePrecache(_drama, const []);
    await pumpEventQueue();
    expect(
      repo.cancelPrecacheCalls,
      greaterThan(cancelsBefore),
      reason: '关闭预缓存 / 换集时应通知原生停止',
    );

    preloader.clear();
    await pumpEventQueue();
    expect(repo.cancelPrecacheCalls, greaterThan(cancelsBefore));
  });

  test('0.8.13：缓冲见底时预缓存让路，恢复后能重新排队', () async {
    final repo = _Repo();
    final preloader = PlaybackPreloader(repo);
    addTearDown(preloader.dispose);

    // 让第一集的预缓存"卡在下载中"，才能验证"在途也能被打断"。
    repo.gate = Completer<void>();
    preloader.schedulePrecache(_drama, [_episode(2)]);
    await pumpEventQueue();
    expect(repo.precaches, ['hongguo:1#2']);

    final cancelsBefore = repo.cancelPrecacheCalls;
    preloader.pausePrecache();
    await pumpEventQueue();
    expect(
      repo.cancelPrecacheCalls,
      greaterThan(cancelsBefore),
      reason: '缓冲不足时应立刻停掉在途的整集下载，把带宽让给播放',
    );

    repo.gate!.complete();
    repo.gate = null;
    await pumpEventQueue();

    // 关键：`pausePrecache` 必须把排队签名一起清掉，
    // 否则缓冲恢复后 `schedulePrecache` 会因"签名相同"直接返回，
    // 预缓存就再也不启动了（用户看到的就是"看两集就不再缓存"）。
    preloader.schedulePrecache(_drama, [_episode(2)]);
    await pumpEventQueue();
    expect(
      repo.precaches,
      ['hongguo:1#2', 'hongguo:1#2'],
      reason: '恢复健康后应重新排同一批目标',
    );

    // 空闲时重复调用不应反复打原生接口（`_syncPreload` 每秒会调好几次）。
    final idleCancels = repo.cancelPrecacheCalls;
    preloader.pausePrecache();
    await pumpEventQueue();
    expect(repo.cancelPrecacheCalls, idleCancels);
  });

  test('0.8.12：预缓存偏好默认 2 集 / 1GB，且能往返序列化', () {
    const defaults = PlaybackCachePreferences();
    expect(defaults.episodes, 2);
    expect(defaults.enabled, isTrue);
    expect(defaults.limitMB, 1024);
    expect(defaults.limitLabel, '1 GB');

    final restored = PlaybackCachePreferences.fromJson(defaults.toJson());
    expect(restored.episodes, 2);
    expect(restored.limitMB, 1024);

    expect(const PlaybackCachePreferences(episodes: 0).enabled, isFalse);
    expect(const PlaybackCachePreferences(episodes: 0).episodeLabel, '关闭');
    expect(const PlaybackCachePreferences(limitMB: 512).limitLabel, '512 MB');
    expect(
      () => PlaybackCachePreferences.fromJson(const {'episodes': 9}),
      throwsFormatException,
    );
  });

  test('0.8.12：缓存用量按人类可读单位展示', () {
    const usage = PlaybackCacheUsage(bytes: 1536, files: 3, limitBytes: 1 << 30);
    expect(usage.bytesLabel, '2 KB');
    expect(usage.limitLabel, '1.00 GB');
    final parsed = PlaybackCacheUsage.fromJson(const {
      'bytes': 5 << 20,
      'files': 12,
      'limit': 2 << 30,
    });
    expect(parsed.bytesLabel, '5.0 MB');
    expect(parsed.files, 12);
  });
}
