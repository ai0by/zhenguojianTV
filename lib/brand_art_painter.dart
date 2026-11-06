import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'brand_art.dart';

/// 品牌矢量图的渲染器。
///
/// 图形数据由 `brand_art.dart` 生成（SVG 路径直译成 Path 指令），这里只负责
/// 把「SVG 坐标系」映射到实际尺寸并按 [BrandShape] 描述绘制。
///
/// 为什么不引 flutter_svg：见 brand_art.dart 顶部说明 —— 本机没有 Flutter
/// SDK，无法验证新依赖在 CI 上能否解析，而构建只有 CI 一条路可走。
class BrandPainter extends CustomPainter {
  const BrandPainter({
    required this.shapes,
    required this.designSize,
    this.bounds,
    this.fit = BoxFit.contain,
    this.alignment = Alignment.center,
  });

  /// 图形列表（坐标仍在 SVG 坐标系里）。
  final List<BrandShape> shapes;

  /// SVG 画布尺寸（viewBox）。
  final Size designSize;

  /// 内容实际包围盒。给了就按它缩放，避免图形在画布里偏到一角。
  final Rect? bounds;

  final BoxFit fit;
  final Alignment alignment;

  @override
  void paint(Canvas canvas, Size size) {
    if (size.isEmpty || size.width.isInfinite || size.height.isInfinite) {
      return;
    }
    if (shapes.isEmpty) return;

    final source = bounds ?? (Offset.zero & designSize);
    if (source.width <= 0 || source.height <= 0) return;

    // 开屏要铺满（cover），图标要整幅可见（contain）。
    final scale = fit == BoxFit.cover
        ? math.max(size.width / source.width, size.height / source.height)
        : math.min(size.width / source.width, size.height / source.height);

    // 按 alignment 把 source 居中/对齐到 target。
    final dx = (size.width - source.width * scale) * (alignment.x + 1) / 2 -
        source.left * scale;
    final dy = (size.height - source.height * scale) * (alignment.y + 1) / 2 -
        source.top * scale;

    canvas.save();
    canvas.translate(dx, dy);
    canvas.scale(scale);
    _paintShapes(canvas, source.inflate(source.longestSide * 0.05));
    canvas.restore();
  }

  void _paintShapes(Canvas canvas, Rect layerBounds) {
    final cutouts = shapes.where((s) => s.isCutout).toList(growable: false);
    if (cutouts.isEmpty) {
      for (final shape in shapes) {
        _drawOne(canvas, shape);
      }
      return;
    }
    // 有镂空（SVG mask）时先离屏合成，再用 dstOut 把洞挖出来。
    canvas.saveLayer(layerBounds, Paint());
    for (final shape in shapes) {
      if (!shape.isCutout) _drawOne(canvas, shape);
    }
    final punch = Paint()
      ..blendMode = BlendMode.dstOut
      ..isAntiAlias = true;
    for (final shape in cutouts) {
      canvas.drawPath(shape.path, punch);
    }
    canvas.restore();
  }

  void _drawOne(Canvas canvas, BrandShape shape) {
    final box = shape.path.getBounds();
    final opacity = shape.opacity.clamp(0.0, 1.0);

    // 描边形状（SVG 里的高光细线）单独走一条路：只有 stroke，没有 fill。
    if (shape.strokeColor != null && shape.strokeWidth > 0) {
      final paint = Paint()
        ..isAntiAlias = true
        ..style = PaintingStyle.stroke
        ..strokeWidth = shape.strokeWidth
        ..color = shape.strokeColor!.withValues(
          alpha: shape.strokeColor!.a * opacity,
        );
      canvas.drawPath(shape.path, paint);
      return;
    }

    final gradient = shape.gradient;
    final paint = Paint()..isAntiAlias = true;
    if (gradient == null) {
      paint.color = (shape.color ?? const Color(0xFF000000))
          .withValues(alpha: (shape.color ?? const Color(0xFF000000)).a * opacity);
      canvas.drawPath(shape.path, paint);
      return;
    }

    paint.shader = _shader(gradient, box);
    if (opacity >= 1) {
      canvas.drawPath(shape.path, paint);
      return;
    }
    // 渐变色的透明度已经烘进 stop 颜色里，只能在图层上再乘一次整体不透明度。
    canvas.saveLayer(
      box,
      Paint()..color = Colors.white.withValues(alpha: opacity),
    );
    canvas.drawPath(shape.path, paint);
    canvas.restore();
  }

  Shader _shader(BrandGradient grad, Rect box) {
    // SVG 的 objectBoundingBox 坐标 → 路径包围盒内的绝对坐标。
    if (grad.isRadial) {
      final center = Offset(
        box.left + box.width * grad.cx,
        box.top + box.height * grad.cy,
      );
      final radius = math.max(box.width, box.height) * grad.radius;
      return RadialGradient(colors: grad.colors, stops: grad.stops)
          .createShader(Rect.fromCircle(center: center, radius: radius));
    }
    return LinearGradient(colors: grad.colors, stops: grad.stops)
        .createShader(
      Rect.fromLTRB(
        box.left + box.width * grad.x1,
        box.top + box.height * grad.y1,
        box.left + box.width * grad.x2,
        box.top + box.height * grad.y2,
      ),
    );
  }

  @override
  bool shouldRepaint(covariant BrandPainter old) =>
      old.shapes != shapes ||
      old.designSize != designSize ||
      old.fit != fit ||
      old.bounds != bounds;
}

/// 品牌矢量图组件。
///
/// - [BrandArt.mark] 只画 logo 主体（关于对话框、列表占位等用它）
/// - [BrandArt.splash] 整幅开屏插画（铺满）
/// - [BrandArt.icon] 带白底与文字的完整图标
class BrandArt extends StatelessWidget {
  const BrandArt.mark({super.key, this.size = 64})
      : _kind = _BrandKind.mark;

  const BrandArt.splash({super.key, required bool television})
      : _kind = television ? _BrandKind.splashTv : _BrandKind.splashPhone,
        size = null;

  const BrandArt.icon({super.key, this.size = 96}) : _kind = _BrandKind.icon;

  final _BrandKind _kind;

  /// 固定边长；为 null 表示铺满父级（开屏用）。
  final double? size;

  @override
  Widget build(BuildContext context) {
    final (design, bounds, fit) = switch (_kind) {
      _BrandKind.mark => (brandMarkSize, brandMarkBounds, BoxFit.contain),
      _BrandKind.splashPhone => (
          brandSplashPhoneSize,
          brandSplashPhoneBounds,
          BoxFit.cover,
        ),
      _BrandKind.splashTv => (
          brandSplashTvSize,
          brandSplashTvBounds,
          BoxFit.cover,
        ),
      _BrandKind.icon => (brandIconSize, brandIconBounds, BoxFit.cover),
    };

    final painter = BrandPainter(
      shapes: _shapesFor(_kind),
      designSize: design,
      bounds: bounds,
      fit: fit,
    );

    if (size == null) {
      return CustomPaint(painter: painter, size: Size.infinite);
    }
    return CustomPaint(
      size: Size.square(size!),
      painter: painter,
    );
  }
}

enum _BrandKind { mark, splashPhone, splashTv, icon }

/// 生成出来的 Path 只读复用：每次 build 重新构造几百段贝塞尔没有必要，
/// 开屏又恰好是最在意首帧的地方。
final Map<_BrandKind, List<BrandShape>> _shapeCache =
    <_BrandKind, List<BrandShape>>{};

List<BrandShape> _shapesFor(_BrandKind kind) => _shapeCache.putIfAbsent(
      kind,
      () => switch (kind) {
        _BrandKind.mark => brandMarkShapes(),
        _BrandKind.splashPhone => brandSplashPhoneShapes(),
        _BrandKind.splashTv => brandSplashTvShapes(),
        _BrandKind.icon => brandIconShapes(),
      },
    );

/// 启动前预热矢量路径（替代原来的位图 `precacheImage`）。
///
/// 位图开屏需要把图片解码进缓存才不会"闪一下出现"；矢量图没有解码这一步，
/// 但把几百段 Path 指令拼成 Path 对象也要几毫秒 —— 放在 `runApp` 之前做，
/// 首帧就只剩一次 `drawPath`，用户看不到任何跳变。
void warmUpBrandArt({bool television = false}) {
  _shapesFor(_BrandKind.mark);
  _shapesFor(television ? _BrandKind.splashTv : _BrandKind.splashPhone);
}
