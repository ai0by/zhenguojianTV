import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

abstract final class AppTheme {
  static final light = _theme(Brightness.light);
  static final dark = _theme(Brightness.dark);

  static ThemeMode mode(String preference) => switch (preference) {
    'light' => ThemeMode.light,
    'dark' => ThemeMode.dark,
    _ => ThemeMode.system,
  };

  static String label(String preference) => switch (preference) {
    'light' => '浅色',
    'dark' => '深色',
    _ => '跟随系统',
  };

  static SystemUiOverlayStyle systemBars(Brightness brightness) {
    final icons = brightness == Brightness.dark
        ? Brightness.light
        : Brightness.dark;
    return SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: icons,
      statusBarBrightness: brightness,
      systemStatusBarContrastEnforced: false,
      systemNavigationBarColor: Colors.transparent,
      systemNavigationBarDividerColor: Colors.transparent,
      systemNavigationBarIconBrightness: icons,
      systemNavigationBarContrastEnforced: false,
    );
  }

  static ThemeData _theme(Brightness brightness) {
    final dark = brightness == Brightness.dark;
    final background = dark ? const Color(0xFF101114) : const Color(0xFFFAF8F6);
    final scheme = ColorScheme.fromSeed(
      seedColor: const Color(0xFFFF664F),
      brightness: brightness,
      primary: dark ? const Color(0xFFFF765F) : const Color(0xFFAD3826),
      onPrimary: dark ? const Color(0xFF3B0E07) : Colors.white,
      primaryContainer: dark
          ? const Color(0xFF5C3027)
          : const Color(0xFFFFE0D8),
      onPrimaryContainer: dark
          ? const Color(0xFFFFE0D8)
          : const Color(0xFF4B160D),
      tertiary: dark ? const Color(0xFFF6C86B) : const Color(0xFF805500),
      surface: dark ? const Color(0xFF16171B) : Colors.white,
      onSurface: dark ? const Color(0xFFF2F0F4) : const Color(0xFF252328),
      onSurfaceVariant: dark
          ? const Color(0xFFB9B6C2)
          : const Color(0xFF68636C),
      outline: dark ? const Color(0xFF85818B) : const Color(0xFF7C757D),
      outlineVariant: dark ? const Color(0xFF34343D) : const Color(0xFFE3DFDC),
      surfaceContainerLowest: dark ? const Color(0xFF0D0E11) : Colors.white,
      surfaceContainerLow: dark
          ? const Color(0xFF191A20)
          : const Color(0xFFF7F3F0),
      surfaceContainer: dark
          ? const Color(0xFF1E2026)
          : const Color(0xFFF2EEEB),
      surfaceContainerHigh: dark
          ? const Color(0xFF25262D)
          : const Color(0xFFEDE8E5),
      surfaceContainerHighest: dark
          ? const Color(0xFF30313A)
          : const Color(0xFFE6E0DC),
      surfaceTint: Colors.transparent,
    );
    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: scheme,
      scaffoldBackgroundColor: background,
      appBarTheme: AppBarTheme(
        backgroundColor: background,
        foregroundColor: scheme.onSurface,
        scrolledUnderElevation: 0,
        elevation: 0,
        centerTitle: false,
        systemOverlayStyle: systemBars(brightness),
        // 顶栏与内容之间拉一条 1px 细线。浅色底上阴影会发灰、显脏，细线更干净，
        // 也让桌面版的「自绘标题栏 / 顶栏 / 内容」三层各自成形。
        shape: Border(bottom: BorderSide(color: scheme.outlineVariant)),
      ),
      dividerTheme: DividerThemeData(color: scheme.outlineVariant),
      // 桌面版（Windows）滚动条是常驻可见的，默认那条又细又淡还贴着边。
      // 统一成 6px 圆角、悬停加深、四周留 2px 间隙，宽度不再随内容抖动。
      scrollbarTheme: ScrollbarThemeData(
        thickness: const WidgetStatePropertyAll(6),
        radius: const Radius.circular(3),
        crossAxisMargin: 2,
        mainAxisMargin: 2,
        interactive: true,
        trackColor: const WidgetStatePropertyAll(Colors.transparent),
        thumbColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.hovered)
              ? scheme.onSurface.withValues(alpha: .38)
              : scheme.onSurface.withValues(alpha: .2),
        ),
      ),
      // Tooltip 在桌面上是高频出现的元素（顶栏图标、下载按钮都靠它说明），
      // Material 3 默认那套浅色气泡在深色界面上几乎看不见，这里统一成
      // 深色气泡 + 白字，深浅主题下都清晰。
      tooltipTheme: TooltipThemeData(
        waitDuration: const Duration(milliseconds: 500),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: dark ? const Color(0xFF3A3B43) : const Color(0xFF34353C),
          borderRadius: BorderRadius.circular(8),
        ),
        textStyle: const TextStyle(
          fontSize: 12,
          height: 1.2,
          fontWeight: FontWeight.w500,
          color: Colors.white,
        ),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: background,
        // 0.8.27：桌面侧栏改成「胶囊指示器 + 品牌色图标」。
        // 原来关掉指示器只剩图标变色，在暖白底上几乎看不出哪一项是选中的；
        // 桌面用户靠鼠标点，需要一块明确的落点提示。
        useIndicator: true,
        indicatorColor: scheme.primary.withValues(alpha: dark ? .18 : .12),
        indicatorShape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
        selectedIconTheme: IconThemeData(color: scheme.primary),
        unselectedIconTheme: IconThemeData(color: scheme.onSurfaceVariant),
        selectedLabelTextStyle: TextStyle(
          color: scheme.primary,
          fontWeight: FontWeight.w700,
        ),
        unselectedLabelTextStyle: TextStyle(color: scheme.onSurfaceVariant),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainer,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: BorderSide.none,
        ),
      ),
    );
  }
}
