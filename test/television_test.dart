import 'package:duanju_app/app_layout.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/remote_widgets.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'remote_test_helpers.dart';

class TelevisionRepository extends FixtureRepository {
  @override
  Future<DramaDetail> detail(Drama drama) async {
    detailCalls++;
    return DramaDetail(
      drama,
      List.generate(
        100,
        (index) => Episode({
          'id': '${index + 1}',
          'currentEpisode': index + 1,
          'vip': true,
        }, index + 1),
      ),
    );
  }
}

void main() {
  testWidgets(
    'RemoteTarget handles TV confirm keys without ancestor Shortcuts',
    (tester) async {
      var activations = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: RemoteTarget(
              key: const ValueKey('standalone-remote-target'),
              autofocus: true,
              label: '独立遥控目标',
              onPressed: () => activations++,
              child: const SizedBox(width: 120, height: 64),
            ),
          ),
        ),
      );
      await tester.pump();
      await tester.sendKeyEvent(LogicalKeyboardKey.select);
      await tester.pump();
      expect(activations, 1);
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pump();
      expect(activations, 2);
    },
  );

  Future<LocalStore> makeStore() async {
    // 0.8.15 第二批：黄豆等属于「黑洞」站源，默认不展示；
    // 这组用例按 `SourceSite.values` 的分组顺序驱动站源弹窗，先打开黑洞。
    SharedPreferences.setMockInitialValues({'blackhole': true});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  void size(WidgetTester tester, Size size) {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
  }

  Future<void> press(WidgetTester tester, LogicalKeyboardKey key) async {
    if (key == LogicalKeyboardKey.goBack) {
      await tester.binding.handlePopRoute();
    } else {
      await tester.sendKeyEvent(key);
    }
    await tester.pumpAndSettle();
  }

  testWidgets(
    'Android TV detection falls back safely when the platform channel is unavailable',
    (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      addTearDown(
        () => messenger.setMockMethodCallHandler(AppDevice.channel, null),
      );
      messenger.setMockMethodCallHandler(AppDevice.channel, (call) async {
        expect(call.method, 'deviceInfo');
        return {'television': true, 'version': appVersion};
      });
      final detected = await AppDevice.detect();
      expect(detected.television, isTrue);
      expect(detected.version, appVersion);
      messenger.setMockMethodCallHandler(
        AppDevice.channel,
        (call) async => throw MissingPluginException(),
      );
      expect((await AppDevice.detect()).television, isFalse);
      debugDefaultTargetPlatformOverride = null;
    },
  );

  testWidgets(
    'manual interface preference overrides device detection and survives restart',
    (tester) async {
      size(tester, const Size(960, 540));
      final store = await makeStore();
      final repository = FixtureRepository();
      await tester.pumpWidget(
        DuanjuApp(repository: repository, store: store, television: true),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('tv-nav-0')), findsOneWidget);
      // 0.8.7：电视版去掉顶部栏，「界面模式」改从左侧菜单进设置页切换。
      await tester.tap(find.byKey(const ValueKey('tv-nav-settings')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('display-mode-setting')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('手机 / 电脑'));
      await tester.pumpAndSettle();
      expect(store.displayMode, 'standard');
      expect(LocalStore(store.preferences).displayMode, 'standard');
      await press(tester, LogicalKeyboardKey.goBack);
      expect(find.byKey(const ValueKey('tv-nav-0')), findsNothing);
      await store.setDisplayMode('television');
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('tv-nav-0')), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  for (final dimensions in [const Size(960, 540), const Size(1280, 720)]) {
    testWidgets(
      'TV sources, details, VIP confirmation and back retain remote focus at $dimensions',
      (tester) async {
        size(tester, dimensions);
        final store = await makeStore();
        final repository = TelevisionRepository();
        await tester.pumpWidget(
          DuanjuApp(repository: repository, store: store, television: true),
        );
        await tester.pumpAndSettle();
        if (SourceSite.values.length > 1) {
          // 0.8.2 起电视版站源入口是 RemoteTarget（可聚焦的胶囊），
          // 弹窗改成居中对话框 + 可聚焦勾选行，所以这里按真实遥控路径驱动：
          // 打开 → 清空 → 勾选第二个分组 → 确定。
          // 0.8.7：入口从顶部栏挪到左侧菜单。
          final switcher = find.byKey(const ValueKey('tv-nav-source'));
          focusRemote(tester, switcher);
          await tester.pumpAndSettle();
          await press(tester, LogicalKeyboardKey.select);
          expect(find.text('选择站源'), findsOneWidget);
          focusRemote(tester, find.byKey(const ValueKey('source-sheet-clear')));
          await tester.pumpAndSettle();
          await press(tester, LogicalKeyboardKey.select);
          final target = SourceGroup.fromSources(SourceSite.values)[1];
          focusRemote(
            tester,
            find.byKey(ValueKey('source-sheet-${target.id}')),
          );
          await tester.pumpAndSettle();
          await press(tester, LogicalKeyboardKey.select);
          focusRemote(
            tester,
            find.byKey(const ValueKey('source-sheet-confirm')),
          );
          await tester.pumpAndSettle();
          await press(tester, LogicalKeyboardKey.select);
          expect(find.text('选择站源'), findsNothing);
        }
        expect(
          repository.requests.last,
          SourceSite.values.length > 1 ? SourceSite.values[1].id : 'hongguo',
        );
        // 0.8.3：电视首页改成横向海报行，剧库网格在「发现」标签里。
        // 这里先按遥控器切到「发现」，再按剧卡片 id 找目标。
        focusRemote(tester, find.byKey(const ValueKey('tv-nav-1')));
        await tester.pumpAndSettle();
        await press(tester, LogicalKeyboardKey.select);
        expect(find.byKey(const ValueKey('tv-nav-1')), findsOneWidget);
        focusRemote(tester, find.byKey(ValueKey(FixtureRepository.free.id)));
        await tester.pumpAndSettle();
        await press(tester, LogicalKeyboardKey.select);
        expect(repository.detailCalls, 1);
        expect(find.byKey(const ValueKey('start-play')), findsOneWidget);
        focusRemote(tester, find.byKey(const ValueKey('episode-1')));
        await tester.pumpAndSettle();
        await press(tester, LogicalKeyboardKey.select);
        expect(find.text('这是一集 VIP 内容'), findsOneWidget);
        await press(tester, LogicalKeyboardKey.goBack);
        expect(find.text('这是一集 VIP 内容'), findsNothing);
        expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-1');
        await press(tester, LogicalKeyboardKey.escape);
        expect(
          FocusManager.instance.primaryFocus?.debugLabel,
          'remote-${FixtureRepository.free.id}',
        );
        expect(tester.takeException(), isNull);
      },
    );
  }

  testWidgets(
    'remote grid reaches unbuilt rows, partial last row and pagination without touch',
    (tester) async {
      size(tester, const Size(960, 540));
      var loaded = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: RemoteGrid(
              itemKeys: List.generate(59, (index) => '$index'),
              columns: 4,
              itemExtent: 100,
              autofocus: true,
              footer: Center(
                child: RemoteButton(label: '加载更多', onPressed: () => loaded++),
              ),
              itemBuilder: (_, index, node, onFocus) => RemoteEpisodeButton(
                number: index,
                focusNode: node,
                onFocus: onFocus,
                onPressed: () {},
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-0');
      for (var index = 0; index < 3; index++) {
        await press(tester, LogicalKeyboardKey.arrowRight);
      }
      for (var row = 0; row < 14; row++) {
        await press(tester, LogicalKeyboardKey.arrowDown);
      }
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-58');
      await press(tester, LogicalKeyboardKey.arrowDown);
      await press(tester, LogicalKeyboardKey.select);
      expect(loaded, 1);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('episode grid initially focuses an offscreen saved episode', (
    tester,
  ) async {
    size(tester, const Size(960, 540));
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RemoteGrid(
            itemKeys: List.generate(160, (index) => '$index'),
            columns: 6,
            itemExtent: 64,
            initialIndex: 131,
            autofocus: true,
            itemBuilder: (_, index, node, onFocus) => RemoteEpisodeButton(
              number: index,
              focusNode: node,
              onFocus: onFocus,
              onPressed: () {},
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-131');
    await press(tester, LogicalKeyboardKey.arrowUp);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-125');
    expect(tester.takeException(), isNull);
  });

  testWidgets('TV back from a non-home tab returns home and moves nav focus', (
    tester,
  ) async {
    size(tester, const Size(960, 540));
    final store = await makeStore();
    await tester.pumpWidget(
      DuanjuApp(
        repository: FixtureRepository(),
        store: store,
        television: true,
      ),
    );
    await tester.pumpAndSettle();

    // 切到「最近观看」（下标 3）。
    focusRemote(tester, find.byKey(const ValueKey('tv-nav-3')));
    await tester.pumpAndSettle();
    await press(tester, LogicalKeyboardKey.select);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'tv-nav-focus-3');

    // 返回：页面回首页，左侧菜单焦点必须一并回到「首页」（0.8.9）。
    await press(tester, LogicalKeyboardKey.goBack);
    expect(
      FocusManager.instance.primaryFocus?.debugLabel,
      'tv-nav-focus-0',
      reason: '退回首页后光标不能停在「最近观看」上',
    );
    expect(tester.takeException(), isNull);
  });
}
