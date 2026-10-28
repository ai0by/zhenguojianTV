import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

const _a = Drama(id: 'hongguo:1', source: 'hongguo', title: '甲剧', episodes: 3);
const _b = Drama(id: 'hongguo:2', source: 'hongguo', title: '乙剧', episodes: 3);
const _c = Drama(id: 'hongguo:3', source: 'hongguo', title: '丙剧', episodes: 3);

class _Repo extends FixtureRepository {
  final preloads = <String>[];
  final resolves = <String>[];
  final releases = <String>[];

  @override
  Future<CatalogPage> recommendations(
    String genre, {
    bool more = false,
    bool force = false,
  }) async => CatalogPage(const [_a, _b, _c]);

  @override
  Future<DramaDetail> detail(Drama drama) async => DramaDetail(drama, [
    Episode({'id': '1', 'title': '第1集', 'currentEpisode': 1}, 1),
    Episode({'id': '2', 'title': '第2集', 'currentEpisode': 2}, 2),
    Episode({'id': '3', 'title': '第3集', 'currentEpisode': 3}, 3),
  ]);

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    final key = '${drama.title}${episode.number}';
    resolves.add(key);
    return PlaybackPlan(
      url: 'https://example.test/r-$key.mp4',
      session: 'R-$key-${resolves.length}',
    );
  }

  @override
  Future<PlaybackPlan?> preload(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) async {
    final key = '${drama.title}${episode.number}';
    preloads.add(key);
    return PlaybackPlan(
      url: 'https://example.test/p-$key.mp4',
      session: 'P-$key-${preloads.length}',
    );
  }

  @override
  Future<void> release(String session) async {
    releases.add(session);
  }
}

Future<void> _pump(WidgetTester tester, int frames) async {
  for (var i = 0; i < frames; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

Future<List<ScriptedPlayer>> _mount(
  WidgetTester tester,
  _Repo repo,
  LocalStore store, {
  Player Function()? factory,
}) async {
  final platforms = <ScriptedPlayer>[];
  await tester.pumpWidget(
    MaterialApp(
      theme: ThemeData.dark(),
      home: Scaffold(
        body: VerticalFeedScreen(
          key: const ValueKey('feed'),
          repository: repo,
          store: store,
          sources: const [SourceSite.hongguo],
          embedded: true,
          active: true,
          playerFactory:
              factory ??
              () {
                final p = ScriptedPlayer();
                platforms.add(p);
                return Player(platformPlayer: p);
              },
          videoBuilder: (controls) => controls,
        ),
      ),
    ),
  );
  return platforms;
}

int _currentPage(WidgetTester tester) {
  final pageView = tester.widget<PageView>(find.byType(PageView).first);
  final controller = pageView.controller;
  if (controller == null || !controller.hasClients) return 0;
  return (controller.page ?? controller.initialPage.toDouble()).round();
}

/// 取一个**落在视频画面内**的起手点（0.8.13）。
///
/// 小屏改成"视频 + 信息面板"两段式布局后，下半屏是可滚动的面板，
/// 从那里起手只会滚动面板、不会切集/翻页；视频画面本身只有约 236 高，
/// 正中的「播放」按钮与顶部 60px 禁滑条也要避开。
Offset _videoPoint(WidgetTester tester) {
  final stage = find.byKey(const ValueKey('feed-stage'));
  final rect = stage.evaluate().isEmpty
      ? tester.getRect(find.byType(VerticalFeedScreen))
      : tester.getRect(stage);
  return rect.centerLeft + const Offset(24, 0);
}

void main() {
  late LocalStore store;

  setUp(() async {
    SharedPreferences.setMockInitialValues(const {});
    store = LocalStore(await SharedPreferences.getInstance());
  });

  tearDown(() => store.dispose());

  testWidgets('0.6.9：滑到下一部时**复用**预加载，不再重新解析', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);

    final repo = _Repo();
    final platforms = await _mount(tester, repo, store);
    await _pump(tester, 80);

    final firstPage = _currentPage(tester);

    for (final p in platforms) {
      p.state = p.state.copyWith(position: const Duration(seconds: 90));
      p.positionController.add(const Duration(seconds: 90));
    }
    await _pump(tester, 80);

    expect(repo.preloads, isNotEmpty, reason: '推进位置后应触发"下一部"预加载');
    final preloaded = repo.preloads.last;
    debugPrint('预加载目标 = $preloaded（当前页 = $firstPage）');

    repo.resolves.clear();
    repo.releases.clear();

    // 0.8.13：小屏改了两段式布局，起点必须落在视频画面内，
    // 否则手势只会滚动下方信息面板、不会翻页。
    await tester.flingFrom(_videoPoint(tester), const Offset(0, -400), 1200);
    await _pump(tester, 120);

    final secondPage = _currentPage(tester);
    debugPrint(
      '滑到页 = $secondPage；期间 resolve=${repo.resolves} '
      'release=${repo.releases}',
    );

    expect(secondPage, isNot(firstPage), reason: '应已切换到另一部剧');
    expect(repo.resolves, isEmpty, reason: '滑到下一部时不应重新解析播放地址（预加载应被复用）');
    expect(
      repo.releases.where((s) => s.startsWith('P-')),
      isEmpty,
      reason: '不应释放刚预载好的会话',
    );
  });

  testWidgets('0.6.9：切下一集**复用**预加载（既有行为不得回退）', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);

    final repo = _Repo();
    final platforms = <ScriptedPlayer>[];
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: Scaffold(
          body: VerticalFeedScreen(
            key: const ValueKey('feed'),
            repository: repo,
            store: store,
            embedded: true,
            active: true,
            singleDrama: _a,
            playerFactory: () {
              final p = ScriptedPlayer();
              platforms.add(p);
              return Player(platformPlayer: p);
            },
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await _pump(tester, 80);

    for (final p in platforms) {
      p.state = p.state.copyWith(position: const Duration(seconds: 90));
      p.positionController.add(const Duration(seconds: 90));
    }
    await _pump(tester, 80);
    expect(repo.preloads, contains('甲剧2'), reason: '应预加载第 2 集');

    repo.resolves.clear();
    // 0.8.13：同上，起点取视频画面左侧空白处。
    await tester.flingFrom(_videoPoint(tester), const Offset(0, -400), 1200);
    await _pump(tester, 120);

    expect(repo.resolves, isEmpty, reason: '切下一集不应重新解析');
  });

  testWidgets('0.8.15：切上一集同样复用预载（不再现解析）', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);

    final repo = _Repo();
    final platforms = <ScriptedPlayer>[];
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: Scaffold(
          body: VerticalFeedScreen(
            key: const ValueKey('feed'),
            repository: repo,
            store: store,
            embedded: true,
            active: true,
            singleDrama: _a,
            playerFactory: () {
              final p = ScriptedPlayer();
              platforms.add(p);
              return Player(platformPlayer: p);
            },
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await _pump(tester, 80);

    // 先切到第 2 集（这一步没有预载可用，会现解析，属于正常）。
    await tester.flingFrom(_videoPoint(tester), const Offset(0, -400), 1200);
    await _pump(tester, 120);

    // 让第 2 集播起来，触发"两侧预载"。
    for (final p in platforms) {
      p.state = p.state.copyWith(position: const Duration(seconds: 90));
      p.positionController.add(const Duration(seconds: 90));
    }
    await _pump(tester, 80);

    expect(
      repo.preloads,
      contains('甲剧1'),
      reason: '0.8.15 起上一集也要预载，否则回看时整条解析链路都暴露在切集那一刻',
    );

    repo.resolves.clear();
    await tester.flingFrom(_videoPoint(tester), const Offset(0, 400), 1200);
    await _pump(tester, 120);

    expect(repo.resolves, isEmpty, reason: '切上一集不应重新解析播放地址');
  });

  testWidgets('0.6.9：预载时机提前到 25%（原为过半）', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);

    final repo = _Repo();
    final platforms = await _mount(tester, repo, store);
    await _pump(tester, 80);

    for (final p in platforms) {
      p.state = p.state.copyWith(position: const Duration(seconds: 40));
      p.positionController.add(const Duration(seconds: 40));
    }
    await _pump(tester, 80);

    expect(repo.preloads, isNotEmpty, reason: '播到 40 秒（时长的 33%）就应开始预载，不该等到过半');
  });
}
