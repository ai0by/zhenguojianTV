import 'dart:convert';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const red = FixtureRepository.free;
  const other = FixtureRepository.vip;

  test(
    'edition branding and restored source match the compiled catalog',
    () async {
      SharedPreferences.setMockInitialValues({'source': 'huangdou'});
      final store = LocalStore(await SharedPreferences.getInstance());
      expect(appName, allSourcesEnabled ? '真果鉴TV' : '红果鉴');
      expect(appSlug, allSourcesEnabled ? 'zhenguojian' : 'hongguojian');
      // 0.8.15 第二批：黄豆属于「黑洞」站源，默认关闭 → 不进可展示列表，
      // 于是默认站源回落到列表第一项（红果）。
      expect(
        store.sources.length,
        SourceSite.values.where((site) => !SourceSite.isBlackhole(site.id)).length,
      );
      expect(store.source, 'hongguo');
      // 存档里的 'huangdou' 不会被改写：打开黑洞后能自动回到黄豆。
      expect(store.preferences.getString('source'), 'huangdou');
      store.dispose();
    },
  );

  test('blackhole sources stay hidden until the setting is turned on', () async {
    SharedPreferences.setMockInitialValues({'source': 'huangdou'});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    expect(store.blackholeEnabled, isFalse);
    if (!allSourcesEnabled) {
      // 红果鉴这条分发线只有红果，黑洞开关对它没有可见影响。
      expect(store.sources.single.id, 'hongguo');
      return;
    }
    expect(store.sources.map((site) => site.id), isNot(contains('huangdou')));
    expect(store.sources.map((site) => site.id), isNot(contains('yeguo')));
    expect(store.sources.map((site) => site.id), isNot(contains('huangguoai')));
    // 权限判定不受影响：黑洞只过滤"展示"，历史 / 下载里的剧仍能播。
    expect(store.allowsSource('huangdou'), isTrue);

    await store.setBlackholeEnabled(true);
    expect(store.blackholeEnabled, isTrue);
    expect(store.sources.map((site) => site.id), contains('huangdou'));
    expect(store.sources.map((site) => site.id), contains('yeguo'));
    expect(store.sources.length, SourceSite.values.length);
    // 关掉之前存的是黄豆，重新打开后自动回到黄豆。
    expect(store.source, 'huangdou');

    await store.setBlackholeEnabled(false);
    expect(store.sources.map((site) => site.id), isNot(contains('huangdou')));
  });

  test('blackhole unlock needs the passphrase, near misses stay locked', () async {
    SharedPreferences.setMockInitialValues(const {});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);

    expect(store.blackholeEnabled, isFalse);
    // 空口令 / 少一位 / 大小写不同：一律不开，也不会把半开的存档写进去。
    expect(await store.unlockBlackhole(''), isFalse);
    expect(await store.unlockBlackhole('github-zhenguojian'), isFalse);
    expect(await store.unlockBlackhole('Github-ZhenguojianTV'), isFalse);
    expect(store.blackholeEnabled, isFalse);
    // 口令对（前后空格容错）才开。
    expect(await store.unlockBlackhole(' github-zhenguojianTV '), isTrue);
    expect(store.blackholeEnabled, isTrue);
    // 关掉不需要口令。
    await store.setBlackholeEnabled(false);
    expect(store.blackholeEnabled, isFalse);
  });

  test(
    'edition filtering preserves favorites and history through backup restore',
    () async {
      final history = [
        for (final drama in [red, other])
          WatchEntry(
            drama: drama,
            episode: 1,
            position: 12,
            duration: 60,
            updatedAt: DateTime(2026, 9, 19),
          ).toJson(),
      ];
      SharedPreferences.setMockInitialValues({
        'source': 'huangdou',
        'favorites': jsonEncode([red.toJson(), other.toJson()]),
        'history': jsonEncode(history),
      });
      final store = LocalStore(await SharedPreferences.getInstance());
      expect(store.favorites.length, allSourcesEnabled ? 2 : 1);
      expect(store.history.length, allSourcesEnabled ? 2 : 1);
      expect(store.isFavorite(other.id), allSourcesEnabled);
      expect(store.watched(other.id) != null, allSourcesEnabled);
      await store.toggleFavorite(red);
      final backup = await store.exportBackup();
      final library =
          (jsonDecode(backup)['libraries'] as Map)['default'] as Map;
      expect((library['favorites'] as List).single['id'], other.id);
      expect(library['history'], hasLength(2));
      await store.importBackup(backup);
      expect(store.preferences.getString('source'), 'huangdou');
      expect(store.history, hasLength(2));
      expect(store.favorites.single.id, other.id);
      store.dispose();
    },
  );

  test(
    'a restored foreign-source profile keeps its identity and permissions',
    () async {
      SharedPreferences.setMockInitialValues({
        'profiles': jsonEncode([
          LocalProfile(
            id: 'default',
            name: '管理员',
            admin: true,
            salt: '0' * 32,
            pinHash: '1' * 64,
          ).toJson(),
          const LocalProfile(
            id: 'viewer',
            name: '已有用户',
            sources: ['huangdou'],
            download: false,
          ).toJson(),
        ]),
        'activeProfile': 'viewer',
        'profile.viewer.source': 'huangdou',
        // 0.8.15 第二批：黄豆是黑洞站源，本用例要验证"存档里的站源
        // 能按用户权限恢复"，所以先把它打开，否则列表里根本没有这一项。
        'profile.viewer.blackhole': true,
      });
      final store = LocalStore(await SharedPreferences.getInstance());
      expect(store.profile.id, 'viewer');
      expect(store.profile.admin, isFalse);
      expect(store.profile.sources, ['huangdou']);
      expect(store.canDownload, isFalse);
      expect(store.source, allSourcesEnabled ? 'huangdou' : '');
      expect(store.allowsSource('hongguo'), isFalse);
      expect(store.allowsSource('huangdou'), allSourcesEnabled);
      store.dispose();
    },
  );

  test(
    'background requests reject unavailable sources before native I/O',
    () async {
      final repository = NativeRepository(background: true);
      final denied = [
        ...SourceSite.knownValues
            .where((source) => !SourceSite.isAvailable(source.id))
            .map((source) => source.id),
        'unknown',
      ];
      for (final source in denied) {
        final drama = Drama(id: '$source:123', source: source, title: '合成数据');
        final episode = Episode({'id': '1'}, 1);
        for (final request in [
          () => repository.catalog(source),
          () => repository.cached(source),
          () => repository.sourceStatus(source),
          () => repository.startSourceJob(source, 'update'),
          () => repository.cancelSourceJob(source),
          () => repository.detail(drama),
          () => repository.cover(drama),
          () => repository.resolve(drama, episode),
          () => repository.resolveOnline(drama, episode),
          () => repository.enqueueDownloads(DramaDetail(drama, [episode]), [
            episode,
          ]),
          () => repository.localPlayback(drama, episode),
        ]) {
          await expectLater(
            request(),
            throwsA(
              isA<AppFailure>().having(
                (error) => error.message,
                'message',
                '当前版本不包含此站源',
              ),
            ),
          );
        }
      }
    },
  );
}
