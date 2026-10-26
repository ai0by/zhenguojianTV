import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_layout.dart';

/// 开屏页（启动画面）。
///
/// 解决的问题：进入软件后，首页要等原生核心初始化 + 拉取第一批视频
/// 才显示内容，中间这段时间是空白等待。
///
/// 做法：先用开屏页盖住，等首页真正"有视频可播"再淡出，
/// 用户感知上就是"一点开就有画面"，而不是"进去后干等"。
///
/// 设计约束：
/// - 不阻塞启动：只做视觉遮挡，初始化逻辑仍在后台并行进行。
/// - **最多停留 [timeout]**：首次拉数据可能较慢，不能让用户干等；
///   超时后**无论如何都进入首页**，首页自己显示加载占位或离线提示。
/// - 淡出动画不改变布局，不影响底部导航与顶部标题栏。
///
/// 0.8.10 修复（用户反馈"选了香蕉这种不可用站源，退出再进就一直卡在加载页"）：
/// 原来 [timeout] 只把"最短展示时间"置位，仍然要求 `ready` 为真才淡出。
/// 而 `ready` 只在首页**真的拉到第一批视频**时才置位 —— 站源不可用、
/// 或者断网启动时它永远是 false，于是开屏页永久盖在上面，用户只能杀进程。
/// 现在超时是**硬超时**：时间一到直接放行，剩下的交给首页的
/// 错误态 / 离线态（首页本身有"重试"按钮，所以离线也能进）。
class AppSplash extends StatefulWidget {
  const AppSplash({
    super.key,
    required this.child,
    required this.ready,
    this.minimumDuration = const Duration(milliseconds: 420),
    this.timeout = const Duration(milliseconds: 2200),
  });

  /// 真正的主界面。
  final Widget child;

  /// 主界面是否已经准备好（例如首页已拉到第一批视频）。
  final ValueListenable<bool> ready;

  /// 开屏页最短展示时间，避免"闪一下就没了"。
  final Duration minimumDuration;

  /// 最长等待时间，超过则**强制**放行（不再等 [ready]）。
  final Duration timeout;

  @override
  State<AppSplash> createState() => _AppSplashState();
}

class _AppSplashState extends State<AppSplash> {
  bool _dismissed = false;
  bool _minimumElapsed = false;
  bool _timedOut = false;
  Timer? _minimumTimer;
  Timer? _timeoutTimer;

  /// 淡出期间覆盖层仍在树上（`_dismissed` 已为真），
  /// 等透明度走完 0 才真正摘掉，避免浅色开屏硬切到深色主界面。
  bool _visible = true;

  static const _fadeDuration = Duration(milliseconds: 240);

  @override
  void initState() {
    super.initState();
    _minimumTimer = Timer(widget.minimumDuration, () {
      if (!mounted) return;
      _minimumElapsed = true;
      _maybeDismiss();
    });
    _timeoutTimer = Timer(widget.timeout, () {
      if (!mounted) return;
      // 硬超时：置位后 `_maybeDismiss` 不再看 `ready`。
      _timedOut = true;
      _minimumElapsed = true;
      _maybeDismiss();
    });
    widget.ready.addListener(_maybeDismiss);
    _maybeDismiss();
  }

  @override
  void didUpdateWidget(covariant AppSplash oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.ready != widget.ready) {
      oldWidget.ready.removeListener(_maybeDismiss);
      widget.ready.addListener(_maybeDismiss);
    }
  }

  @override
  void dispose() {
    _minimumTimer?.cancel();
    _timeoutTimer?.cancel();
    widget.ready.removeListener(_maybeDismiss);
    super.dispose();
  }

  void _maybeDismiss() {
    if (!mounted || _dismissed) return;
    if (!_minimumElapsed) return;
    if (!_timedOut && !widget.ready.value) return;
    setState(() => _dismissed = true);
  }

  @override
  Widget build(BuildContext context) {
    return Stack(
      fit: StackFit.expand,
      children: [
        widget.child,
        // 用 `IgnorePointer` + 淡出：淡出期间不吞掉首页的点击，
        // 否则刚进首页的前 300ms 遥控器/手指操作会被开屏页吃掉。
        if (_visible)
          IgnorePointer(
            child: AnimatedOpacity(
              opacity: _dismissed ? 0 : 1,
              duration: _fadeDuration,
              curve: Curves.easeOut,
              onEnd: () {
                if (!_dismissed || !mounted) return;
                setState(() => _visible = false);
              },
              child: _SplashSurface(
                television: AppLayout.isTelevision(context),
              ),
            ),
          ),
      ],
    );
  }
}

/// 把开屏图提前解进图片缓存。
///
/// 这是"开屏不闪"的关键一步：`Image.asset` 的解码是异步的，不预加载的话
/// Flutter 首帧画出来时图片还没就绪，用户看到的是"一层纯色 → 突然出现整张图"
/// 的硬切，也就是反馈里的"闪了一下"。
///
/// 在 `runApp` 之前调用，期间原生启动窗口显示的是同色底（`splash_background`），
/// 所以多等的这一百来毫秒观感上就是"开屏图慢慢显出来"，不会跳。
Future<void> precacheSplashImage(bool television) async {
  final provider = AssetImage(
    television ? 'assets/splash/splash_tv.jpg' : 'assets/splash/splash_phone.jpg',
  );
  final completer = Completer<void>();
  void finish() {
    if (!completer.isCompleted) completer.complete();
  }

  final stream = provider.resolve(ImageConfiguration.empty);
  stream.addListener(
    ImageStreamListener(
      (image, synchronous) => finish(),
      onError: (error, stack) => finish(),
    ),
  );
  // 资源缺失 / 解码失败时不能卡住启动，最多等 2 秒就放行。
  await completer.future.timeout(const Duration(seconds: 2), onTimeout: () {});
}

/// 开屏页视觉。
///
/// 0.8.11：只留美术给的成品开屏图（手机 1080×1920 / 电视 1920×1080），
/// 外加一条极细的加载线 —— 用户要求"只有图和加载信息"。
///
/// 图上不再有任何品牌块 / 文字 / 花哨动效，避免和图片本身打架。
class _SplashSurface extends StatelessWidget {
  const _SplashSurface({required this.television});

  final bool television;

  /// 与开屏图顶部主色一致（手机图取样 #FEF5F5，电视图 #FDF7F7）。
  ///
  /// 预加载之后这一层基本看不到，但资源缺失 / 解码异常时它是最后的兜底，
  /// 不能让下层深色首页露出来。
  static const _backdrop = Color(0xFFFEF5F5);

  /// 开屏是浅色画面，状态栏/导航栏图标要翻成深色，
  /// 否则深色主题下白色图标压在白底上完全看不见。
  static const _overlay = SystemUiOverlayStyle(
    statusBarColor: Colors.transparent,
    statusBarIconBrightness: Brightness.dark,
    statusBarBrightness: Brightness.light,
    systemNavigationBarColor: Colors.transparent,
    systemNavigationBarIconBrightness: Brightness.dark,
  );

  @override
  Widget build(BuildContext context) {
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: _overlay,
      child: ColoredBox(
        color: _backdrop,
        child: Stack(
          fit: StackFit.expand,
          children: [
            Image.asset(
              television
                  ? 'assets/splash/splash_tv.jpg'
                  : 'assets/splash/splash_phone.jpg',
              fit: BoxFit.cover,
              filterQuality: FilterQuality.medium,
              // 测试环境没有真实资源包，这里兜底成纯色底，不让异常冒到上层。
              errorBuilder: (context, error, stack) =>
                  const ColoredBox(color: _backdrop),
            ),
            Align(
              // 竖版图从高度 0.5 往下是红色胶片带，白线放 0.90 的深红区才看得见；
              // 横版底部是浅色，品牌橙放在 0.84。
              alignment: Alignment(0, television ? .84 : .90),
              child: SizedBox(
                width: 96,
                height: 2,
                child: _SplashBar(
                  color: television ? const Color(0xFFFF765F) : Colors.white,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 加载指示：一条细线在轨道里缓慢滑动。
///
/// 比 `CircularProgressIndicator` 安静得多，也不会和开屏图抢注意力。
/// 刻意做得又细又短（96×2），远看只是"有一点东西在动"，不喧宾夺主。
class _SplashBar extends StatefulWidget {
  const _SplashBar({required this.color});

  final Color color;

  @override
  State<_SplashBar> createState() => _SplashBarState();
}

class _SplashBarState extends State<_SplashBar>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1100),
  )..repeat();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ClipRRect(
    borderRadius: BorderRadius.circular(1),
    child: ColoredBox(
      // 轨道用同色的低透明度：浅底上白 16% 几乎不可见，正好只留滑动的那一段。
      color: widget.color.withValues(alpha: .16),
      child: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          final progress = _controller.value;
          // 线段在 0..1 之间来回滑动，宽度固定 38%。
          final start = (progress * 1.38 - .38).clamp(0.0, 1.0);
          return Align(
            alignment: Alignment(-1 + start * 2, 0),
            child: FractionallySizedBox(
              widthFactor: .38,
              child: Container(
                height: 2,
                decoration: BoxDecoration(
                  color: widget.color,
                  borderRadius: BorderRadius.circular(1),
                ),
              ),
            ),
          );
        },
      ),
    ),
  );
}
