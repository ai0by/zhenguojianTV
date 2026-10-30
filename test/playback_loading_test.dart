import 'dart:async';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:duanju_app/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

/// 播放页加载态（0.8.16 第三批）。
///
/// 用户要求原话：「如果有卡顿不能停留在当前页面卡顿并显示文字，这种体验感
/// 不好，这块必须跟红果、抖音保持一致逻辑，有真正的加载动效」。
///
/// 所以这里守两条：
/// 1. 加载态只有动效，**一个字都没有**；
/// 2. 播放中缓冲卡顿要过 500ms 才亮动效，恢复后立刻撤
///    （切片间隙闪一下比不亮更烦）。
class _Repo extends RouteRepository {
  int total = 3;

  /// 非空时 `resolve` 挂在这里，用来把"切集加载中"这个中间态定住。
  Completer<void>? gate;

  @override
  Future<DramaDetail> detail(Drama drama) async {
    detailCalls++;
    return DramaDetail(drama, [
      for (var i = 1; i <= total; i++)
        Episode({'id': '$i', 'title': '第$i集', 'currentEpisode': i}, i),
    ]);
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    requestedEpisodes.add(episode.number);
    final pending = gate;
    if (pending != null) await pending.future;
    primaryCalls++;
    requestedQualities.add(quality);
    return plan(false);
  }
}

void main() {
  Future<void> settle(WidgetTester tester, {int frames = 60}) async {
    for (var i = 0; i < frames; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  Future<(_Repo, ScriptedPlayer)> mount(WidgetTester tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final repository = _Repo();
    final platform = ScriptedPlayer();
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
            active: true,
            playerFactory: () => Player(platformPlayer: platform),
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await settle(tester);
    return (repository, platform);
  }

  /// 上滑切下一集；[hold] 为真时只把手指抬起来那一帧跑完就返回，
  /// 让调用方自己控制后续时序。
  Future<void> swipeUp(WidgetTester tester, {bool hold = false}) async {
    final gesture = await tester.startGesture(const Offset(210, 420));
    await gesture.moveBy(const Offset(0, -300));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.moveBy(const Offset(0, -120));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.up();
    if (!hold) await settle(tester);
  }

  testWidgets('0.8.16：加载态是纯动效，页面上没有任何"正在加载"文字', (tester) async {
    final (repo, _) = await mount(tester);
    // 首集已经播起来，遮罩必须撤干净，否则下面的断言测的不是切集那条。
    expect(find.byKey(const ValueKey('feed-switch-loading')), findsNothing);

    repo.gate = Completer<void>();
    await swipeUp(tester, hold: true);
    // 先跑一帧启动 ticker，再推 220ms 让滑出动画跑完 + 过掉遮罩延迟。
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 220));
    await tester.pump(const Duration(milliseconds: 400));

    expect(
      repo.requestedEpisodes.last,
      2,
      reason: '先确认切集确实发生了，否则测的不是加载态',
    );
    expect(
      find.byKey(const ValueKey('feed-switch-loading')),
      findsOneWidget,
      reason: '切集还在取流，必须有加载动效',
    );
    expect(
      find.byType(PlaybackLoading),
      findsWidgets,
      reason: '动效组件必须真的挂上去了',
    );
    // 核心：抖音 / 红果的加载态没有一个字。
    expect(
      find.textContaining('正在加载'),
      findsNothing,
      reason: '加载态不允许出现文案，只要动效',
    );

    repo.gate!.complete();
    repo.gate = null;
    await settle(tester);
    expect(
      find.byKey(const ValueKey('feed-switch-loading')),
      findsNothing,
      reason: '取流结束，切集遮罩必须撤掉',
    );
  });

  testWidgets('0.8.16：缓冲超过 500ms 才亮动效，恢复后立刻撤掉', (tester) async {
    final (_, platform) = await mount(tester);
    expect(find.byType(PlaybackLoading), findsNothing, reason: '正常播放时不该有动效');

    // 播放中卡住（播放器报 buffering，位置不再推进）。
    platform.buffering(true);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    expect(
      find.byType(PlaybackLoading),
      findsNothing,
      reason: '300ms 还在 500ms 门槛内，切片间隙闪一下反而更烦',
    );

    await tester.pump(const Duration(milliseconds: 300));
    expect(
      find.byType(PlaybackLoading),
      findsOneWidget,
      reason: '卡过 500ms 就该亮动效，让用户知道还在等数据',
    );

    // 缓冲恢复：动效必须立刻撤。
    platform.buffering(false);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(
      find.byType(PlaybackLoading),
      findsNothing,
      reason: '缓冲一恢复就要撤掉动效，不能挂着',
    );
  });
}
