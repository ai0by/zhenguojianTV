import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

/// 连播本剧切集的跟手滑动动画（0.7.14，XQ-0123）。
void main() {
  Future<void> settle(WidgetTester tester, {int frames = 60}) async {
    for (var i = 0; i < frames; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  Future<RouteRepository> mount(WidgetTester tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final repository = RouteRepository();
    final player = ScriptedPlayer();
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: Scaffold(
          body: VerticalFeedScreen(
            key: const ValueKey('feed'),
            repository: repository,
            store: store,
            embedded: true,
            // 单剧模式默认开启「连播本剧」，正好走本版新增的跟手滑动。
            singleDrama: FixtureRepository.free,
            active: true,
            playerFactory: () => Player(platformPlayer: player),
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await settle(tester);
    return repository;
  }

  /// 当前是否有视频层被位移（跟手拖动的直接证据）。
  bool hasShiftedVideo(WidgetTester tester) {
    for (final t in tester.widgetList<Transform>(find.byType(Transform))) {
      if (t.transform.storage[13].abs() > 1) return true;
    }
    return false;
  }

  testWidgets('连播本剧：拖动时画面跟着走，松手滑够就切下一集', (tester) async {
    final repository = await mount(tester);
    expect(repository.requestedEpisodes, contains(1));
    expect(hasShiftedVideo(tester), isFalse);

    // 按下并向上拖 400 像素（超过一屏 25% = 220）。
    final gesture = await tester.startGesture(const Offset(210, 120));
    await gesture.moveBy(const Offset(0, -220));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.moveBy(const Offset(0, -180));
    await tester.pump(const Duration(milliseconds: 16));

    // 拖动过程中画面必须已经跟着移动（跟手，而不是松手才动）。
    expect(hasShiftedVideo(tester), isTrue, reason: '拖动中画面应跟着手指位移');

    await gesture.up();
    await settle(tester);

    expect(
      repository.requestedEpisodes,
      contains(2),
      reason: '滑够距离后应切到第 2 集',
    );
  });

  testWidgets('连播本剧：拖动距离不够时回弹，不切集', (tester) async {
    final repository = await mount(tester);

    final gesture = await tester.startGesture(const Offset(210, 120));
    await gesture.moveBy(const Offset(0, -60));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.up();
    await settle(tester);

    expect(
      repository.requestedEpisodes,
      isNot(contains(2)),
      reason: '只拖 60 像素不应切集',
    );
    // 回弹后位移归零。
    expect(hasShiftedVideo(tester), isFalse);
  });

  testWidgets('连播本剧：一次上滑只切一集（两个入口不重复触发）', (tester) async {
    final repository = await mount(tester);

    await tester.flingFrom(const Offset(210, 120), const Offset(0, -500), 1600);
    await settle(tester);

    expect(
      repository.requestedEpisodes.where((e) => e == 2).length,
      1,
      reason: '第 2 集只应被请求一次（不重复跳集）',
    );
    expect(
      repository.requestedEpisodes,
      isNot(contains(3)),
      reason: '不应一次跳两集',
    );
  });
}
