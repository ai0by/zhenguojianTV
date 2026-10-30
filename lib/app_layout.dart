import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

export 'app_build.dart';

const appVersion = '0.8.17';

ThemeData televisionTheme(ThemeData theme) {
  final colors = theme.colorScheme;
  final focusSide = WidgetStateProperty.resolveWith<BorderSide?>(
    (states) => states.contains(WidgetState.focused)
        ? BorderSide(color: colors.primary, width: 3)
        : null,
  );
  final focusBackground = WidgetStateProperty.resolveWith<Color?>(
    (states) =>
        states.contains(WidgetState.focused) ? colors.primaryContainer : null,
  );
  // 0.8.7：电视控件整体收小一档。原来 52×48 / 18pt 是按 10 英尺视距
  // 放大的，但 720p 盒子只有 540~720 逻辑高度，一行海报加标题就顶掉半屏，
  // 列表里一屏看不到几张卡。
  final button = ButtonStyle(
    side: focusSide,
    minimumSize: const WidgetStatePropertyAll(Size(40, 36)),
    textStyle: const WidgetStatePropertyAll(TextStyle(fontSize: 15)),
  );
  return theme.copyWith(
    focusColor: colors.primaryContainer,
    iconButtonTheme: IconButtonThemeData(
      style: button.copyWith(backgroundColor: focusBackground),
    ),
    filledButtonTheme: FilledButtonThemeData(style: button),
    outlinedButtonTheme: OutlinedButtonThemeData(style: button),
    textButtonTheme: TextButtonThemeData(
      style: button.copyWith(backgroundColor: focusBackground),
    ),
    inputDecorationTheme: theme.inputDecorationTheme.copyWith(
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: BorderSide(color: colors.primary, width: 3),
      ),
    ),
    // 0.8.1 电视样式：播放器进度条在 10 英尺外要看得见，
    // 轨道加粗、滑块与焦点光圈放大（遥控器可把焦点移到进度条上）。
    sliderTheme: theme.sliderTheme.copyWith(
      trackHeight: 5,
      activeTrackColor: colors.primary,
      inactiveTrackColor: colors.onSurface.withValues(alpha: .24),
      thumbColor: colors.primary,
      thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 7),
      overlayShape: const RoundSliderOverlayShape(overlayRadius: 18),
    ),
    progressIndicatorTheme: theme.progressIndicatorTheme.copyWith(
      strokeWidth: 4,
      color: colors.primary,
    ),
    dialogTheme: theme.dialogTheme.copyWith(
      backgroundColor: theme.colorScheme.surface,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
    ),
    tooltipTheme: theme.tooltipTheme.copyWith(
      textStyle: const TextStyle(fontSize: 13),
    ),
    chipTheme: theme.chipTheme.copyWith(
      labelStyle: const TextStyle(fontSize: 13),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
    ),
  );
}

class AppDevice {
  const AppDevice({this.television = false, this.version = appVersion});
  final bool television;
  final String version;
  static const channel = MethodChannel('duanju/device');

  static Future<AppDevice> detect() async {
    if (defaultTargetPlatform != TargetPlatform.android) {
      return const AppDevice();
    }
    try {
      final data = await channel.invokeMapMethod<String, dynamic>('deviceInfo');
      return AppDevice(
        television: data?['television'] == true,
        version: data?['version'] as String? ?? appVersion,
      );
    } on PlatformException {
      return const AppDevice();
    } on MissingPluginException {
      return const AppDevice();
    }
  }
}

class AppLayout extends InheritedWidget {
  const AppLayout({
    super.key,
    required this.television,
    this.version = appVersion,
    required super.child,
  });
  final bool television;
  final String version;

  /// 电视过扫描安全边距（0.8.0）。
  ///
  /// 电视/投影仪普遍存在 3%~5% 的过扫描（overscan），贴边内容会被裁掉。
  /// 电视模式下给整个应用留出安全边距，避免侧边导航首项、底部按钮行
  /// 和进度条两端被屏幕边框吃掉。
  static const overscan = EdgeInsets.symmetric(horizontal: 24, vertical: 12);

  static bool isTelevision(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppLayout>()?.television ??
      false;
  static String versionOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppLayout>()?.version ??
      appVersion;

  @override
  bool updateShouldNotify(AppLayout oldWidget) =>
      television != oldWidget.television || version != oldWidget.version;
}
