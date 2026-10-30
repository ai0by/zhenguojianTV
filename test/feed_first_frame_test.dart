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

const _a = Drama(id: 'hongguo:1', source: 'hongguo', title: '甲剧', episodes: 3);

class _Repo extends FixtureRepository {
  final resolves = <String>[];

  @override
  Future<CatalogPage> recommendations(
    String genre, {
    bool more = false,
    bool force = false,
  }) async => CatalogPage(const [_a]);

  @override
  Future<DramaDetail> detail(Drama drama) async => DramaDetail(drama, [
    Episode({'id': '1', 'title': '第1集', 'currentEpisode': 1}, 1),
    Episode({'id': '2', 'title': '第2集', 'currentEpisode': 2}, 2),
  ]);

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    resolves.add('${episode.number}');
    return PlaybackPlan(
      url: 'https://example.test/r${episode.number}.mp4',
      session: 'R${episode.number}-${resolves.length}',
    );
  }
}

/// 模拟真机弱网：`open()` 已返回（地址交给播放器），但**始终没有起播**。
///
/// 这正是"黑屏没有任何反应"的场景：没有错误、没有画面。
class _NeverPlays extends ScriptedPlayer {
  @override
  Future<void> open(Playable playable, {bool play = true}) async {
    final media = playable as Media;
    opened.add(media);
    played.add(play);
    state = state.copyWith(
      position: Duration.zero,
      duration: const Duration(minutes: 2),
      playing: false, // 关键：始终未起播
      completed: false,
      buffering: false,
      width: 320,
      height: 180,
    );
    durationController.add(state.duration);
    positionController.add(Duration.zero);
  }
}

Future<void> _pump(WidgetTester tester, int frames) async {
  for (var i = 0; i < frames; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

void main() {
  late LocalStore store;

  setUp(() async {
    SharedPreferences.setMockInitialValues(const {});
    store = LocalStore(await SharedPreferences.getInstance());
  });

  tearDown(() => store.dispose());

  testWidgets('0.6.9：未起播时**保留加载提示**（不再纯黑无反应）', (tester) async {
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
              final p = _NeverPlays();
              platforms.add(p);
              return Player(platformPlayer: p);
            },
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await _pump(tester, 60);

    // 地址已交给播放器（open 被调用过）。
    expect(platforms, isNotEmpty);
    expect(platforms.last.opened, isNotEmpty, reason: '地址应已交给播放器');
    expect(platforms.last.state.playing, isFalse, reason: '模拟的是"未起播"');

    // 核心断言：未起播期间**必须有加载提示**，不能纯黑无任何反馈。
    // 0.8.16 第三批：提示从文字换成纯动效，断言组件本身。
    expect(
      find.byType(PlaybackLoading),
      findsWidgets,
      reason: '地址已交出但尚未起播时，应保留加载动效，不能纯黑无反应',
    );
    // 也不应误报错误。
    expect(find.textContaining('播放已中断'), findsNothing);
  });

  testWidgets('0.6.9：迟迟不起播时**自动恢复**（不再需要手动再滑一次）', (tester) async {
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
              final p = _NeverPlays();
              platforms.add(p);
              return Player(platformPlayer: p);
            },
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await _pump(tester, 60);

    final resolvesBefore = repo.resolves.length;
    debugPrint('等待前 resolve 次数 = $resolvesBefore');

    // 推进假时钟超过"未起播"容忍轮次（15 秒）。
    await _pump(tester, 1000);

    final resolvesAfter = repo.resolves.length;
    debugPrint('等待 20 秒后 resolve 次数 = $resolvesAfter');

    // 核心断言：应触发自动恢复（重新解析地址）。
    expect(
      resolvesAfter,
      greaterThan(resolvesBefore),
      reason: '迟迟不起播时应自动恢复（重新获取播放地址），'
          '而不是永久黑屏等用户手动再滑一次',
    );
  });
}
