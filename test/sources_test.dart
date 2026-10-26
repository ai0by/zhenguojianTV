import 'dart:async';
import 'dart:convert';

import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class SourceFixtureRepository extends FixtureRepository {
  final statuses = <String, SourceStatus>{};
  final operations = <String>[];
  final statusRequests = <String>[];
  Completer<SourceStatus>? pending;

  @override
  bool get supportsSourceManagement => true;

  @override
  Future<SourceStatus> sourceStatus(String source) async {
    statusRequests.add(source);
    return statuses[source] ??
        SourceStatus.fromJson({
          'source': source,
          'count': 3,
          'page': 2,
          'hasMore': true,
        });
  }

  @override
  Future<SourceStatus> startSourceJob(
    String source,
    String operation, {
    Drama? drama,
  }) async {
    operations.add('$source:$operation');
    if (pending != null) return pending!.future;
    return statuses[source] = SourceStatus.fromJson({
      'source': source,
      'count': 3,
      'running': true,
      'operation': operation,
      'stage': '查找新剧',
    });
  }

  @override
  Future<SourceStatus> cancelSourceJob(String source) async {
    operations.add('$source:cancel');
    return statuses[source] = SourceStatus.fromJson({
      'source': source,
      'count': 3,
      'running': false,
      'stage': '已停止',
    });
  }
}

void main() {
  // 0.8.10 第三批：香蕉（xiangjiao）暂时下线。
  //
  // 用户口径是"整个香蕉源暂时屏蔽掉不展示"：入口不能再出现，但旧存档、
  // 局域网同步、下载记录里已经存在的 `xiangjiao:xxx` 仍然要能解析出名字，
  // 否则会掉回红果、变成"未知站源"。恢复时把 xiangjiao 加回
  // SourceSite.values 即可，这条断言就是那时要改的地方。
  test('retired source stays resolvable but is not selectable', () {
    expect(SourceSite.knownValues.map((source) => source.id), contains('xiangjiao'));
    expect(SourceSite.values.map((source) => source.id), isNot(contains('xiangjiao')));
    expect(SourceSite.isAvailable('xiangjiao'), isFalse);
    expect(SourceSite.byId('xiangjiao').name, '香蕉');
  });

  test('source picker labels include anime and short-drama sources', () {
    final sources = {
      for (final source in SourceSite.knownValues) source.id: source,
    };
    expect(sources.keys, containsAll(['yeguo', 'xiangjiao', 'ezdmw']));
    expect(sources['yeguo']?.name, '野果');
    expect(sources['xiangjiao']?.name, '香蕉');
    expect(sources['51duanju']?.name, '51');
    expect(sources['huangguo-video']?.name, '剧场');
    expect(sources['huangguoai']?.name, 'AI');
    expect(sources['cloudfront']?.name, '旧版');
    expect(sources['yeguo']?.onlineSearch, isTrue);
    expect(sources['yeguo']?.pagedSearch, isTrue);
    expect(sources['xiangjiao']?.onlineSearch, isTrue);
    expect(sources['ezdmw']?.name, 'E站');
    expect(sources['ezdmw']?.onlineSearch, isTrue);
    expect(sources['ezdmw']?.pagedSearch, isTrue);
  });

  // 0.8.11：新增四个苹果CMS 采集站。它们共用一套适配器，差异只在路由配置里，
  // 但搜索能力不一样 —— 网果的搜索页被 WAF 拦死，花果的搜索页不认 page，
  // 这两条如果哪天被"顺手统一"了，用户点搜索就会看到一个空列表或重复的第一页。
  test('apple cms sources declare their real search capabilities', () {
    final sources = {
      for (final source in SourceSite.knownValues) source.id: source,
    };
    for (final id in ['huaguo', 'wangguo', 'faguo', 'wuguo']) {
      expect(SourceSite.isAvailable(id), isTrue, reason: '$id 应可选');
    }
    expect(sources['huaguo']?.name, '花果');
    expect(sources['wangguo']?.name, '网果');
    expect(sources['faguo']?.name, '发果');
    expect(sources['wuguo']?.name, '伍果');

    expect(sources['huaguo']?.onlineSearch, isTrue);
    expect(sources['huaguo']?.pagedSearch, isFalse);
    expect(sources['wangguo']?.onlineSearch, isFalse);
    expect(sources['faguo']?.onlineSearch, isTrue);
    expect(sources['faguo']?.pagedSearch, isTrue);
    expect(sources['wuguo']?.onlineSearch, isTrue);
    expect(sources['wuguo']?.pagedSearch, isTrue);
  });

  testWidgets(
    'source update prevents duplicate submits and can stop without losing cache',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final repository = SourceFixtureRepository()
        ..pending = Completer<SourceStatus>();
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      final update = find.byKey(const ValueKey('update-hongguo'));
      await tester.tap(update);
      await tester.pump();
      await tester.tap(update);
      expect(repository.operations, ['hongguo:update']);
      expect(tester.widget<FilledButton>(update).onPressed, isNull);
      repository.pending!.complete(
        SourceStatus.fromJson({
          'source': 'hongguo',
          'count': 3,
          'running': true,
          'stage': '查找新剧',
        }),
      );
      await tester.pump();
      await tester.tap(find.text('停止').first);
      await tester.pumpAndSettle();
      expect(repository.operations, ['hongguo:update', 'hongguo:cancel']);
      expect(find.text('已停止'), findsOneWidget);
      expect(find.text('3 部'), findsWidgets);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );

  testWidgets(
    'source manager requests only the current user permitted compiled sources',
    (tester) async {
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
            name: '只看红果',
            sources: ['hongguo'],
            download: false,
          ).toJson(),
        ]),
        'activeProfile': 'viewer',
      });
      final store = LocalStore(await SharedPreferences.getInstance());
      final repository = SourceFixtureRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      expect(repository.statusRequests, ['hongguo']);
      expect(find.byKey(const ValueKey('source-hongguo')), findsOneWidget);
      for (final source in SourceSite.knownValues.skip(1)) {
        expect(find.byKey(ValueKey('source-${source.id}')), findsNothing);
      }
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );

  for (final operation in ['check', 'checkCatalog']) {
    testWidgets(
      'source diagnostics remain collapsed through $operation polling and completion',
      (tester) async {
        SharedPreferences.setMockInitialValues({});
        final store = LocalStore(await SharedPreferences.getInstance());
        SourceStatus status(String state, {bool running = false}) =>
            SourceStatus.fromJson({
              'source': 'hongguo',
              'count': 7,
              'operation': operation,
              'running': running,
              'stage': running ? '检测连接' : '检测完成',
              'health': {
                'state': state,
                'checkedAt': '2026-09-21T00:00:00Z',
                'sample': '合成检测剧集',
                'steps': [
                  {
                    'name': '入口与目录',
                    'state': 'ok',
                    'message': '目录可达',
                    'httpStatus': 200,
                  },
                ],
              },
            });
        final repository = SourceFixtureRepository()
          ..statuses['hongguo'] = status('ok')
          ..pending = Completer<SourceStatus>();
        await tester.pumpWidget(
          MaterialApp(
            home: SourcesScreen(repository: repository, store: store),
          ),
        );
        await tester.pumpAndSettle();
        final toggle = find.byKey(const ValueKey('health-toggle-hongguo'));
        final sample = find.text('检测剧集：合成检测剧集');
        expect(toggle, findsOneWidget);
        expect(sample, findsNothing);
        expect(find.byTooltip('复制诊断信息'), findsOneWidget);

        if (operation == 'check') {
          await tester.tap(find.byKey(const ValueKey('check-hongguo')));
        } else {
          await tester.tap(find.byTooltip('红果更多操作'));
          await tester.pumpAndSettle();
          await tester.tap(find.text('仅检测目录'));
        }
        await tester.pump();
        expect(repository.operations, ['hongguo:$operation']);
        expect(sample, findsOneWidget);
        await tester.ensureVisible(toggle);
        await tester.tap(toggle);
        await tester.pump();
        expect(sample, findsNothing);

        final checking = status('checking', running: true);
        repository.statuses['hongguo'] = checking;
        repository.pending!.complete(checking);
        repository.pending = null;
        await tester.pump();
        expect(find.text('检测中'), findsOneWidget);
        expect(sample, findsNothing);

        final requests = repository.statusRequests.length;
        await tester.pump(const Duration(seconds: 2));
        await tester.pump();
        expect(repository.statusRequests.length, greaterThan(requests));
        expect(sample, findsNothing);

        repository.statuses['hongguo'] = status(
          operation == 'check' ? 'ok' : 'catalogOnly',
        );
        await tester.pump(const Duration(seconds: 2));
        await tester.pumpAndSettle();
        expect(
          find.text(operation == 'check' ? '连接检测通过' : '目录正常 · 播放未检测'),
          findsOneWidget,
        );
        expect(sample, findsNothing);

        await tester.tap(toggle);
        await tester.pumpAndSettle();
        expect(sample, findsOneWidget);
        expect(find.text('入口与目录：目录可达'), findsOneWidget);
        await tester.tap(toggle);
        await tester.pumpAndSettle();
        expect(sample, findsNothing);
        await tester.pump(const Duration(seconds: 10));
        await tester.pump();
        expect(sample, findsNothing);
        expect(find.byTooltip('复制诊断信息'), findsOneWidget);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
        store.dispose();
      },
    );
  }

  testWidgets(
    'source diagnostics distinguish CF playback failure on a narrow screen',
    (tester) async {
      tester.view.physicalSize = const Size(320, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final repository = SourceFixtureRepository();
      repository.statuses['hongguo'] = SourceStatus.fromJson({
        'source': 'hongguo',
        'count': 7,
        'page': 3,
        'hasMore': true,
        'error': '合成 Cloudflare 验证错误',
        'health': {
          'state': 'failed',
          'checkedAt': '2026-09-19T03:00:00Z',
          'sample': '无图合成数据',
          'steps': [
            {
              'name': '入口与目录',
              'state': 'ok',
              'message': '目录可达',
              'httpStatus': 200,
            },
            {
              'name': '播放地址与播放列表',
              'state': 'failed',
              'message': 'Cloudflare 要求浏览器验证',
              'httpStatus': 403,
              'host': 'example.test',
            },
          ],
        },
      });
      await tester.pumpWidget(
        MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: const TextScaler.linear(1.5)),
            child: child!,
          ),
          home: SourcesScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('检测未通过'), findsOneWidget);
      expect(find.textContaining('HTTP 403'), findsNothing);
      final toggle = find.byKey(const ValueKey('health-toggle-hongguo'));
      await tester.ensureVisible(toggle);
      await tester.tap(toggle);
      await tester.pumpAndSettle();
      expect(find.textContaining('HTTP 403'), findsOneWidget);
      expect(find.textContaining('连接检测通过'), findsNothing);
      await tester.tap(toggle);
      await tester.pumpAndSettle();
      expect(find.textContaining('HTTP 403'), findsNothing);
      expect(find.textContaining('检测未通过'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
}
