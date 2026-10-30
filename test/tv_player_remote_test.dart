import 'dart:ui' show SemanticsFlag;

import 'package:duanju_app/app_layout.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

const _drama = Drama(
  id: 'hongguo:tv-stable',
  source: 'hongguo',
  title: '电视稳定性测试剧',
  episodes: 3,
);

class _TvRepo extends FixtureRepository {
  final requestedEpisodes = <int>[];

  @override
  Future<CatalogPage> recommendations(
    String genre, {
    bool more = false,
    bool force = false,
  }) async => CatalogPage(const [_drama]);

  @override
  Future<DramaDetail> detail(Drama drama) async => DramaDetail(
    drama,
    List.generate(
      3,
      (index) => Episode({
        'id': '${index + 1}',
        'title': '第${index + 1}集',
        'currentEpisode': index + 1,
      }, index + 1),
    ),
  );

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    requestedEpisodes.add(episode.number);
    return PlaybackPlan(
      url: 'https://example.test/tv-${episode.number}.mp4',
      session: 'tv-${episode.number}-${requestedEpisodes.length}',
      qualities: const [1080, 720],
      quality: quality == 0 ? 1080 : quality,
    );
  }
}

Future<void> _pumpFrames(WidgetTester tester, [int frames = 50]) async {
  for (var i = 0; i < frames; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

Future<(LocalStore, _TvRepo, ScriptedPlayer)> _mount(
  WidgetTester tester, {
  bool singleDrama = true,
  Size size = const Size(1280, 720),
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = size;
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetPhysicalSize);
  SharedPreferences.setMockInitialValues(const {});
  final store = LocalStore(await SharedPreferences.getInstance());
  addTearDown(store.dispose);
  final repo = _TvRepo();
  final platform = ScriptedPlayer();

  await tester.pumpWidget(
    MaterialApp(
      theme: ThemeData.dark(),
      home: Shortcuts(
        shortcuts: const {
          SingleActivator(LogicalKeyboardKey.select, includeRepeats: false):
              ActivateIntent(),
          SingleActivator(
            LogicalKeyboardKey.gameButtonA,
            includeRepeats: false,
          ): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.goBack): DismissIntent(),
        },
        child: AppLayout(
          television: true,
          version: '0.8.6',
          child: VerticalFeedScreen(
            repository: repo,
            store: store,
            singleDrama: singleDrama ? _drama : null,
            playerFactory: () => Player(platformPlayer: platform),
            videoBuilder: (_) => const ColoredBox(color: Colors.black),
          ),
        ),
      ),
    ),
  );
  await _pumpFrames(tester);
  expect(repo.requestedEpisodes, isNotEmpty);
  expect(platform.state.playing, isTrue);
  return (store, repo, platform);
}

/// 当前播放器打开的是第几集。
///
/// 0.8.16 起**不能再用 `requestedEpisodes.last` 判断切集**：往回切命中
/// 本页播放计划缓存，根本不会再发一次解析请求。改看播放器实际打开
/// 的地址（`_TvRepo` 把它做成 `…/tv-<集号>.mp4`）。
int _playingEpisode(ScriptedPlayer platform) {
  final uri = platform.opened.last.uri;
  final match = RegExp(r'tv-(\d+)\.mp4$').firstMatch(uri);
  expect(match, isNotNull, reason: '播放地址应带集号：$uri');
  return int.parse(match!.group(1)!);
}

void main() {
  testWidgets(
    'TV short OK toggles once and moves focus into visible controls',
    (tester) async {
      final (_, _, platform) = await _mount(tester);
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'vertical-feed');

      await tester.sendKeyDownEvent(LogicalKeyboardKey.select);
      await tester.pump(const Duration(milliseconds: 100));
      expect(
        platform.state.playing,
        isTrue,
        reason: 'KeyDown must not pause before long-press is decided',
      );

      await tester.sendKeyUpEvent(LogicalKeyboardKey.select);
      await tester.pumpAndSettle();
      expect(platform.state.playing, isFalse);
      expect(
        FocusManager.instance.primaryFocus?.debugLabel,
        'feed-play-button',
        reason: '精简遥控器没有菜单键时，短按 OK 后仍必须能进入控件焦点链',
      );

      await tester.sendKeyEvent(LogicalKeyboardKey.escape);
      await tester.pumpAndSettle();
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'vertical-feed');
    },
  );

  testWidgets(
    'TV long OK enters 3x and release restores without toggling playback',
    (tester) async {
      final (_, _, platform) = await _mount(tester);

      await tester.sendKeyDownEvent(LogicalKeyboardKey.select);
      await tester.pump(const Duration(milliseconds: 420));
      expect(platform.rates, isNotEmpty);
      expect(platform.rates.last, 3);
      expect(platform.state.playing, isTrue);
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'vertical-feed');

      await tester.sendKeyUpEvent(LogicalKeyboardKey.select);
      await tester.pump(const Duration(milliseconds: 120));
      expect(platform.rates.last, 1);
      expect(platform.state.playing, isTrue, reason: '长按松手只能恢复倍速，不能额外触发一次暂停');
    },
  );

  testWidgets(
    'TV single-drama up/down changes episodes instead of dead-ending',
    (tester) async {
      final (_, repo, platform) = await _mount(tester);
      expect(repo.requestedEpisodes.last, 1);
      expect(_playingEpisode(platform), 1);

      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      await _pumpFrames(tester, 20);
      expect(_playingEpisode(platform), 2);

      await tester.sendKeyEvent(LogicalKeyboardKey.arrowUp);
      await _pumpFrames(tester, 20);
      expect(
        _playingEpisode(platform),
        1,
        reason: '回切第 1 集直接命中本页缓存，不再重新解析',
      );
    },
  );

  testWidgets(
    'TV rapid opposite episode keys are not swallowed by touch debounce',
    (tester) async {
      final (_, repo, platform) = await _mount(tester);
      expect(repo.requestedEpisodes.last, 1);
      expect(_playingEpisode(platform), 1);

      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      await tester.pump(const Duration(milliseconds: 120));
      expect(_playingEpisode(platform), 2);

      await tester.sendKeyEvent(LogicalKeyboardKey.arrowUp);
      await _pumpFrames(tester, 12);
      expect(
        _playingEpisode(platform),
        1,
        reason: '遥控器是离散按键，不能复用触屏 400ms 防重复窗口吞掉反向操作',
      );
    },
  );

  testWidgets('TV left/right seek works from root playback focus', (
    tester,
  ) async {
    final (_, _, platform) = await _mount(tester);
    await platform.seek(const Duration(seconds: 30));

    await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
    await tester.pump();
    expect(platform.state.position, const Duration(seconds: 40));

    await tester.sendKeyEvent(LogicalKeyboardKey.arrowLeft);
    await tester.pump();
    expect(platform.state.position, const Duration(seconds: 30));
  });

  testWidgets('TV menu is reachable and exposes all core playback actions', (
    tester,
  ) async {
    await _mount(tester, singleDrama: false);

    await tester.sendKeyEvent(LogicalKeyboardKey.contextMenu);
    await tester.pumpAndSettle();
    expect(find.text('播放设置'), findsOneWidget);
    expect(find.text('选集'), findsWidgets);
    expect(find.text('倍速'), findsWidgets);
    expect(find.text('清晰度'), findsWidgets);
    expect(find.text('连播模式'), findsWidgets);
    expect(find.text('追剧'), findsWidgets);
    expect(find.text('剧集详情'), findsWidgets);
    expect(
      find.byKey(const ValueKey('tv-player-menu-detail')),
      findsOneWidget,
      reason: '低分辨率 TV 上剧集详情也必须预先存在于遥控焦点树',
    );

    await tester.sendKeyEvent(LogicalKeyboardKey.select);
    await tester.pumpAndSettle();
    expect(find.text('选集'), findsWidgets);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'remote-episode-0');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('播放设置'), findsNothing);
  });

  testWidgets('TV 960x540 menu D-pad reaches and scrolls to bottom actions', (
    tester,
  ) async {
    await _mount(tester, singleDrama: false, size: const Size(960, 540));
    final semanticsHandle = tester.ensureSemantics();

    await tester.sendKeyEvent(LogicalKeyboardKey.contextMenu);
    await tester.pumpAndSettle();
    final detail = find.byKey(const ValueKey('tv-player-menu-detail'));
    expect(detail, findsOneWidget);

    for (var i = 0; i < 5; i++) {
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      await tester.pump(const Duration(milliseconds: 160));
    }

    final semantics = tester.getSemantics(detail);
    expect(
      semantics.hasFlag(SemanticsFlag.isFocused),
      isTrue,
      reason: '低分辨率 TV 必须能只用方向键走到底部菜单项',
    );
    final rect = tester.getRect(detail);
    expect(rect.top, greaterThanOrEqualTo(0));
    expect(rect.bottom, lessThanOrEqualTo(540));
    semanticsHandle.dispose();
  });

  testWidgets('TV back hierarchy hides controls before exit confirmation', (
    tester,
  ) async {
    final (_, _, _) = await _mount(tester);

    await tester.sendKeyEvent(LogicalKeyboardKey.select);
    await tester.pumpAndSettle();
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'feed-play-button');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('退出播放？'), findsNothing);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'vertical-feed');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('退出播放？'), findsOneWidget);
    expect(find.text('继续观看'), findsOneWidget);
  });

  testWidgets(
    'TV up/down still switch episodes while a control button has focus',
    (tester) async {
      final (_, repo, platform) = await _mount(tester);
      expect(repo.requestedEpisodes.last, 1);
      expect(_playingEpisode(platform), 1);

      // 短按 OK：暂停，并把焦点交给底部播放按钮。
      await tester.sendKeyEvent(LogicalKeyboardKey.select);
      await tester.pumpAndSettle();
      expect(platform.state.playing, isFalse);
      expect(FocusManager.instance.primaryFocus?.debugLabel, 'feed-play-button');

      // 焦点停在按钮上时，上下键仍然必须是「下一集 / 上一集」（0.8.9），
      // 不能退化成焦点遍历——控件只有一行，遍历的结果就是按了没反应。
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      await _pumpFrames(tester, 20);
      expect(_playingEpisode(platform), 2);

      await tester.sendKeyEvent(LogicalKeyboardKey.arrowUp);
      await _pumpFrames(tester, 20);
      expect(
        _playingEpisode(platform),
        1,
        reason: '回切命中缓存，不再解析；但画面必须真的回到第 1 集',
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('TV resuming from the play button returns focus to the page', (
    tester,
  ) async {
    final (_, _, platform) = await _mount(tester);

    await tester.sendKeyEvent(LogicalKeyboardKey.select);
    await tester.pumpAndSettle();
    expect(platform.state.playing, isFalse);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'feed-play-button');

    // 在播放按钮上再按一次确定 → 恢复播放，焦点必须还给播放画面，
    // 否则控件会因为"有焦点"而永远不自动隐藏（0.8.9）。
    await tester.sendKeyEvent(LogicalKeyboardKey.select);
    await tester.pumpAndSettle();
    expect(platform.state.playing, isTrue);
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'vertical-feed');
    expect(tester.takeException(), isNull);
  });

  testWidgets('TV hides the fullscreen button', (tester) async {
    await _mount(tester);
    await tester.sendKeyEvent(LogicalKeyboardKey.select);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('feed-landscape')), findsNothing);
    expect(find.byKey(const ValueKey('fullscreen-exit')), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
