import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/remote_widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

void main() {
  Future<LocalStore> store() async {
    // 0.8.15 第二批：黄豆属于「黑洞」站源，默认不展示；
    // 本用例要切到黄豆验证 VIP 过滤，先把它打开。
    SharedPreferences.setMockInitialValues({'blackhole': true});
    return LocalStore(await SharedPreferences.getInstance());
  }

  Widget catalogApp(FixtureRepository repository, LocalStore local) =>
      DuanjuApp(
        repository: repository,
        store: local,
        initialTab: 1,
        feedPlayerFactory: () => Player(platformPlayer: ScriptedPlayer()),
      );

  for (final size in [const Size(390, 844), const Size(1280, 800)]) {
    testWidgets('catalog, VIP filter and fresh details at $size', (
      tester,
    ) async {
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final repository = FixtureRepository();
      final local = await store();
      addTearDown(local.dispose);
      await tester.pumpWidget(catalogApp(repository, local));
      await tester.pumpAndSettle();
      expect(find.byTooltip('VIP：隐藏'), findsNothing);
      expect(find.text('测试短剧'), findsOneWidget);
      if (allSourcesEnabled) {
        await tester.tap(find.byKey(const ValueKey('source-switch')));
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('source-sheet-clear')));
        await tester.pumpAndSettle();
        await tester.tap(find.text('黄豆'));
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('source-sheet-confirm')));
        await tester.pumpAndSettle();
        expect(find.text('会员测试剧'), findsNothing);
        expect(find.byTooltip('VIP：隐藏'), findsOneWidget);
        await tester.tap(find.byTooltip('VIP：隐藏'));
        await tester.pumpAndSettle();
        expect(find.byTooltip('VIP：显示'), findsOneWidget);
        expect(find.text('会员测试剧'), findsOneWidget);
        await tester.tap(find.byTooltip('VIP：显示'));
        await tester.pumpAndSettle();
      } else {
        expect(find.text('黄豆'), findsNothing);
      }
      expect(find.text('会员测试剧'), findsNothing);
      await tester.tap(find.text('测试短剧'));
      await tester.pumpAndSettle();
      expect(repository.detailCalls, 1);
      expect(find.byKey(const ValueKey('episode-2')), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('follow-status')));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(CheckedPopupMenuItem<String>).first);
      await tester.pumpAndSettle();
      expect(local.isFavorite(FixtureRepository.free.id), isTrue);
      for (final episode in [1, 2]) {
        await local.saveWatch(
          WatchEntry(
            drama: FixtureRepository.free,
            episode: episode,
            position: 18.5,
            duration: 90,
            updatedAt: DateTime.now(),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('继续播放 · 第 $episode 集'), findsOneWidget);
        for (final number in [1, 2]) {
          final button = tester.widget<RemoteEpisodeButton>(
            find.byKey(ValueKey('episode-$number')),
          );
          expect(button.current, number == episode);
        }
      }
      await local.clearHistory();
      await tester.pumpAndSettle();
      expect(find.text('立即播放'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('failed catalog can be retried', (tester) async {
    final repository = FixtureRepository()..fail = true;
    final local = await store();
    addTearDown(local.dispose);
    await tester.pumpWidget(catalogApp(repository, local));
    await tester.pumpAndSettle();
    expect(find.text('暂时无法加载'), findsOneWidget);
    repository.fail = false;
    await tester.tap(find.text('重试'));
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
  });

  testWidgets('fresh disk catalog skips the network and resumes pagination', (
    tester,
  ) async {
    final repository = FixtureRepository();
    repository.cachedPages['hongguo'] = CatalogPage(
      [FixtureRepository.free],
      fresh: true,
      page: 3,
      hasMore: true,
    );
    final local = await store();
    addTearDown(local.dispose);
    await tester.pumpWidget(catalogApp(repository, local));
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
    expect(repository.requests, isEmpty);
    await tester.scrollUntilVisible(
      find.text('加载更多'),
      200,
      scrollable: find.descendant(
        of: find.byType(CustomScrollView),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.tap(find.text('加载更多'));
    await tester.pumpAndSettle();
    expect(repository.pages, [4]);
    final refresh = find.byKey(const ValueKey('catalog-refresh'));
    if (refresh.evaluate().isNotEmpty) {
      await tester.tap(refresh);
    } else {
      await tester.tap(find.byTooltip('更多'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('更新剧库'));
    }
    await tester.pumpAndSettle();
    expect(repository.pages, [4, 1]);
    expect(repository.forced, [false, true]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('stale disk catalog remains usable when refresh fails', (
    tester,
  ) async {
    final repository = FixtureRepository()..fail = true;
    repository.cachedPages['hongguo'] = CatalogPage([FixtureRepository.free]);
    final local = await store();
    addTearDown(local.dispose);
    await tester.pumpWidget(catalogApp(repository, local));
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
    expect(find.text('合成网络错误'), findsOneWidget);
    expect(find.text('暂时无法加载'), findsNothing);
    expect(repository.requests, ['hongguo']);
    expect(tester.takeException(), isNull);
  });

  test('watch progress and favorites survive a new store', () async {
    final local = await store();
    await local.saveWatch(
      WatchEntry(
        drama: FixtureRepository.free,
        episode: 2,
        position: 18.5,
        duration: 90,
        updatedAt: DateTime.now(),
      ),
    );
    await local.toggleFavorite(FixtureRepository.free);
    final restored = LocalStore(local.preferences);
    expect(restored.watched(FixtureRepository.free.id)?.position, 18.5);
    expect(restored.watched(FixtureRepository.free.id)?.episode, 2);
    expect(restored.isFavorite(FixtureRepository.free.id), isTrue);
    local.dispose();
    restored.dispose();
  });
}
