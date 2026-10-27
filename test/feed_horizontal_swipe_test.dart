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
  }) async => PlaybackPlan(
    url: 'https://example.test/${drama.title}${episode.number}.mp4',
    session: 'S-${drama.title}${episode.number}',
  );

  @override
  Future<PlaybackPlan?> preload(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) async => PlaybackPlan(
    url: 'https://example.test/p-${drama.title}${episode.number}.mp4',
    session: 'P-${drama.title}${episode.number}',
  );
}

Future<void> _pump(WidgetTester tester, int frames) async {
  for (var i = 0; i < frames; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

int _currentPage(WidgetTester tester) {
  final pageView = tester.widget<PageView>(find.byType(PageView).first);
  final controller = pageView.controller;
  if (controller == null || !controller.hasClients) return 0;
  return (controller.page ?? controller.initialPage.toDouble()).round();
}

void main() {
  late LocalStore store;

  setUp(() async {
    SharedPreferences.setMockInitialValues(const {});
    store = LocalStore(await SharedPreferences.getInstance());
  });

  tearDown(() => store.dispose());

  testWidgets('横屏视频非全屏：水平滑调进度时页面不应翻页', (tester) async {
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
            sources: const [SourceSite.hongguo],
            embedded: true,
            active: true,
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
      p.videoSize(1920, 1080);
    }
    await _pump(tester, 20);

    final first = _currentPage(tester);
    await tester.dragFrom(
      const Offset(210, 120),
      const Offset(150, 40),
      touchSlopX: 0,
      touchSlopY: 0,
    );
    await _pump(tester, 40);

    final after = _currentPage(tester);
    expect(after, first, reason: '水平滑动调进度时，页面不应翻到下一部');
    expect(tester.takeException(), isNull);
  });

  testWidgets('横屏视频非全屏：先竖直 30px 再横向拖动，不应翻页', (tester) async {
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
            sources: const [SourceSite.hongguo],
            embedded: true,
            active: true,
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
      p.videoSize(1920, 1080);
    }
    await _pump(tester, 20);

    final first = _currentPage(tester);
    final gesture = await tester.startGesture(const Offset(210, 120));
    await gesture.moveBy(const Offset(0, 30));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.moveBy(const Offset(120, -8));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.up();
    await _pump(tester, 40);

    expect(_currentPage(tester), first, reason: '先竖直 30px 再转横向，不应翻页');
  });

  testWidgets('横屏视频非全屏：竖直滑仍然可以翻页（不得回退）', (tester) async {
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
            sources: const [SourceSite.hongguo],
            embedded: true,
            active: true,
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
      p.videoSize(1920, 1080);
    }
    await _pump(tester, 20);

    final first = _currentPage(tester);
    await tester.fling(
      find.byType(PageView),
      const Offset(0, -400),
      1200,
      initialOffset: const Offset(210, 120),
    );
    await _pump(tester, 120);
    final after = _currentPage(tester);
    debugPrint('竖直 fling：$first -> $after');
    expect(after, isNot(first), reason: '竖直滑动必须还能翻页');
  });
}
