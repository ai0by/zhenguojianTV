import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:media_kit/media_kit.dart';

import 'core_bridge.dart';
import 'media_pipeline.dart';

/// 播放位置至少推进到这里才算「播放器真的在动」。
const _targetPosition = Duration(milliseconds: 400);
const _playbackBudget = Duration(seconds: 20);
const _pollInterval = Duration(milliseconds: 100);
/// 前 2 秒还没自己走起来，就主动 seek 一次驱动状态同步。
const _kickAfter = Duration(seconds: 2);

Future<void> runPackageSmoke(List<String> arguments) async {
  if (arguments.length != 3) exit(2);
  final report = File(arguments[1]), input = arguments[2];
  Player? player;
  StreamSubscription<Duration>? positionSub;
  var positionEvents = 0;
  Duration lastStreamPosition = Duration.zero;

  Map<String, dynamic> diagnostics(Player value) => {
        'positionMs': value.state.position.inMilliseconds,
        'durationMs': value.state.duration.inMilliseconds,
        'playing': value.state.playing,
        'positionEvents': positionEvents,
        'lastStreamPositionMs': lastStreamPosition.inMilliseconds,
      };

  try {
    final repository = NativeRepository();
    await repository.initialize();
    final executor = FFmpegExecutor();
    final probe = await executor.probe(input);
    verifyMediaDuration(probe, 3);
    final remuxed = File(
      '${report.parent.path}${Platform.pathSeparator}remuxed.mkv',
    );
    await executor.run([
      '-i',
      input,
      '-map',
      '0:v:0',
      '-c',
      'copy',
      remuxed.path,
    ]);
    verifyMediaDuration(await executor.probe(remuxed.path), 3);
    player = Player(
      configuration: const PlayerConfiguration(muted: true, vo: 'null'),
    );

    // 0.8.28 之前这里写成：先 `player.stream.position.firstWhere(>=400ms)`
    // 再 `await player.open(...)`，然后 `advancing.timeout(20s)`。CI 上恒报
    // `TimeoutException after 0:00:20.000000`：判据挂在「某个事件必须送达」上，
    // 而 3 秒测试片在 headless（vo=null、无音轨、muted）下可能瞬间播完并 EOF，
    // 订阅早于 open 时那几个 position 事件会错过 / 被 open 的重置吞掉，
    // firstWhere 的 Future 就永远不完成 —— 包本身没坏，是验收写法不成立。
    //
    // 现在：先 open + play，判据只读同步状态快照 `state.position`（事件流只用来
    // 记诊断）。另外 headless 下若播放时钟不自己走，2 秒后主动 seek 一次，
    // 验证 Player ↔ libmpv 的控制链路与状态回传仍然可用 —— 这不是放宽断言，
    // 解码失败 / DLL 没加载 / seek 不响应都会照样失败。
    await player.open(Media(remuxed.path));
    await player.play();
    positionSub = player.stream.position.listen((time) {
      positionEvents++;
      lastStreamPosition = time;
    });

    final started = DateTime.now();
    var kicked = false;
    while (player.state.position < _targetPosition &&
        DateTime.now().difference(started) < _playbackBudget) {
      if (!kicked && DateTime.now().difference(started) >= _kickAfter) {
        kicked = true;
        await player.seek(const Duration(seconds: 1));
      }
      await Future<void>.delayed(_pollInterval);
    }

    final state = diagnostics(player);
    if (player.state.position < _targetPosition) {
      throw StateError('播放位置未推进到 400ms：$state');
    }

    await positionSub.cancel();
    positionSub = null;
    await report.writeAsString(
      jsonEncode({
        'ok': true,
        'nativeCore': true,
        'ffprobe': true,
        'remux': true,
        'mediaKitPlayback': true,
        ...state,
        'seekKick': kicked,
      }),
      flush: true,
    );
    await player.dispose();
    exit(0);
  } catch (error) {
    await positionSub?.cancel();
    Map<String, dynamic>? state;
    if (player != null) {
      try {
        state = diagnostics(player);
      } catch (_) {
        // dispose 之后再读状态可能抛错，诊断拿不到也不能盖掉原始错误。
      }
    }
    await player?.dispose();
    await report.writeAsString(
      jsonEncode({
        'ok': false,
        'error': error.toString(),
        if (state != null) 'state': state,
      }),
      flush: true,
    );
    exit(1);
  }
}
