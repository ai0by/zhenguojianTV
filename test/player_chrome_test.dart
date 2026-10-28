import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/settings_screen.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

/// 只选中红果时，首页走的是「官方推荐流」（`recommendations`），
/// 而 `FixtureRepository` 没实现它 —— 不覆盖就会一直停在加载态、
/// `feed-stage` 根本不会出现。
class _Repo extends FixtureRepository {
  @override
  Future<CatalogPage> recommendations(
    String genre, {
    bool more = false,
    bool force = false,
  }) async => CatalogPage(const [FixtureRepository.free]);
}

/// 0.8.15 第二批：播放页顶部黑边 / 状态栏显隐，以及「黑洞站源」开关。
void main() {
  Future<void> pumpFrames(WidgetTester tester, int frames) async {
    for (var i = 0; i < frames; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  testWidgets('独立播放页：顶部不再有黑条，返回按钮压在画面左上角', (tester) async {
    tester.view.physicalSize = const Size(420, 880);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: VerticalFeedScreen(
          repository: _Repo(),
          store: store,
          sources: const [SourceSite.hongguo],
          playerFactory: () => Player(platformPlayer: ScriptedPlayer()),
          videoBuilder: (controls) => controls,
        ),
      ),
    );
    await pumpFrames(tester, 80);

    // 画面铺到屏幕最上沿：原先这里是 `SafeArea` + 纯黑 `_header`，
    // 状态栏图标落在纯黑底上完全看不见（用户："黑边遮挡电量和时间"）。
    expect(tester.getTopLeft(find.byKey(const ValueKey('feed-stage'))).dy, 0);
    // 返回按钮改成画面里的浮层按钮；顶部黑条那个带 tooltip 的返回已不存在。
    expect(find.byKey(const ValueKey('feed-back')), findsOneWidget);
    expect(find.byTooltip('返回'), findsNothing);
  });

  testWidgets('设置页：黑洞开关默认关闭，打开后黑洞站源才出现', (tester) async {
    tester.view.physicalSize = const Size(420, 880);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: SettingsScreen(repository: FixtureRepository(), store: store),
      ),
    );
    await tester.pumpAndSettle();

    expect(store.blackholeEnabled, isFalse);
    final tile = find.byKey(const ValueKey('setting-blackhole'));
    expect(tile, findsOneWidget);

    await tester.ensureVisible(tile);
    await tester.pumpAndSettle();
    await tester.tap(tile);
    await tester.pumpAndSettle();
    // 落盘走的是异步队列（SharedPreferences），多泵几帧等它真正写完。
    for (var i = 0; i < 20 && !store.blackholeEnabled; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }

    expect(store.blackholeEnabled, isTrue);
    if (allSourcesEnabled) {
      // 打开后黑洞站源回到可展示列表（红果鉴只有红果，不参与这条断言）。
      expect(store.sources.map((site) => site.id), contains('huangdou'));
      expect(store.sources.length, SourceSite.values.length);
    }

    await tester.tap(tile);
    await tester.pumpAndSettle();
    for (var i = 0; i < 20 && store.blackholeEnabled; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
    expect(store.blackholeEnabled, isFalse);
    if (allSourcesEnabled) {
      expect(store.sources.map((site) => site.id), isNot(contains('huangdou')));
    }
  });
}
