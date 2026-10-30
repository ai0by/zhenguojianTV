import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:duanju_app/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

void main() {
  Future<void> settle(WidgetTester tester) async {
    for (var i = 0; i < 20; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  /// 用固定 key 挂载，保证 `active` 变化时是 `didUpdateWidget` 而不是重建 State。
  Future<void> pumpFeed(
    WidgetTester tester, {
    required RouteRepository repository,
    required ScriptedPlayer player,
    required LocalStore store,
    required bool active,
  }) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: Scaffold(
          body: VerticalFeedScreen(
            key: const ValueKey('feed'),
            repository: repository,
            store: store,
            embedded: true,
            singleDrama: FixtureRepository.free,
            active: active,
            playerFactory: () => Player(platformPlayer: player),
            // 测试环境不创建 VideoController（会走原生通道并失败）。
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await settle(tester);
  }

  Future<(LocalStore, RouteRepository, ScriptedPlayer)> setUpFeed(
    WidgetTester tester, {
    required bool active,
  }) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final repository = RouteRepository();
    final player = ScriptedPlayer();
    await pumpFeed(
      tester,
      repository: repository,
      player: player,
      store: store,
      active: active,
    );
    return (store, repository, player);
  }

  testWidgets('当前页能正常开始加载并播放（回归：0.3.5 曾卡在"正在加载视频…"）', (tester) async {
    final (_, _, player) = await setUpFeed(tester, active: true);
    expect(
      player.opened,
      hasLength(1),
      reason: '当前页挂载后必须真的把媒体交给播放器，否则界面会一直卡在加载中',
    );
    // 0.8.16 第三批：加载态改成纯动效、没有文案了，改断言组件本身。
    expect(find.byType(PlaybackLoading), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('未激活的页面不创建播放器，变为当前页后才加载', (tester) async {
    final (store, repository, player) = await setUpFeed(tester, active: false);
    expect(player.opened, isEmpty);
    await pumpFeed(
      tester,
      repository: repository,
      player: player,
      store: store,
      active: true,
    );
    expect(player.opened, hasLength(1));
  });
}
