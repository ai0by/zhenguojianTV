import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'app_build.dart';

/// Windows 桌面版的窗口外框（自绘标题栏）。
///
/// 为什么自绘：Flutter 的 Windows 模板给的是**系统原生标题栏** —— 一块不跟随
/// 应用主题的白条，上面还挂着旧品牌名和陈旧图标，与下面的暖白 / 深色界面拼在
/// 一起就是"两条割裂的顶栏"。这里把原生标题栏隐藏（见 `main.dart` 的
/// `TitleBarStyle.hidden`），改用 Flutter 画一条跟随主题的品牌栏：
/// 应用图标 + 应用名 + 可拖动区域 + 最小化 / 最大化 / 关闭。
///
/// **只在 Windows 上启用**：判定用 `defaultTargetPlatform`（而不是
/// `Platform.isWindows`），这样 `flutter test`（测试环境的默认目标平台是
/// Android）与手机 / 电视版都拿不到这个外框，现有的 widget 测试不受影响。
class WindowsDesktopFrame extends StatelessWidget {
  const WindowsDesktopFrame({super.key, required this.child});

  final Widget child;

  /// 当前是否运行在真正的 Windows 桌面版上。
  static bool get supported =>
      !kIsWeb && defaultTargetPlatform == TargetPlatform.windows;

  @override
  Widget build(BuildContext context) {
    if (!supported) return child;
    return Column(
      children: [
        const WindowsTitleBar(),
        // 标题栏固定占顶部一条，其余高度留给应用的 Navigator。
        Expanded(child: child),
      ],
    );
  }
}

/// 自绘标题栏本体。高度按 Windows 习惯给 36 逻辑像素。
class WindowsTitleBar extends StatefulWidget {
  const WindowsTitleBar({super.key, this.height = 36});

  final double height;

  @override
  State<WindowsTitleBar> createState() => _WindowsTitleBarState();
}

class _WindowsTitleBarState extends State<WindowsTitleBar>
    with WindowListener {
  bool _maximized = false;

  @override
  void initState() {
    super.initState();
    windowManager.addListener(this);
    _readMaximized();
  }

  @override
  void dispose() {
    windowManager.removeListener(this);
    super.dispose();
  }

  /// 上电时窗口可能还没就绪，读不到状态就按"未最大化"画，等监听回调纠正。
  Future<void> _readMaximized() async {
    try {
      final maximized = await windowManager.isMaximized();
      if (mounted && maximized != _maximized) {
        setState(() => _maximized = maximized);
      }
    } catch (_) {
      // 窗口尚未就绪：保持默认外观即可，不要因为一次读状态失败就崩掉外框。
    }
  }

  @override
  void onWindowMaximize() {
    if (mounted) setState(() => _maximized = true);
  }

  @override
  void onWindowUnmaximize() {
    if (mounted) setState(() => _maximized = false);
  }

  /// 双击标题栏 / 点最大化按钮都走这里：最大化与还原互为反操作。
  Future<void> _toggleMaximize() async {
    try {
      if (await windowManager.isMaximized()) {
        await windowManager.unmaximize();
      } else {
        await windowManager.maximize();
      }
    } catch (_) {
      // 窗口正在销毁等边界情况：忽略，交给系统标题栏的既有行为兜底。
    }
  }

  /// 拖动窗口。
  ///
  /// 这里必须自己 catch：`startDragging` 走的是原生 MethodChannel，窗口还没
  /// 就绪或正在关闭时会抛 `PlatformException`；`unawaited` 不会替我们收异常，
  /// 一个漏出去的异步错误在 debug 下会直接弹红屏。
  Future<void> _drag() async {
    try {
      await windowManager.startDragging();
    } catch (_) {
      // 同上：拖不动就拖不动，不影响点击。
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    // 与下面的 AppBar 用**同一个底色**，两条拼成一块完整的头部；分隔线只留
    // AppBar 底部那一条（见 `AppTheme` 的 `appBarTheme.shape`）。各画各的线
    // 会在窗口顶部出现两条平行细线，比原来那条系统白条还脏。
    final header = theme.appBarTheme.backgroundColor ?? colors.surface;
    return Material(
      color: header,
      child: SizedBox(
        height: widget.height,
        child: Row(
          children: [
            // 左侧整块是拖动区。窗口按钮必须留在外面 —— 包进来的话点关闭
            // 会先被拖动逻辑吃掉，变成"按了没反应"。
            Expanded(
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onPanStart: (_) => unawaited(_drag()),
                onDoubleTap: () => unawaited(_toggleMaximize()),
                child: Padding(
                  padding: const EdgeInsets.only(left: 12, right: 12),
                  child: Row(
                    children: [
                      ClipRRect(
                        borderRadius: BorderRadius.circular(4),
                        child: Image.asset(
                          'assets/logo.jpg',
                          width: 18,
                          height: 18,
                          // 18 逻辑像素按 2x 解码就够，别整张 400×400 解进来。
                          cacheWidth: 36,
                          filterQuality: FilterQuality.medium,
                        ),
                      ),
                      const SizedBox(width: 8),
                      Flexible(
                        child: Text(
                          appName,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontSize: 12.5,
                            height: 1,
                            fontWeight: FontWeight.w600,
                            letterSpacing: .3,
                            color: colors.onSurface.withValues(alpha: .78),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
            _TitleBarButton(
              icon: Icons.remove_rounded,
              tooltip: '最小化',
              onTap: windowManager.minimize,
            ),
            _TitleBarButton(
              icon: _maximized
                  ? Icons.filter_none_rounded
                  : Icons.crop_square_rounded,
              tooltip: _maximized ? '向下还原' : '最大化',
              onTap: _toggleMaximize,
            ),
            _TitleBarButton(
              icon: Icons.close_rounded,
              tooltip: '关闭',
              danger: true,
              onTap: windowManager.close,
            ),
          ],
        ),
      ),
    );
  }
}

/// 标题栏右侧的窗口按钮：46×36 的命中区，悬停才出底色。
///
/// 不用 `InkWell` 的水波纹 —— 窗口按钮是"系统级"操作，Material 的涟漪在
/// 这一条上显得很脏；图标按钮的默认水波纹还会溢出到标题栏外。
class _TitleBarButton extends StatefulWidget {
  const _TitleBarButton({
    required this.icon,
    required this.tooltip,
    required this.onTap,
    this.danger = false,
  });

  final IconData icon;
  final String tooltip;
  final Future<void> Function() onTap;

  /// 关闭按钮：悬停时整块变红、图标转白，与 Windows 的习惯一致。
  final bool danger;

  @override
  State<_TitleBarButton> createState() => _TitleBarButtonState();
}

class _TitleBarButtonState extends State<_TitleBarButton> {
  bool _hovered = false;

  /// 窗口操作一律吞掉异常：最小化 / 关闭走原生通道，窗口正在销毁时可能抛，
  /// 一个漏出去的异步错误会盖住界面。
  Future<void> _run(Future<void> Function() action) async {
    try {
      await action();
    } catch (_) {
      // 忽略：用户再点一次即可。
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final background = !_hovered
        ? Colors.transparent
        : widget.danger
        ? const Color(0xFFD93025)
        : colors.onSurface.withValues(alpha: .08);
    final foreground = _hovered && widget.danger
        ? Colors.white
        : colors.onSurface.withValues(alpha: .74);
    return Tooltip(
      message: widget.tooltip,
      waitDuration: const Duration(milliseconds: 600),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hovered = true),
        onExit: (_) => setState(() => _hovered = false),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => unawaited(_run(widget.onTap)),
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 110),
            width: 46,
            height: double.infinity,
            color: background,
            alignment: Alignment.center,
            child: Icon(widget.icon, size: 15, color: foreground),
          ),
        ),
      ),
    );
  }
}
