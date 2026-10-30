import 'dart:async';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

/// 来回切集的零等待（0.8.16 第二批）。
///
/// 用户反馈：向下滑切上一集时会先显示「正在加载」，而且**画面还停在
/// 本集**，等上一集解析好才真正切过去。根因是换集时把上一集的播放
/// 会话 `release` 掉了 —— 6 → 5 命中预载，5 → 6 却要重新解析。
///
/// 下面几条守住「播过的集留在本页缓存里，回切零网络」。
class _Repo extends RouteRepository {
  /// 集数（默认 5 集，够验证缓存上限）。
  int total = 5;

  /// 非空时 `resolve` 会挂在这里，用来观察"加载中"的中间态。
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
    // 先记下来再挂住：测试要靠它确认"确实已经切到这一集了"。
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

  Future<_Repo> mount(WidgetTester tester, {int episodes = 5}) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final repository = _Repo()..total = episodes;
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
            // 单剧模式默认开启「连播本剧」，上下滑就是剧内切集。
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

  /// 拖动一屏并松手，方向由 [up] 决定（up = 上滑 = 下一集）。
  Future<void> swipe(WidgetTester tester, {required bool up}) async {
    final gesture = await tester.startGesture(const Offset(210, 420));
    final delta = up ? -300.0 : 300.0;
    await gesture.moveBy(Offset(0, delta));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.moveBy(Offset(0, delta * 0.4));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.up();
    await settle(tester);
  }

  testWidgets('0.8.16：来回切集复用本页缓存，不再重新解析', (tester) async {
    final repo = await mount(tester);
    expect(repo.requestedEpisodes, [1], reason: '首集要现场解析');

    await swipe(tester, up: true);
    expect(repo.requestedEpisodes, [1, 2], reason: '切到第 2 集：没准备过，解析一次');

    await swipe(tester, up: false);
    expect(
      repo.requestedEpisodes,
      [1, 2],
      reason: '回切第 1 集应命中本页缓存，一次解析都不该再发',
    );

    await swipe(tester, up: true);
    expect(
      repo.requestedEpisodes,
      [1, 2],
      reason: '再切回第 2 集同样命中缓存 —— 这才是抖音式的来回切',
    );
  });

  testWidgets('0.8.16：切走时旧一集的会话不立刻释放，留在缓存里', (tester) async {
    final repo = await mount(tester);
    await swipe(tester, up: true);

    expect(
      repo.active,
      contains('route-1'),
      reason: '第 1 集的播放会话要留着，回切才不用重新解析',
    );
  });

  testWidgets('0.8.16：缓存上限 3 集，更早的集会被释放', (tester) async {
    final repo = await mount(tester);
    for (var i = 0; i < 4; i++) {
      await swipe(tester, up: true);
    }
    // 播到第 5 集：缓存 3 集 + 当前这一集 = 最多 4 份会话在手里。
    expect(
      repo.active.length,
      lessThanOrEqualTo(4),
      reason: '不能无限攒播放会话，原生侧一共只有 12 个名额',
    );
    expect(repo.active, contains('route-5'), reason: '当前这一集必须还在');
  });

  testWidgets('0.8.16：加载遮罩延迟 220ms 才出现，短加载完全无感', (tester) async {
    final repo = await mount(tester);
    repo.gate = Completer<void>();

    // 上滑切集：解析被挂住，进入加载态。
    final gesture = await tester.startGesture(const Offset(210, 420));
    await gesture.moveBy(const Offset(0, -300));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.moveBy(const Offset(0, -120));
    await tester.pump(const Duration(milliseconds: 16));
    await gesture.up();
    // 先跑一帧把 ticker 启动起来（`AnimationController.forward` 的起始时间
    // 是在**第一帧**才记的），再推进 220ms 让 200ms 的滑出动画跑完。
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 220));
    expect(
      repo.requestedEpisodes.last,
      2,
      reason: '先确认切集确实发生了，否则下面的断言测的不是遮罩',
    );
    // 换集后 40ms：还没到 220ms 的遮罩延迟。
    await tester.pump(const Duration(milliseconds: 40));

    expect(
      find.text('正在加载视频…'),
      findsNothing,
      reason: '切过去才 40ms，遮罩还没到显示时间，用户不该看到转圈',
    );

    // 再走 400ms，累计远超 220ms 延迟。
    await tester.pump(const Duration(milliseconds: 400));
    expect(
      find.text('正在加载视频…'),
      findsOneWidget,
      reason: '超过 220ms 还在加载，必须给出反馈',
    );

    repo.gate!.complete();
    repo.gate = null;
    await settle(tester);
    expect(find.text('正在加载视频…'), findsNothing);
  });
}
