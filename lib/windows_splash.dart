import 'package:flutter/material.dart';

import 'app_build.dart';

/// Windows 桌面版的满屏横版动画开屏（0.8.28）。
///
/// 手机 / 电视继续用 `app_splash.dart` 里那张成品位图。桌面之所以另做一版，
/// 是因为把 1920×1080 的成品图 `BoxFit.cover` 进 1360×840（接近 16:10）的
/// 窗口时，四周装饰会被裁掉、构图看着像"竖版图被压扁"；而且整页只有一条
/// 96×2 的细线在动，用户感知不到"开屏"。
///
/// 这一版是**按窗口比例铺满**的横版构图，不裁任何元素：
/// - 深墨底 + 品牌橙径向光晕（缓慢呼吸），和主界面深色主题直接衔接；
/// - 左侧品牌区：成品 logo 位图缩放淡入、应用名与标语错峰上浮；
/// - 右侧胶片带 / 光带装饰，配合一道循环横扫的流光；
/// - 底部明确的加载进度条（不再是一条几乎看不见的细线）。
///
/// 仍然**只用位图 logo**，没有去复刻 0.8.25 那套矢量 Path —— 矢量方案在
/// 渐变 / 遮罩上还原不准，0.8.27 已经回滚过一次。
class WindowsSplashSurface extends StatefulWidget {
  const WindowsSplashSurface({super.key});

  @override
  State<WindowsSplashSurface> createState() => _WindowsSplashSurfaceState();
}

class _WindowsSplashSurfaceState extends State<WindowsSplashSurface>
    with TickerProviderStateMixin {
  /// 进场：logo 与文字错峰淡入，1.1 秒走完。
  late final AnimationController _intro = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1100),
  )..forward();

  /// 循环：光晕呼吸 + 流光横扫，直到开屏淡出才停。
  late final AnimationController _loop = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 3000),
  )..repeat();

  static const _backdrop = Color(0xFF0D0E13);
  static const _brand = Color(0xFFFF765F);
  static const _brandDeep = Color(0xFFB8332A);

  @override
  void dispose() {
    _intro.dispose();
    _loop.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: const BoxDecoration(color: _backdrop),
      child: AnimatedBuilder(
        animation: Listenable.merge([_intro, _loop]),
        builder: (context, _) => Stack(
          fit: StackFit.expand,
          children: [
            // 品牌橙径向光晕：从左侧品牌区往外散，缓慢呼吸。
            DecoratedBox(
              decoration: BoxDecoration(
                gradient: RadialGradient(
                  center: const Alignment(-.52, -.18),
                  radius: 1.15,
                  colors: [
                    _brand.withValues(alpha: .13 + .07 * _pulse),
                    _brandDeep.withValues(alpha: .05 + .04 * _pulse),
                    Colors.transparent,
                  ],
                  stops: const [0, .42, 1],
                ),
              ),
            ),
            // 右侧胶片带：只做几何装饰，避免和品牌位图抢注意力。
            Positioned.fill(child: CustomPaint(painter: _FilmStripPainter(_loop.value))),
            // 一道横扫流光。
            Positioned.fill(
              child: IgnorePointer(
                child: FractionalTranslation(
                  translation: Offset(_loop.value * 2.4 - 1.2, 0),
                  child: Opacity(
                    opacity: .10,
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        gradient: LinearGradient(
                          colors: [
                            Colors.transparent,
                            Colors.white.withValues(alpha: .5),
                            Colors.transparent,
                          ],
                        ),
                      ),
                      child: const SizedBox(width: 180),
                    ),
                  ),
                ),
              ),
            ),
            // 主体：横版左对齐，窄窗口自动改成上下居中，永远不裁元素。
            LayoutBuilder(
              builder: (context, constraints) {
                final wide = constraints.maxWidth >= 720;
                final content = _BrandIntro(intro: _intro.value);
                return Padding(
                  padding: EdgeInsets.symmetric(
                    horizontal: wide ? 72 : 32,
                    vertical: 40,
                  ),
                  child: wide
                      ? Align(alignment: Alignment.centerLeft, child: content)
                      : Center(child: content),
                );
              },
            ),
            // 底部加载条 + 提示。
            Positioned(
              left: 0,
              right: 0,
              bottom: 38,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SizedBox(
                    width: 220,
                    height: 3,
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(2),
                      child: LinearProgressIndicator(
                        value: null,
                        backgroundColor: Colors.white.withValues(alpha: .10),
                        valueColor: AlwaysStoppedAnimation<Color>(
                          _brand.withValues(alpha: .9),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(height: 14),
                  Opacity(
                    opacity: .42 * _intro.value,
                    child: const Text(
                      '正在准备剧库…',
                      style: TextStyle(color: Colors.white, fontSize: 11.5),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  double get _pulse => (.5 + .5 * (1 - (2 * _loop.value - 1).abs()));
}

/// 品牌区：logo 缩放淡入 → 应用名上浮 → 标语与副标题上浮。
class _BrandIntro extends StatelessWidget {
  const _BrandIntro({required this.intro});

  final double intro;

  @override
  Widget build(BuildContext context) {
    final logo = Curves.easeOutCubic.transform(
      ((intro - .02) / .58).clamp(0.0, 1.0),
    );
    final title = Curves.easeOutCubic.transform(
      ((intro - .22) / .58).clamp(0.0, 1.0),
    );
    final caption = Curves.easeOutCubic.transform(
      ((intro - .40) / .60).clamp(0.0, 1.0),
    );
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Opacity(
          opacity: logo,
          child: Transform.scale(
            scale: .86 + .14 * logo,
            alignment: Alignment.centerLeft,
            child: Container(
              width: 92,
              height: 92,
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(22),
                boxShadow: [
                  BoxShadow(
                    color: const Color(0xFFFF664F).withValues(alpha: .30),
                    blurRadius: 40,
                    spreadRadius: 2,
                  ),
                ],
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(22),
                child: Image.asset(
                  'assets/logo.jpg',
                  fit: BoxFit.cover,
                  cacheWidth: 184,
                  errorBuilder: (context, error, stack) => const DecoratedBox(
                    decoration: BoxDecoration(color: Color(0xFFB8332A)),
                  ),
                ),
              ),
            ),
          ),
        ),
        const SizedBox(height: 30),
        Opacity(
          opacity: title,
          child: Transform.translate(
            offset: Offset(0, 16 * (1 - title)),
            child: const Text(
              appName,
              style: TextStyle(
                color: Colors.white,
                fontSize: 44,
                height: 1.05,
                fontWeight: FontWeight.w800,
                letterSpacing: 1.5,
              ),
            ),
          ),
        ),
        const SizedBox(height: 14),
        Opacity(
          opacity: caption,
          child: Transform.translate(
            offset: Offset(0, 12 * (1 - caption)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 28,
                  height: 2,
                  color: const Color(0xFFFF765F),
                ),
                const SizedBox(width: 12),
                const Text(
                  '好片随心看',
                  style: TextStyle(
                    color: Color(0xFFD8D3D8),
                    fontSize: 15,
                    letterSpacing: 5,
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

/// 右侧缓慢下移的胶片穿孔装饰（纯几何，不是品牌图复刻）。
class _FilmStripPainter extends CustomPainter {
  const _FilmStripPainter(this.progress);

  final double progress;

  @override
  void paint(Canvas canvas, Size size) {
    final bandWidth = size.width * .16;
    final left = size.width - bandWidth - size.width * .04;
    final paint = Paint()
      ..color = const Color(0xFFFF765F).withValues(alpha: .10)
      ..style = PaintingStyle.fill;
    final strip = Rect.fromLTWH(left, 0, bandWidth, size.height);
    canvas.drawRect(strip, paint);
    final hole = Paint()
      ..color = const Color(0xFF0D0E13).withValues(alpha: .85)
      ..style = PaintingStyle.fill;
    const spacing = 58.0;
    final holeSize = bandWidth * .30;
    final offset = (progress * spacing) % spacing;
    for (double y = -spacing + offset; y < size.height + spacing; y += spacing) {
      final rect = RRect.fromRectAndRadius(
        Rect.fromLTWH(
          left + (bandWidth - holeSize) / 2,
          y,
          holeSize,
          holeSize * .72,
        ),
        const Radius.circular(5),
      );
      canvas.drawRRect(rect, hole);
    }
  }

  @override
  bool shouldRepaint(covariant _FilmStripPainter old) =>
      old.progress != progress;
}
