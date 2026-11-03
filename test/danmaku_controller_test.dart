import 'package:flutter_test/flutter_test.dart';

import 'package:duanju_app/danmaku_controller.dart';
import 'package:duanju_app/danmaku_models.dart';
import 'package:duanju_app/models.dart';

import 'fixtures.dart';

/// 可控时钟：`testWidgets` 的假时钟只管 Timer，`DateTime.now` 还是真实时间，
/// 而退避判定用的是后者 —— 不注入时钟，"定时器到点了但退避还没到"会成为假失败。
class _Clock {
  DateTime value = DateTime(2026, 1, 1);
  DateTime call() => value;
  void advance(Duration delta) => value = value.add(delta);
}

/// 只覆盖弹幕接口的假仓库（其余沿用 FixtureRepository）。
class _DanmakuRepository extends FixtureRepository {
  _DanmakuRepository({this.failuresBeforeSuccess = 0});

  /// 前 N 次请求失败，用来验证"失败之后自己会重试"。
  final int failuresBeforeSuccess;
  int calls = 0;

  @override
  Future<DanmakuPage> danmaku(
    PlaybackPlan plan, {
    required int startMs,
    required int durationMs,
  }) async {
    calls++;
    if (calls <= failuresBeforeSuccess) {
      throw Exception('偶发超时');
    }
    final raw = startMs + danmakuWindowMs;
    return DanmakuPage(
      episodeId: plan.danmakuId,
      startMs: startMs,
      nextMs: raw <= durationMs ? raw : durationMs,
      items: [
        DanmakuItem(id: 'c$calls', text: '第 $calls 次', timeMs: startMs + 1000),
      ],
    );
  }
}

PlaybackPlan _plan([String id = '7001']) => PlaybackPlan(
  url: 'http://127.0.0.1/$id',
  session: 'session-$id',
  danmakuId: id,
);

/// 统一的播放状态：5 分钟时长、已出画面。
void _feed(DanmakuController controller, {int positionMs = 0, bool playing = true}) {
  controller.update(
    position: Duration(milliseconds: positionMs),
    duration: const Duration(minutes: 5),
    speed: 1,
    playing: playing,
    buffering: false,
    foreground: true,
    available: true,
  );
}

void main() {
  testWidgets('首次加载成功即有弹幕', (tester) async {
    final repository = _DanmakuRepository();
    final clock = _Clock();
    final controller = DanmakuController(repository, now: clock.call)
      ..setPlan(_plan());
    _feed(controller);
    await tester.pump();
    expect(repository.calls, 1);
    expect(controller.items, hasLength(1));
    expect(controller.canRetry, isFalse);
    controller.dispose();
  });

  testWidgets('失败后即使没有位置 tick 也会自己重试', (tester) async {
    final repository = _DanmakuRepository(failuresBeforeSuccess: 1);
    final clock = _Clock();
    final controller = DanmakuController(repository, now: clock.call)
      ..setPlan(_plan());
    _feed(controller);
    await tester.pump();
    expect(repository.calls, 1);
    expect(controller.items, isEmpty);
    expect(controller.canRetry, isTrue);

    // 关键点：这里**不再喂任何 update**，完全靠退避定时器自己醒来。
    clock.advance(const Duration(seconds: 3));
    await tester.pump(const Duration(seconds: 3));
    await tester.pump();
    expect(repository.calls, 2);
    expect(controller.items, hasLength(1));
    expect(controller.canRetry, isFalse);
    controller.dispose();
  });

  testWidgets('手动重试清掉退避立即再取', (tester) async {
    final repository = _DanmakuRepository(failuresBeforeSuccess: 1);
    final clock = _Clock();
    final controller = DanmakuController(repository, now: clock.call)
      ..setPlan(_plan());
    _feed(controller, playing: false);
    await tester.pump();
    expect(controller.canRetry, isTrue);
    controller.retry();
    await tester.pump();
    expect(repository.calls, 2);
    expect(controller.items, hasLength(1));
    controller.dispose();
  });

  testWidgets('时长抖动 1 秒内不清空已载入的弹幕', (tester) async {
    final repository = _DanmakuRepository();
    final clock = _Clock();
    final controller = DanmakuController(repository, now: clock.call)
      ..setPlan(_plan());
    _feed(controller);
    await tester.pump();
    expect(controller.items, hasLength(1));
    clock.advance(const Duration(milliseconds: 500));
    controller.update(
      position: const Duration(milliseconds: 500),
      duration: const Duration(minutes: 5, milliseconds: 300),
      speed: 1,
      playing: true,
      buffering: false,
      foreground: true,
      available: true,
    );
    await tester.pump();
    expect(controller.items, hasLength(1));
    expect(repository.calls, 1);
    controller.dispose();
  });

  testWidgets('换集时清空上一集已载入的弹幕', (tester) async {
    final repository = _DanmakuRepository();
    final clock = _Clock();
    final controller = DanmakuController(repository, now: clock.call)
      ..setPlan(_plan());
    _feed(controller);
    await tester.pump();
    expect(controller.items, hasLength(1));
    controller.setPlan(_plan('7002'));
    expect(controller.items, isEmpty);
    controller.dispose();
  });
}
