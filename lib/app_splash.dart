import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_layout.dart';
import 'brand_art_painter.dart';

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

/// 把开屏素材提前就绪。
///
/// 0.8.25：开屏从位图换成矢量图（用户提供的品牌 SVG 直译成 Path 指令）。
/// 位图要靠 `precacheImage` 解码才不会"闪一下出现"；矢量图没有解码这一步，
/// 但把几百段贝塞尔拼成 Path 也要几毫秒，所以同样在 `runApp` 之前预热。
///
/// 保留这个函数名与签名，`main.dart` 不用改。
Future<void> precacheSplashImage(bool television) async {
  // 同步完成，包一层 Future 是为了不破坏原有调用点的 await 写法。
  warmUpBrandArt(television: television);
}

/// 开屏页视觉。
///
/// 0.8.25：位图换成矢量（用户提供的品牌 SVG），并加入克制的一段入场动画：
/// 主体从 0.94 倍轻微放大到 1.0、同时淡入，底部品牌线随后稳定滑动。
///
/// 动画刻意做得"几乎察觉不到"：开屏总共只停留 0.4~2.2 秒，任何明显动效
/// 都会在还没看清时就被切断，反而显得卡。这里只做「从略小一点到位」的
/// 收束感，替代原来"图片突然出现"的硬切。
class _SplashSurface extends StatefulWidget {
  const _SplashSurface({required this.television});

  final bool television;

  @override
  State<_SplashSurface> createState() => _SplashSurfaceState();
}

class _SplashSurfaceState extends State<_SplashSurface>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 620),
  )..forward();

  late final Animation<double> _fade = CurvedAnimation(
    parent: _controller,
    curve: const Interval(0, .62, curve: Curves.easeOut),
  );

  late final Animation<double> _scale = Tween<double>(begin: .94, end: 1)
      .animate(CurvedAnimation(
    parent: _controller,
    curve: Curves.easeOutCubic,
  ));

  /// 与开屏图顶部主色一致（手机图取样 #FEF5F5，电视图 #FDF7F7）。
  ///
  /// 矢量图没有解码等待，这一层基本看不到，但它是 PNG 转矢量后
  /// 唯一的兜底底色，不能让下层深色首页在首帧露出来。
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
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final television = widget.television;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: _overlay,
      child: ColoredBox(
        color: _backdrop,
        child: Stack(
          fit: StackFit.expand,
          children: [
            // 矢量开屏图：铺满整屏，无解码等待。
            FadeTransition(
              opacity: _fade,
              child: ScaleTransition(
                scale: _scale,
                child: BrandArt.splash(television: television),
              ),
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
