import 'package:flutter/material.dart';

import 'core_bridge.dart';
import 'models.dart';
import 'widgets.dart' show DramaCover, isDesktopPlatform;

/// 桌面「发现」页顶部的 Jellyfin 式主推横幅（0.8.28）。
///
/// 手机 / 电视完全不渲染（调用方还有一层 [isDesktopPlatform] 守卫），因此
/// `DramaCover` 的数量断言在 widget 测试里不受影响 —— 测试环境默认 Android。
///
/// 视觉上参照媒体库类应用：一张横向大卡，左侧信息层级（标签 → 标题 →
/// 元数据 → 简介 → 主操作），右侧 2:3 海报，底部一层深色渐变把文字托起来；
/// 悬停时海报轻微放大，给鼠标一点反馈。
class WindowsHeroBanner extends StatefulWidget {
  const WindowsHeroBanner({
    super.key,
    required this.drama,
    required this.repository,
    required this.onPlay,
  });

  final Drama drama;
  final AppRepository repository;
  final VoidCallback onPlay;

  @override
  State<WindowsHeroBanner> createState() => _WindowsHeroBannerState();
}

class _WindowsHeroBannerState extends State<WindowsHeroBanner> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final drama = widget.drama;
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 180),
        curve: Curves.easeOut,
        height: 232,
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(20),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withValues(alpha: .28),
              blurRadius: _hovered ? 28 : 18,
              offset: const Offset(0, 8),
            ),
          ],
        ),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(20),
          child: Stack(
            fit: StackFit.expand,
            children: [
              // 影院底色：品牌橙从左下往右上散开，再压一层深色保证白字可读。
              DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.bottomLeft,
                    end: Alignment.topRight,
                    colors: [
                      const Color(0xFF4A1B18),
                      const Color(0xFF221419),
                      colors.surfaceContainerHighest,
                    ],
                    stops: const [0, .55, 1],
                  ),
                ),
              ),
              Positioned(
                right: -60,
                top: -80,
                child: IgnorePointer(
                  child: Container(
                    width: 320,
                    height: 320,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      gradient: RadialGradient(
                        colors: [
                          const Color(0xFFFF765F).withValues(alpha: .28),
                          Colors.transparent,
                        ],
                      ),
                    ),
                  ),
                ),
              ),
              // 左侧信息区。
              Padding(
                padding: const EdgeInsets.fromLTRB(28, 24, 208, 22),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 9,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: colors.primary.withValues(alpha: .20),
                        borderRadius: BorderRadius.circular(6),
                        border: Border.all(
                          color: colors.primary.withValues(alpha: .45),
                        ),
                      ),
                      child: Text(
                        '今日推荐',
                        style: TextStyle(
                          color: colors.primary,
                          fontSize: 11,
                          fontWeight: FontWeight.w700,
                          letterSpacing: 1,
                        ),
                      ),
                    ),
                    const SizedBox(height: 12),
                    Text(
                      drama.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 27,
                        height: 1.15,
                        fontWeight: FontWeight.w800,
                      ),
                    ),
                    const SizedBox(height: 7),
                    Text(
                      [
                        if (drama.episodes > 0) '共 ${drama.episodes} 集',
                        if (drama.category.isNotEmpty) drama.category,
                        if (drama.heat.isNotEmpty) drama.heat,
                      ].join('  ·  '),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: Colors.white.withValues(alpha: .66),
                        fontSize: 12.5,
                      ),
                    ),
                    if (drama.description.isNotEmpty) ...[
                      const SizedBox(height: 9),
                      Text(
                        drama.description.replaceAll(RegExp(r'\s+'), ' ').trim(),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: Colors.white.withValues(alpha: .52),
                          fontSize: 12.5,
                          height: 1.45,
                        ),
                      ),
                    ],
                    const Spacer(),
                    FilledButton.icon(
                      onPressed: widget.onPlay,
                      icon: const Icon(Icons.play_arrow_rounded, size: 19),
                      label: const Text('立即观看'),
                      style: FilledButton.styleFrom(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 18,
                          vertical: 11,
                        ),
                        textStyle: const TextStyle(
                          fontSize: 13,
                          fontWeight: FontWeight.w700,
                        ),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(10),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              // 右侧海报：2:3，悬停时轻微放大。
              Positioned(
                right: 34,
                top: 18,
                bottom: 18,
                child: AnimatedScale(
                  scale: _hovered ? 1.04 : 1,
                  duration: const Duration(milliseconds: 180),
                  child: AspectRatio(
                    aspectRatio: 2 / 3,
                    child: DramaCover(
                      drama: drama,
                      repository: widget.repository,
                      radius: 12,
                    ),
                  ),
                ),
              ),
              // 底部再压一层渐变，保证按钮与文字在任何海报下都清晰。
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                height: 90,
                child: IgnorePointer(
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topCenter,
                        end: Alignment.bottomCenter,
                        colors: [
                          Colors.transparent,
                          Colors.black.withValues(alpha: .32),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
