import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

/// 画中画（0.8.17，手机端）。
///
/// 只做**系统级 PiP**（`Activity.enterPictureInPictureMode`），不自己画悬浮窗：
/// 自绘悬浮窗要「悬浮在其他应用之上」权限，用户得手动去系统设置里开，
/// 而且各家 ROM 限制不一，体验远不如系统小窗。
///
/// 两种进入方式，都走 `duanju/device` 通道：
/// 1. 播放页右上角的小窗按钮 → [enter]；
/// 2. 播放中按 Home / 切到别的应用 → [arm] 置 true 后由原生侧自动进入
///    （Android 12+ 用 `setAutoEnterEnabled`，8.0~11 用 `onUserLeaveHint`）。
///
/// 电视端不启用：TV 没有「小窗」概念，`MainActivity` 也会因为
/// `hasSystemFeature(FEATURE_PICTURE_IN_PICTURE)` 为假而直接返回不可用。
class PictureInPicture {
  const PictureInPicture._();

  static const channel = MethodChannel('duanju/device');

  /// 通道超时：理由同 `SystemVolume`（测试环境未 mock 的通道会永久挂起）。
  static const _timeout = Duration(milliseconds: 800);

  /// 单元测试环境直接短路，避免 `Future.timeout` 留下未清理的计时器。
  static bool get _testing => Platform.environment['FLUTTER_TEST'] == 'true';

  /// 当前是否正处在小窗里。
  ///
  /// 播放页监听它：进小窗要立刻收起控件、停掉自动隐藏计时；
  /// 退出小窗再把控件还回来（但保持"点一下才出现"的默认态）。
  static final ValueNotifier<bool> active = ValueNotifier<bool>(false);

  static bool _available = false;
  static bool _initialized = false;

  /// 设备是否支持画中画；[initialize] 之前一律 false。
  static bool get available => _available;

  /// 注册原生回调并探测设备能力。重复调用只生效一次。
  static Future<bool> initialize() async {
    if (_initialized) return _available;
    _initialized = true;
    if (_testing || defaultTargetPlatform != TargetPlatform.android) {
      return false;
    }
    channel.setMethodCallHandler((call) async {
      if (call.method == 'pictureInPictureChanged') {
        active.value = call.arguments == true;
      }
      return null;
    });
    try {
      final value = await channel
          .invokeMethod<bool>('pictureInPictureAvailable')
          .timeout(_timeout, onTimeout: () => false);
      _available = value ?? false;
    } on PlatformException {
      _available = false;
    } on MissingPluginException {
      _available = false;
    }
    return _available;
  }

  /// 主动进入小窗。返回是否真的切进去了。
  ///
  /// [aspectRatio] 是视频宽高比（宽 / 高），竖版短剧约 `9 / 16`。
  static Future<bool> enter({double aspectRatio = 9 / 16}) async {
    if (!_available) return false;
    try {
      final value = await channel
          .invokeMethod<bool>('enterPictureInPicture', {
            'aspectRatio': aspectRatio.clamp(0.42, 2.39),
          })
          .timeout(_timeout, onTimeout: () => false);
      return value ?? false;
    } on PlatformException {
      return false;
    } on MissingPluginException {
      return false;
    }
  }

  /// 是否允许"按 Home 自动进小窗"。
  ///
  /// 播放页在「正在播放」时置 true，暂停 / 离开页面 / 出错时置 false，
  /// 否则从首页退出应用也会弹一个小窗出来。
  static Future<void> arm(bool armed, {double aspectRatio = 9 / 16}) async {
    if (!_available) return;
    try {
      await channel
          .invokeMethod<bool>('setPictureInPictureArmed', {
            'armed': armed,
            'aspectRatio': aspectRatio.clamp(0.42, 2.39),
          })
          .timeout(_timeout, onTimeout: () => null);
    } on PlatformException {
      // 忽略：个别 ROM 不支持自动进入，手动按钮仍可用。
    } on MissingPluginException {
      // 忽略。
    }
  }
}
