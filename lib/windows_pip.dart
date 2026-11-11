import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:window_manager/window_manager.dart';

/// Windows 端画中画（0.8.28）。
///
/// Windows 没有 Android 那种由系统托管的 `enterPictureInPictureMode()`，能用的
/// 只有 Win32 窗口能力。这里做的是**窗口形态切换**：把主窗口切成一个置顶、
/// 无标题栏、可自由拖动与缩放的浮窗，窗口内容只留播放器画面（播放页本来就是
/// 「画面铺满整屏 + 控件浮层」，窗口缩小后画面自然铺满小窗，不需要重建
/// 播放器，也不会重新缓冲）。
///
/// 与「第二个 Flutter 窗口」方案的区别：后者要新起一个引擎，而 media_kit 的
/// Player 实例不能跨引擎共享，等于要开两个播放器、内存翻倍。浮窗形态在当前
/// 架构下是唯一不牺牲播放连续性的做法。
///
/// 能力：
/// - 可拖动：`startDragging()`（浮层顶部条）；
/// - 可缩放：保留 `WS_THICKFRAME`，用户能拖窗口四条边 / 四个角随意改大小，
///   这里只把 `minimumSize` 从 980×620 降到 240×135 放开下限；
/// - 全局快捷键 `Ctrl+Shift+P`：显示 / 隐藏小窗（`windows/runner/flutter_window.cpp`
///   用 `RegisterHotKey` 注册，应用失焦也能收到）；
/// - `Esc`：退出小窗，还原进入前的尺寸、位置与最大化状态；
/// - 隐藏时窗口 `hide()`，播放器不 pause，声音继续。
///
/// **测试环境保护**：判据用 `defaultTargetPlatform`（`flutter test` 默认
/// Android），测试里 `supported == false`，所有窗口调用都不会发生。
class WindowsPip extends ChangeNotifier {
  WindowsPip._();

  static final WindowsPip instance = WindowsPip._();

  /// 原生侧全局热键回传通道，名字必须与 `flutter_window.cpp` 一致。
  static const MethodChannel channel = MethodChannel('duanju/windows_pip');

  /// 首次进入小窗的默认尺寸（16:9）。用户改过之后沿用 `_pipSize`。
  static const Size defaultPipSize = Size(480, 270);
  static const Size pipMinimumSize = Size(240, 135);
  static const Size normalMinimumSize = Size(980, 620);

  static bool get supported =>
      !kIsWeb && defaultTargetPlatform == TargetPlatform.windows;

  bool _active = false;
  bool _hidden = false;
  bool _alwaysOnTop = true;
  bool _initialized = false;
  Rect? _savedBounds;
  bool _savedMaximized = false;
  Size _pipSize = defaultPipSize;
  Offset? _pipPosition;
  DateTime? _lastHotkey;

  bool get active => _active;
  bool get hidden => _hidden;
  bool get alwaysOnTop => _alwaysOnTop;

  /// 注册原生热键回调与应用内 Esc 快捷键。重复调用只生效一次。
  Future<void> initialize() async {
    if (_initialized || !supported) return;
    _initialized = true;
    channel.setMethodCallHandler((call) async {
      if (call.method == 'togglePip' ||
          call.method == 'togglePipVisibility') {
        await _hotkeyToggle();
      }
      return null;
    });
    // 原生热键注册可能被别的程序占用，应用内再兜一层。两条入口都走
    // _hotkeyToggle 统一节流，不能一条节流一条不节流。
    HardwareKeyboard.instance.addHandler(_onKey);
  }

  /// 热键的**唯一**入口。
  ///
  /// 窗口可见且聚焦时，同一次按键会同时产生原生 `WM_HOTKEY` 和 Flutter 的
  /// HardwareKeyboard 事件；若两条各自调一次 toggleVisibility，窗口会
  /// 「隐藏后立刻又被叫回来」，表现为快捷键完全失灵。这里对两条入口统一
  /// 做 300ms 去重，谁先到谁生效。
  Future<void> _hotkeyToggle() async {
    final now = DateTime.now();
    if (_lastHotkey != null &&
        now.difference(_lastHotkey!).inMilliseconds < 300) {
      return;
    }
    _lastHotkey = now;
    await toggleVisibility();
  }

  bool _onKey(KeyEvent event) {
    if (!supported ||
        (event is! KeyDownEvent && event is! KeyRepeatEvent)) {
      return false;
    }
    final keys = HardwareKeyboard.instance.logicalKeysPressed;
    if (keys.contains(LogicalKeyboardKey.escape)) {
      // Esc 只在小窗里有效；正常页面要留给原有的返回逻辑。
      if (!_active) return false;
      unawaited(exit());
      return true;
    }
    final control = keys.contains(LogicalKeyboardKey.controlLeft) ||
        keys.contains(LogicalKeyboardKey.controlRight);
    final shift = keys.contains(LogicalKeyboardKey.shiftLeft) ||
        keys.contains(LogicalKeyboardKey.shiftRight);
    if (control && shift && keys.contains(LogicalKeyboardKey.keyP)) {
      // 不要求 _active：原生 RegisterHotKey 被别的程序占用时，应用内这条
      // 兜底仍要能「首次按下进入小窗」。
      unawaited(_hotkeyToggle());
      return true;
    }
    return false;
  }

  /// 所有窗口操作都吞掉异常：窗口尚未就绪 / 正在销毁 / 远程桌面会话切换时，
  /// `window_manager` 会抛 `PlatformException`，一个漏出去的异步错误会盖红屏。
  Future<T?> _guard<T>(Future<T> Function() action) async {
    try {
      return await action();
    } catch (_) {
      return null;
    }
  }

  /// 进入 / 退出小窗。
  Future<void> toggle() async => _active ? exit() : enter();

  /// 全局热键的语义：没进小窗就进；已经进了就在「显示 / 隐藏」之间切。
  ///
  /// 隐藏时**不暂停播放** —— 用户按下快捷键通常是在别的窗口里干活，想继续
  /// 听声音；再按一次把小窗叫回来即可。
  Future<void> toggleVisibility() async {
    if (!supported) return;
    if (!_active) {
      await enter();
      return;
    }
    if (_hidden) {
      await _guard(() => windowManager.show());
      await _guard(() => windowManager.focus());
      _hidden = false;
    } else {
      await _guard(() => windowManager.hide());
      _hidden = true;
    }
    notifyListeners();
  }

  Future<void> enter() async {
    if (!supported || _active) return;
    final bounds = await _guard(() => windowManager.getBounds());
    final maximized = await _guard(() => windowManager.isMaximized()) ?? false;
    _savedBounds = bounds;
    _savedMaximized = maximized;
    if (maximized) {
      // 最大化状态下 setBounds 不生效，先还原再缩放。
      await _guard(() => windowManager.unmaximize());
    }
    final base = bounds ?? const Rect.fromLTWH(0, 0, 1360, 840);
    final size = _pipSize;
    // 放在原窗口的右下角内侧，不需要查询屏幕尺寸（多显示器 / DPI 缩放
    // 下 `getBounds` 给的逻辑坐标已经足够）。
    final position = _pipPosition ??
        Offset(
          base.right - size.width - 28,
          base.bottom - size.height - 28,
        );
    await _guard(() => windowManager.setMinimumSize(pipMinimumSize));
    await _guard(
      () => windowManager.setBounds(
        Rect.fromLTWH(position.dx, position.dy, size.width, size.height),
      ),
    );
    await _guard(() => windowManager.setAlwaysOnTop(_alwaysOnTop));
    _hidden = false;
    _active = true;
    notifyListeners();
  }

  Future<void> exit() async {
    if (!_active) return;
    // 记住用户这一轮拖出来的尺寸与位置，下次进小窗沿用。
    final current = await _guard(() => windowManager.getBounds());
    if (current != null) {
      _pipSize = Size(
        current.width.clamp(pipMinimumSize.width, 4000),
        current.height.clamp(pipMinimumSize.height, 4000),
      );
      _pipPosition = current.topLeft;
    }
    if (_hidden) {
      await _guard(() => windowManager.show());
      _hidden = false;
    }
    await _guard(() => windowManager.setMinimumSize(normalMinimumSize));
    await _guard(() => windowManager.setAlwaysOnTop(false));
    final saved = _savedBounds;
    if (saved != null) {
      await _guard(() => windowManager.setBounds(saved));
    }
    if (_savedMaximized) {
      await _guard(() => windowManager.maximize());
    }
    _active = false;
    _savedBounds = null;
    notifyListeners();
  }

  /// 小窗上的「取消 / 恢复置顶」。
  Future<void> setAlwaysOnTop(bool value) async {
    _alwaysOnTop = value;
    if (_active) {
      await _guard(() => windowManager.setAlwaysOnTop(value));
    }
    notifyListeners();
  }
}

/// 小窗浮层：只在画中画激活时出现。
///
/// 平时完全透明、只留顶部一条感应带；鼠标移上去才浮出控制条，不遮挡画面。
/// 整条都是拖动区（除右侧按钮），所以小窗能随便拖。
class WindowsPipOverlay extends StatefulWidget {
  const WindowsPipOverlay({super.key, required this.child});

  final Widget child;

  @override
  State<WindowsPipOverlay> createState() => _WindowsPipOverlayState();
}

class _WindowsPipOverlayState extends State<WindowsPipOverlay> {
  bool _hovered = false;

  Future<void> _drag() async {
    try {
      await windowManager.startDragging();
    } catch (_) {
      // 窗口正在销毁时拖不动，忽略即可。
    }
  }

  @override
  Widget build(BuildContext context) {
    final pip = WindowsPip.instance;
    return AnimatedBuilder(
      animation: pip,
      builder: (context, _) {
        // Stack 与 children 数量必须恒定。若按 active 在「直接 child」和
        // 「Stack 里的 child」之间来回切，widget.child 的 Element 会被
        // deactivate 再重新 inflate —— 里面挂的播放器会 dispose、重新缓冲，
        // 画中画就白做了。这里只用 Offstage 切浮层显示。
        return Stack(
          fit: StackFit.expand,
          children: [
            widget.child,
            Positioned(
              left: 0,
              right: 0,
              top: 0,
              child: Offstage(
                offstage: !pip.active,
                child: MouseRegion(
                  onEnter: (_) => setState(() => _hovered = true),
                  onExit: (_) => setState(() => _hovered = false),
                  child: AnimatedOpacity(
                    opacity: _hovered ? 1 : 0,
                    duration: const Duration(milliseconds: 140),
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        gradient: LinearGradient(
                          begin: Alignment.topCenter,
                          end: Alignment.bottomCenter,
                          colors: [
                            Colors.black.withValues(alpha: .78),
                            Colors.transparent,
                          ],
                        ),
                      ),
                      child: SizedBox(
                        height: 34,
                        child: GestureDetector(
                          behavior: HitTestBehavior.opaque,
                          onPanStart: (_) => unawaited(_drag()),
                          child: Padding(
                            padding: const EdgeInsets.only(left: 10, right: 4),
                            child: Row(
                              children: [
                                const Icon(
                                  Icons.drag_indicator_rounded,
                                  size: 15,
                                  color: Colors.white70,
                                ),
                                const SizedBox(width: 6),
                                const Expanded(
                                  child: Text(
                                    '画中画 · Ctrl+Shift+P 隐藏 / 显示 · Esc 退出',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(
                                      color: Colors.white70,
                                      fontSize: 10.5,
                                    ),
                                  ),
                                ),
                                _PipAction(
                                  icon: pip.alwaysOnTop
                                      ? Icons.push_pin_rounded
                                      : Icons.push_pin_outlined,
                                  tooltip:
                                      pip.alwaysOnTop ? '取消置顶' : '窗口置顶',
                                  onTap: () => unawaited(
                                    pip.setAlwaysOnTop(!pip.alwaysOnTop),
                                  ),
                                ),
                                _PipAction(
                                  icon: Icons.fullscreen_exit_rounded,
                                  tooltip: '退出画中画',
                                  onTap: () => unawaited(pip.exit()),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ],
        );
      },
    );
  }
}

class _PipAction extends StatelessWidget {
  const _PipAction({
    required this.icon,
    required this.tooltip,
    required this.onTap,
  });

  final IconData icon;
  final String tooltip;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Tooltip(
    message: tooltip,
    child: MouseRegion(
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 7),
          child: Icon(icon, size: 15, color: Colors.white),
        ),
      ),
    ),
  );
}
