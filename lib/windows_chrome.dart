import 'dart:async';
import 'dart:ui' show ImageFilter;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'app_build.dart';
import 'windows_pip.dart';

/// 播放页「沉浸模式」开关（0.8.31）。
///
/// 桌面播放页把自己注册进来，最外层 [WindowsDesktopFrame] 收到通知后：
/// 画面铺满整窗（不再给固定标题栏让出 36px），标题栏变成「鼠标移到窗口
/// 最上方才淡入」的半透明玻璃浮层。用户要的正是「播放时不要固定顶部栏」。
///
/// 用引用计数而不是单个 bool：全屏 overlay 与竖屏播放页可能短暂并存，
/// 谁先退出都不能提前把顶栏叫回来。只有 Windows 播放页会调它，
/// 发现 / 资料库等页面仍然是常驻的品牌标题栏。
class WindowsChrome {
  WindowsChrome._();

  static final ValueNotifier<bool> immersive = ValueNotifier<bool>(false);
  static int _depth = 0;

  static void pushImmersive() {
    _depth++;
    if (!immersive.value) immersive.value = true;
  }

  static void popImmersive() {
    if (_depth > 0) _depth--;
    if (_depth == 0 && immersive.value) immersive.value = false;
  }
}

// Windows 画中画见 lib/windows_pip.dart + windows/runner/flutter_window.cpp：
// 浮窗形态切换 + 全局热键 Ctrl+Shift+P，方案取舍记在 docs/TODO.md。

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
    // 0.8.31：改成「画面铺底 + 标题栏浮层」的 Stack。旧的 Column 会把一条
    // 36px 亮色栏永久压在视频上方，黑画面配白顶栏割裂得厉害，播放页尤其难看。
    //
    // 结构恒定：两个子节点永远在树里，只改 padding / Offstage / 透明度。
    // `child` 里挂着播放器，增删节点或换父类型都会让正在播的那一路被 dispose。
    return AnimatedBuilder(
      animation: Listenable.merge([
        WindowsPip.instance,
        WindowsChrome.immersive,
      ]),
      builder: (context, _) {
        final pip = WindowsPip.instance.active;
        final immersive = WindowsChrome.immersive.value;
        return Stack(
          children: [
            // 普通页面让出 36px 给常驻标题栏；播放页 / PiP 铺满整窗。
            Positioned.fill(
              child: AnimatedPadding(
                duration: const Duration(milliseconds: 180),
                curve: Curves.easeOut,
                padding: EdgeInsets.only(
                  top: (pip || immersive) ? 0 : WindowsTitleBar.barHeight,
                ),
                child: WindowsPipOverlay(child: child),
              ),
            ),
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              child: Offstage(
                offstage: pip,
                child: WindowsTitleBar(immersive: immersive),
              ),
            ),
          ],
        );
      },
    );
  }
}

/// 自绘标题栏本体。高度按 Windows 习惯给 36 逻辑像素。
///
/// [immersive] 为 true 时（播放页）默认完全隐藏，鼠标靠近窗口最上沿才淡入
/// 一条半透明玻璃条；窗口按钮仍可用，但不会在黑画面上留一条常驻亮带。
class WindowsTitleBar extends StatefulWidget {
  const WindowsTitleBar({super.key, this.immersive = false});

  static const double barHeight = 36;

  /// 播放页沉浸模式：隐藏 → hover 唤出，并改用深色玻璃配色。
  final bool immersive;

  @override
  State<WindowsTitleBar> createState() => _WindowsTitleBarState();
}

class _WindowsTitleBarState extends State<WindowsTitleBar>
    with WindowListener {
  bool _maximized = false;
  bool _hovered = false;

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
    final colors = Theme.of(context).colorScheme;
    final bar = _bar(context, colors);
    if (!widget.immersive) return bar;
    // 沉浸：默认完全透明也不可点，鼠标移到窗口最上方才淡入。
    //
    // 命中区单独放一层 Listener(translucent)，不能指望 AnimatedOpacity：
    // RenderOpacity 在 opacity=0 时直接跳过 hitTest，那样永远唤不回来。
    // MouseRegion 必须挂最外层 —— 挂在下层的话鼠标一挪到玻璃条上的按钮，
    // 它就不在命中路径里，立刻 onExit 把刚出现的栏又收回去。
    return MouseRegion(
      opaque: false,
      // 必须显式 translucent：deferToChild 时命中层返回 false，MouseRegion
      // 自己不会进入 hit-test 路径，onEnter/onExit 永远不触发。
      hitTestBehavior: HitTestBehavior.translucent,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: SizedBox(
        height: WindowsTitleBar.barHeight + 10,
        child: Stack(
          children: [
            Positioned.fill(
              child: Listener(
                behavior: HitTestBehavior.translucent,
                child: const SizedBox.expand(),
              ),
            ),
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              height: WindowsTitleBar.barHeight,
              child: IgnorePointer(
                ignoring: !_hovered,
                child: AnimatedOpacity(
                  opacity: _hovered ? 1 : 0,
                  duration: const Duration(milliseconds: 150),
                  curve: Curves.easeOut,
                  child: bar,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// 标题栏本体。普通页面跟随主题；播放页用深色玻璃 + 白字。
  Widget _bar(BuildContext context, ColorScheme colors) {
    final light = widget.immersive;
    final header = light
        ? const Color(0xB30B0B0E)
        : (Theme.of(context).appBarTheme.backgroundColor ?? colors.surface);
    final foreground = light
        ? Colors.white.withValues(alpha: .92)
        : colors.onSurface.withValues(alpha: .78);
    final bar = Material(
      color: header,
      child: SizedBox(
        height: WindowsTitleBar.barHeight,
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
                            color: foreground,
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
              light: light,
              onTap: windowManager.minimize,
            ),
            _TitleBarButton(
              icon: _maximized
                  ? Icons.filter_none_rounded
                  : Icons.crop_square_rounded,
              tooltip: _maximized ? '向下还原' : '最大化',
              light: light,
              onTap: _toggleMaximize,
            ),
            _TitleBarButton(
              icon: Icons.close_rounded,
              tooltip: '关闭',
              danger: true,
              light: light,
              onTap: windowManager.close,
            ),
          ],
        ),
      ),
    );
    if (!light) return bar;
    // 玻璃：模糊下方视频 + 半透明深底。不用硬边分隔线，纯黑画面上更干净。
    return ClipRect(
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
        child: bar,
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
    this.light = false,
  });

  final IconData icon;
  final String tooltip;
  final Future<void> Function() onTap;

  /// 关闭按钮：悬停时整块变红、图标转白，与 Windows 的习惯一致。
  final bool danger;

  /// 沉浸玻璃条：底色与图标都按白色半透明来画。
  final bool light;

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
        : widget.light
        ? Colors.white.withValues(alpha: .16)
        : colors.onSurface.withValues(alpha: .08);
    final foreground = _hovered && widget.danger
        ? Colors.white
        : widget.light
        ? Colors.white.withValues(alpha: .86)
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
