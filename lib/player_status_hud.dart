import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// 播放页顶部的时间 / 电量指示条（0.8.19）。
///
/// 为什么自己画，而不是把系统状态栏叫出来：
///
/// - 用户反馈"全屏时点击画面看不到时间和电量"。全屏路由自己管系统栏，
///   横屏下把状态栏叫出来会在画面顶端压一条黑带，观感比自绘差；
/// - 更关键的是**抖动**：非全屏时靠 `edgeToEdge` ↔ `immersiveSticky` 来回切
///   来实现"点一下显示电量时间"，每次切换窗口 inset 都会变，
///   `Video` 的 `BoxFit.contain` 跟着重算尺寸 —— 用户看到的就是"点一下画面跳一下"。
///
/// 现在播放页在播放期间**恒定**沉浸模式（窗口尺寸不再变化 → 不抖），
/// 需要给用户看时间电量时用这个组件画在控件层顶部。竖屏、全屏、电视共用，
/// 三处表现一致。
class PlayerStatusHud extends StatefulWidget {
  const PlayerStatusHud({super.key, this.compact = false});

  /// 电视端没有状态栏概念，字号略大一点更协调。
  final bool compact;

  @override
  State<PlayerStatusHud> createState() => _PlayerStatusHudState();
}

class _PlayerStatusHudState extends State<PlayerStatusHud> {
  /// 时钟对齐到整分钟刷新：每 20 秒醒一次，跨分钟才重建。
  static const Duration _clockInterval = Duration(seconds: 20);

  /// 电量变化很慢，30 秒一次足够（`BatteryManager` 是本地调用，开销可忽略）。
  static const Duration _batteryInterval = Duration(seconds: 30);

  Timer? _clockTimer;
  Timer? _batteryTimer;
  DateTime _now = DateTime.now();
  int? _level;
  bool _charging = false;

  /// 单元测试环境短路：理由同 `SystemVolume`（未 mock 的通道会永久挂起，
  /// 定时器还会在用例结束时留下 "A Timer is still pending"）。
  static bool get _testing => Platform.environment['FLUTTER_TEST'] == 'true';

  @override
  void initState() {
    super.initState();
    if (_testing) return;
    _clockTimer = Timer.periodic(_clockInterval, (_) {
      final now = DateTime.now();
      // 同一分钟内不重建，省掉每分钟两次的无谓刷新。
      if (now.minute == _now.minute && now.hour == _now.hour) return;
      if (mounted) setState(() => _now = now);
    });
    unawaited(_refreshBattery());
    _batteryTimer = Timer.periodic(_batteryInterval, (_) {
      unawaited(_refreshBattery());
    });
  }

  Future<void> _refreshBattery() async {
    final status = await BatteryStatus.read();
    if (!mounted || status == null) return;
    if (status.level == _level && status.charging == _charging) return;
    setState(() {
      _level = status.level;
      _charging = status.charging;
    });
  }

  @override
  void dispose() {
    _clockTimer?.cancel();
    _batteryTimer?.cancel();
    super.dispose();
  }

  IconData get _batteryIcon {
    if (_charging) return Icons.battery_charging_full_rounded;
    final level = _level;
    if (level == null) return Icons.battery_unknown_rounded;
    if (level >= 95) return Icons.battery_full_rounded;
    if (level >= 80) return Icons.battery_6_bar_rounded;
    if (level >= 60) return Icons.battery_5_bar_rounded;
    if (level >= 45) return Icons.battery_4_bar_rounded;
    if (level >= 30) return Icons.battery_3_bar_rounded;
    if (level >= 15) return Icons.battery_2_bar_rounded;
    return Icons.battery_1_bar_rounded;
  }

  @override
  Widget build(BuildContext context) {
    final size = widget.compact ? 15.0 : 13.0;
    final clock =
        '${_now.hour.toString().padLeft(2, '0')}:'
        '${_now.minute.toString().padLeft(2, '0')}';
    final style = TextStyle(
      color: Colors.white,
      fontSize: size,
      fontWeight: FontWeight.w600,
      height: 1,
      letterSpacing: .2,
      // 只靠半透明底，遇到雪景 / 白墙会糊掉；描边 + 阴影保证任何画面上都看得清。
      shadows: const [
        Shadow(color: Color(0xB3000000), blurRadius: 4, offset: Offset(0, 1)),
      ],
    );
    return Padding(
      padding: EdgeInsets.only(top: MediaQuery.paddingOf(context).top + 6),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(clock, key: const ValueKey('player-hud-clock'), style: style),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (_level != null)
                  Text(
                    '$_level%',
                    key: const ValueKey('player-hud-battery'),
                    style: style,
                  ),
                if (_level != null) const SizedBox(width: 3),
                Icon(
                  _batteryIcon,
                  size: size + 4,
                  color: Colors.white,
                  shadows: style.shadows,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// 电池状态（0.8.19）。
///
/// 走 `MainActivity` 的 `duanju/device` 通道（`batteryStatus`），
/// 用 `BatteryManager` 读，**不需要任何新权限**。
class BatteryStatus {
  const BatteryStatus._();

  static const channel = MethodChannel('duanju/device');
  static const _timeout = Duration(milliseconds: 800);

  static Future<({int level, bool charging})?> read() async {
    if (defaultTargetPlatform != TargetPlatform.android) return null;
    try {
      final data = await channel
          .invokeMapMethod<String, dynamic>('batteryStatus')
          .timeout(_timeout, onTimeout: () => null);
      if (data == null) return null;
      final level = (data['level'] as num?)?.toInt();
      if (level == null || level < 0) return null;
      return (level: level, charging: data['charging'] == true);
    } on PlatformException {
      return null;
    } on MissingPluginException {
      return null;
    }
  }
}
