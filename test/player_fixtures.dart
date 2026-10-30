import 'dart:async';

import 'package:duanju_app/models.dart';
import 'package:media_kit/media_kit.dart';

import 'fixtures.dart';

class ScriptedPlayer extends PlatformPlayer {
  ScriptedPlayer() : super(configuration: const PlayerConfiguration());
  final opened = <Media>[];
  final played = <bool>[];
  bool disposed = false;
  final rates = <double>[];

  @override
  Future<void> open(Playable playable, {bool play = true}) async {
    final media = playable as Media;
    opened.add(media);
    played.add(play);
    final failed = media.uri.contains('broken');
    state = state.copyWith(
      position: failed ? Duration.zero : media.start ?? Duration.zero,
      duration: failed ? Duration.zero : const Duration(minutes: 2),
      playing: play,
      completed: false,
      buffering: false,
      width: failed ? 0 : 320,
      height: failed ? 0 : 180,
    );
    positionController.add(state.position);
    durationController.add(state.duration);
    playingController.add(play);
    if (failed) {
      fail();
    }
  }

  void fail() {
    errorController.add('synthetic network failure');
    errorController.add('synthetic decoder failure');
  }

  @override
  Future<void> stop() async {
    state = state.copyWith(
      position: Duration.zero,
      duration: Duration.zero,
      playing: false,
      buffering: false,
      width: 0,
      height: 0,
    );
    positionController.add(Duration.zero);
    playingController.add(false);
  }

  @override
  Future<void> seek(Duration duration) async {
    state = state.copyWith(position: duration);
    positionController.add(duration);
  }

  @override
  Future<void> setRate(double rate) async {
    rates.add(rate);
    state = state.copyWith(rate: rate);
    rateController.add(rate);
  }

  @override
  Future<void> setVolume(double volume) async {
    state = state.copyWith(volume: volume);
    volumeController.add(volume);
  }

  void finishEpisode() {
    state = state.copyWith(
      position: state.duration,
      completed: true,
      playing: false,
    );
    positionController.add(state.position);
    completedController.add(true);
    playingController.add(false);
  }

  void videoSize(int width, int height) {
    videoParamsController.add(
      VideoParams(w: width, h: height, dw: width, dh: height),
    );
  }

  /// 0.8.16 第三批：模拟缓冲卡顿。
  ///
  /// `open()` 只会把 `buffering` 写进 `state` 而不发流，这里补上发流 ——
  /// 播放页靠 `player.stream.buffering` 判断"是不是卡住了"，不发流就
  /// 观察不到那个状态变化。
  void buffering(bool value) {
    state = state.copyWith(buffering: value);
    bufferingController.add(value);
  }

  @override
  Future<void> pause() async {
    state = state.copyWith(playing: false);
    playingController.add(false);
  }

  /// 0.6.4：播放器改为「暂停走 `pause()`、恢复走 `play()`」，
  /// 因此夹具必须实现 `play()`，否则恢复播放会落到基类的
  /// `UnimplementedError`（测试假失败）。
  @override
  Future<void> play() async {
    state = state.copyWith(playing: true, completed: false);
    playingController.add(true);
  }

  @override
  Future<void> playOrPause() async {
    state = state.copyWith(playing: !state.playing);
    playingController.add(state.playing);
  }

  @override
  Future<void> dispose() async {
    disposed = true;
    await super.dispose();
  }
}

class RouteRepository extends FixtureRepository {
  int primaryCalls = 0;
  int fallbackCalls = 0;
  final requestedQualities = <int>[];
  final requestedEpisodes = <int>[];
  bool broken = false;
  bool deferFallback = false;
  final active = <String>{};
  Completer<PlaybackPlan>? pending;

  PlaybackPlan plan(bool alternate) {
    final session = 'route-${primaryCalls + fallbackCalls}';
    active.add(session);
    return PlaybackPlan(
      url: 'https://media.test/${broken ? 'broken' : 'working'}-$session.mp4',
      session: session,
      routeIndex: alternate ? 1 : 0,
      routeCount: 2,
      quality: 1080,
      qualities: const [1080, 720],
    );
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    primaryCalls++;
    requestedQualities.add(quality);
    requestedEpisodes.add(episode.number);
    return plan(false);
  }

  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) async {
    fallbackCalls++;
    if (deferFallback) {
      pending = Completer<PlaybackPlan>();
      return pending!.future;
    }
    return plan(true);
  }

  @override
  Future<void> release(String session) async {
    active.remove(session);
  }
}
