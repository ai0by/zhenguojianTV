import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'app_build.dart';

/// 电视版的满屏开屏（0.8.34）。
///
/// 为什么不再用 assets/splash/splash_tv.jpg 那张成品位图：
/// - 那张图是浅色底（#FDF7F7），而 TV 首页是深色主题（#1C1D22 卡片 / 深底），
///   开屏淡出时从白跳到黑，观感很跳；
/// - 1920×1080 的位图用 BoxFit.cover 铺到各种电视比例（含过扫描）上，
///   logo 显得小、边缘被裁，看起来像"一张小图被强行拉大"；
/// - 只有一条 96×2 的加载线，电视远距离观看几乎看不见。
///
/// 这一版和 Windows 开屏同源（深色底 + 品牌橙光晕 + 位图 logo），但按
/// **电视大屏、远距离、居中构图**重新排布：logo / 应用名都放大，底部给
/// 一条明确的加载条，深色底直接衔接 TV 首页，不再有深浅跳变。
///
/// 仍然只用位图 logo（assets/logo.jpg），不复刻 0.8.25 那套矢量 Path —— 矢量
/// 在渐变/遮罩上还原不准，0.8.27 已经回滚过一次。
class TvSplashSurface extends StatefulWidget {
  const TvSplashSurface({super.key});

  @override
  State<TvSplashSurface> createState() => _TvSplashSurfaceState();
}

// 与 TV 首页主背景/卡片同色系，保证开屏→首页无缝衔接。
const _backdrop = Color(0xFF0D0E13);
const _brand = Color(0xFFFF765F);
const _brandDeep = Color(0xFFB8332A);

class _TvSplashSurfaceState extends State<TvSplashSurface>
    with TickerProviderStateMixin {
  /// 进场：logo 与文字错峰淡入上浮。
  late final AnimationController _intro = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1000),
  )..forward();

  /// 循环：光晕呼吸 + 加载条流光，开屏淡出才停。
  late final AnimationController _loop = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 3200),
  )..repeat();

  @override
  void dispose() {
    _intro.dispose();
    _loop.dispose();
    super.dispose();
  }

  double get _pulse =>
      0.5 + 0.5 * math.sin(_loop.value * 2 * math.pi);

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: const BoxDecoration(color: _backdrop),
      child: AnimatedBuilder(
        animation: Listenable.merge([_intro, _loop]),
        builder: (context, _) => Stack(
          fit: StackFit.expand,
          children: [
            // 品牌橙径向光晕：从中心往外散，缓慢呼吸。
            DecoratedBox(
              decoration: BoxDecoration(
                gradient: RadialGradient(
                  center: const Alignment(0, -.12),
                  radius: 0.95,
                  colors: [
                    _brand.withValues(alpha: .15 + .08 * _pulse),
                    _brandDeep.withValues(alpha: .06 + .04 * _pulse),
                    Colors.transparent,
                  ],
                  stops: const [0, .45, 1],
                ),
              ),
            ),
            // 上下暗角，让居中品牌区更聚焦。
            const DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [
                    Color(0x33000000),
                    Colors.transparent,
                    Color(0x55000000),
                  ],
                  stops: [0, .4, 1],
                ),
              ),
            ),
            // 居中品牌区。
            Center(child: _TvBrandIntro(intro: _intro.value, pulse: _pulse)),
            // 底部加载条 + 提示（电视远距离也看得清）。
            Positioned(
              left: 0,
              right: 0,
              bottom: 64,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SizedBox(
                    width: 340,
                    height: 4,
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(2),
                      child: LayoutBuilder(
                        builder: (context, constraints) {
                          final total = constraints.maxWidth;
                          final seg = total * .32;
                          final head = (_loop.value * 1.35 - .35).clamp(0.0, 1.0);
                          final x = (head * (total + seg) - seg).clamp(0.0, total);
                          return Stack(
                            children: [
                              ColoredBox(
                                color: Colors.white.withValues(alpha: .12),
                              ),
                              Positioned(
                                left: x,
                                width: seg,
                                top: 0,
                                bottom: 0,
                                child: const ColoredBox(color: _brand),
                              ),
                            ],
                          );
                        },
                      ),
                    ),
                  ),
                  const SizedBox(height: 18),
                  Text(
                    '正在准备片库…',
                    style: TextStyle(
                      color: Colors.white.withValues(alpha: .55 + .15 * _pulse),
                      fontSize: 16,
                      letterSpacing: 3,
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
}

class _TvBrandIntro extends StatelessWidget {
  const _TvBrandIntro({required this.intro, required this.pulse});

  final double intro;
  final double pulse;

  static double _ease(double t) =>
      Curves.easeOutCubic.transform(t.clamp(0.0, 1.0));

  @override
  Widget build(BuildContext context) {
    final logo = _ease((intro - .02) / .5);
    final title = _ease((intro - .18) / .5);
    final caption = _ease((intro - .34) / .5);
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Opacity(
          opacity: logo,
          child: Transform.scale(
            scale: .88 + .12 * logo,
            child: Container(
              width: 200,
              height: 200,
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(46),
                boxShadow: [
                  BoxShadow(
                    color: const Color(0xFFFF664F).withValues(alpha: .28 + .12 * pulse),
                    blurRadius: 60 + 20 * pulse,
                    spreadRadius: 4,
                  ),
                ],
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(46),
                child: Image.asset(
                  'assets/logo.jpg',
                  fit: BoxFit.cover,
                  cacheWidth: 400,
                  errorBuilder: (context, error, stack) => const DecoratedBox(
                    decoration: BoxDecoration(color: _brandDeep),
                  ),
                ),
              ),
            ),
          ),
        ),
        const SizedBox(height: 40),
        Opacity(
          opacity: title,
          child: Transform.translate(
            offset: Offset(0, 20 * (1 - title)),
            child: const Text(
              appName,
              style: TextStyle(
                color: Colors.white,
                fontSize: 64,
                height: 1.05,
                fontWeight: FontWeight.w800,
                letterSpacing: 2,
              ),
            ),
          ),
        ),
        const SizedBox(height: 22),
        Opacity(
          opacity: caption,
          child: Transform.translate(
            offset: Offset(0, 14 * (1 - caption)),
            child: const Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox(width: 40, height: 2, child: ColoredBox(color: _brand)),
                SizedBox(width: 18),
                Text(
                  '好片随心看',
                  style: TextStyle(
                    color: Color(0xFFD8D3D8),
                    fontSize: 20,
                    letterSpacing: 8,
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
