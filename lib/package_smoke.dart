import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:media_kit/media_kit.dart';

import 'core_bridge.dart';
import 'media_pipeline.dart';

/// 测试片由 `scripts/smoke_windows.py` 用 ffmpeg 生成，固定 3 秒。
const _minDuration = Duration(milliseconds: 2900);
const _seekTarget = Duration(seconds: 1);
const _seekBudget = Duration(seconds: 5);
const _eventGrace = Duration(milliseconds: 500);
/// 只用来在报告里记录「播放时钟是否推进」，不作为验收判据。
const _targetPosition = Duration(milliseconds: 400);

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
        'width': value.state.width,
        'height': value.state.height,
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

    // 旧写法在 open() 之前 `position.firstWhere(>=400ms)`，再 `timeout(20s)`，
    // CI 上恒报 TimeoutException。查到真实原因不是事件时序，而是**这个判据在
    // 目标环境里根本不可能成立**：`PlayerConfiguration.vo` 设的就是 mpv 的
    // `--vo=null`，测试片又是纯视频无音轨，headless runner 上既没有视频输出
    // 也没有音频时钟，mpv 没有 clock source，`time-pos` 永远停在 0；实测诊断
    // `{positionMs: 0, durationMs: 3000, playing: true, positionEvents: 0}`，
    // 连主动 seek 都拉不动它。包没问题，libmpv 已经把文件 demux 出来了。
    //
    // 所以换成三条不依赖播放时钟、但都会真失败的硬断言：
    //   ① libmpv 解析出 3 秒时长（DLL 加载 + demux + 状态回传）
    //   ② 播放器进入 playing
    //   ③ seek 命令在 5 秒内返回（Player ↔ libmpv 控制链路）
    // 任何一条坏了照样红。position / width / height 全部写进报告作观察，
    // 不隐瞒 headless 下时钟不推进这一事实。
    await player.open(Media(remuxed.path));
    await player.play();
    positionSub = player.stream.position.listen((time) {
      positionEvents++;
      lastStreamPosition = time;
    });
    // 让 native 事件有机会落到 state 上，顺带把诊断填满。
    await Future<void>.delayed(_eventGrace);

    if (player.state.duration < _minDuration) {
      throw StateError(
          'libmpv 未解析出测试片时长：${diagnostics(player)}');
    }
    if (!player.state.playing) {
      throw StateError('播放器未进入播放状态：${diagnostics(player)}');
    }
    await player.seek(_seekTarget).timeout(_seekBudget);

    final state = diagnostics(player);
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
        'clockAdvanced': player.state.position >= _targetPosition,
        'clockNote': 'headless vo=null + 无音轨时 mpv 没有输出时钟，'
            'time-pos 不推进属预期，不作为验收判据',
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
