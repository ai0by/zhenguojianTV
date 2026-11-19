import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/vertical_feed_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

/// 2026-11-19：已下载短剧的离线播放回归。
///
/// 用户反馈：已下载完的短剧离线打开仍要求联网才能播。根因是 _prepare() 里的
/// `repository.detail()`（在线详情，缓存过期且断网时失败）。下载页其实已经
/// 拼好了本地分集列表，通过 localDetail 传给播放器后应**零在线 detail 调用**。
class _OfflineRepo extends FixtureRepository {
  int onlineDetailCalls = 0;
  int localCalls = 0;

  @override
  Future<DramaDetail> detail(Drama drama) async {
    onlineDetailCalls++;
    throw AppFailure('离线：在线详情不可用');
  }

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localCalls++;
    return PlaybackPlan(url: '/data/local/$drama/ep${episode.number}.mp4', local: true);
  }
}

void main() {
  Future<void> settle(WidgetTester tester) async {
    for (var i = 0; i < 50; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  testWidgets('localOnly + localDetail：断网也能播已下载短剧，零在线 detail', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(420, 880);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    final repo = _OfflineRepo();
    // 下载页拼好的离线详情：含已下载的两集。
    final localDetail = DramaDetail(FixtureRepository.free, [
      Episode({'id': '1', 'title': '第1集', 'currentEpisode': 1}, 1),
      Episode({'id': '2', 'title': '第2集', 'currentEpisode': 2}, 2),
    ]);
    final player = ScriptedPlayer();

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
            singleDrama: FixtureRepository.free,
            localOnly: true,
            localDetail: localDetail,
            playerFactory: () => Player(platformPlayer: player),
            videoBuilder: (controls) => controls,
          ),
        ),
      ),
    );
    await settle(tester);

    expect(repo.onlineDetailCalls, 0, reason: '已下载播放不应调用在线详情');
    expect(repo.localCalls, greaterThan(0), reason: '应走本地文件取源');
    expect(player.opened, isNotEmpty, reason: '本地文件应交给播放器');
    expect(player.opened.first.uri, contains('local'), reason: '打开的应是本地路径');
  });
}
