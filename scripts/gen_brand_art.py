# -*- coding: utf-8 -*-
"""把品牌 SVG 转成 Flutter CustomPainter 可用的 Dart Path 代码。

只支持本项目素材用到的子集（已核实）：M L C Q Z H V、translate/scale 变换、
linearGradient / radialGradient、rect / circle / path、mask 镂空。

为什么不引入 flutter_svg：本机没有 Flutter SDK，无法验证新依赖在 CI
（Flutter 3.47.4）上能否解析成功；一旦解析失败整个构建就挂。这里直接把
SVG 路径编译成 Path 指令，零依赖、零解析风险，而且是真正的矢量。
"""

import re
import sys

NUM = re.compile(r"[-+]?(?:\d*\.\d+|\d+\.?)(?:[eE][-+]?\d+)?")
PATH_CMD = "MmLlCcQqZzHhVvAa"


# ---------------------------------------------------------------- path 解析
def parse_path(d):
    tokens = []
    i, n = 0, len(d)
    while i < n:
        ch = d[i]
        if ch in PATH_CMD:
            tokens.append(ch)
            i += 1
        elif ch in " \t\r\n,":
            i += 1
        else:
            m = NUM.match(d, i)
            if not m:
                i += 1
                continue
            tokens.append(float(m.group(0)))
            i = m.end()

    out, cur, start = [], (0.0, 0.0), (0.0, 0.0)
    idx, cmd = 0, None
    while idx < len(tokens):
        t = tokens[idx]
        if isinstance(t, str):
            cmd = t
            idx += 1
            if cmd in "Zz":
                out.append(("Z", []))
                cur = start
            continue
        rel, c = cmd.islower(), cmd.upper()

        def take(k):
            nonlocal idx
            vals = tokens[idx:idx + k]
            idx += k
            return vals

        if c == "M":
            x, y = take(2)
            if rel:
                x, y = cur[0] + x, cur[1] + y
            cur = start = (x, y)
            out.append(("M", [x, y]))
            cmd = "l" if rel else "L"
        elif c == "L":
            x, y = take(2)
            if rel:
                x, y = cur[0] + x, cur[1] + y
            cur = (x, y)
            out.append(("L", [x, y]))
        elif c == "H":
            (x,) = take(1)
            x = cur[0] + x if rel else x
            cur = (x, cur[1])
            out.append(("L", list(cur)))
        elif c == "V":
            (y,) = take(1)
            y = cur[1] + y if rel else y
            cur = (cur[0], y)
            out.append(("L", list(cur)))
        elif c == "C":
            x1, y1, x2, y2, x, y = take(6)
            if rel:
                x1, y1, x2, y2, x, y = (cur[0] + x1, cur[1] + y1,
                                        cur[0] + x2, cur[1] + y2,
                                        cur[0] + x, cur[1] + y)
            cur = (x, y)
            out.append(("C", [x1, y1, x2, y2, x, y]))
        elif c == "Q":
            x1, y1, x, y = take(4)
            if rel:
                x1, y1 = cur[0] + x1, cur[1] + y1
                x, y = cur[0] + x, cur[1] + y
            cur = (x, y)
            out.append(("Q", [x1, y1, x, y]))
        elif c == "A":
            # 本项目只有开屏 spinner 用到弧线，改为 Flutter 动画，这里直接跳过
            take(7)
        else:
            idx += 1
    return out


def fmt(v):
    s = ("%.3f" % v).rstrip("0").rstrip(".")
    return s if s not in ("-0", "") else "0"


# ---------------------------------------------------------------- 变换
def parse_transform(t):
    sx = sy = 1.0
    tx = ty = 0.0
    for name, args in re.findall(r"(translate|scale)\s*\(([^)]*)\)", t or ""):
        v = [float(x) for x in NUM.findall(args)]
        if name == "translate":
            tx += v[0] if v else 0
            ty += v[1] if len(v) > 1 else 0
        else:
            if len(v) == 1:
                sx *= v[0]
                sy *= v[0]
            elif len(v) >= 2:
                sx *= v[0]
                sy *= v[1]
    return sx, sy, tx, ty


def compose(outer, inner):
    osx, osy, otx, oty = outer
    isx, isy, itx, ity = inner
    return osx * isx, osy * isy, otx + osx * itx, oty + osy * ity


def apply(xf, x, y):
    sx, sy, tx, ty = xf
    return sx * x + tx, sy * y + ty


def transform_ops(ops, xf):
    out = []
    for cmd, a in ops:
        if cmd in ("M", "L"):
            out.append((cmd, list(apply(xf, a[0], a[1]))))
        elif cmd == "C":
            out.append((cmd, list(apply(xf, a[0], a[1])) +
                        list(apply(xf, a[2], a[3])) +
                        list(apply(xf, a[4], a[5]))))
        elif cmd == "Q":
            out.append((cmd, list(apply(xf, a[0], a[1])) +
                        list(apply(xf, a[2], a[3]))))
        else:
            out.append((cmd, a))
    return out


def ops_bounds(ops):
    xs, ys = [], []
    for cmd, a in ops:
        for i in range(0, len(a) - 1, 2):
            xs.append(a[i])
            ys.append(a[i + 1])
    if not xs:
        return None
    return min(xs), min(ys), max(xs), max(ys)


# ---------------------------------------------------------------- 颜色 / 渐变
def norm_color(v):
    v = (v or "").strip()
    if v.startswith("#"):
        h = v[1:]
        if len(h) == 3:
            h = "".join(c * 2 for c in h)
        if len(h) == 6:
            return "0xFF" + h.upper()
        if len(h) == 8:
            return "0x" + h.upper()
    return {"white": "0xFFFFFFFF", "black": "0xFF000000"}.get(v, "0xFF000000")


def read_stops(body):
    stops = []
    for sm in re.finditer(r"<stop([^>]*?)/?>", body):
        sa = sm.group(1)
        col = re.search(r'stop-color="([^"]+)"', sa)
        off = re.search(r'offset="([^"]+)"', sa)
        op = re.search(r'stop-opacity="([^"]+)"', sa)
        color = norm_color(col.group(1) if col else "#000")
        if op:
            alpha = max(0, min(255, int(round(float(op.group(1)) * 255))))
            color = color[:2] + "%02X" % alpha + color[4:]
        o = off.group(1) if off else "0"
        o = float(o[:-1]) / 100 if o.endswith("%") else float(o)
        stops.append((o, color))
    return stops


def parse_gradients(svg):
    grads = {}
    for m in re.finditer(r"<linearGradient\s+([^>]*)>(.*?)</linearGradient>",
                         svg, re.S):
        attrs, body = m.group(1), m.group(2)
        gid = re.search(r'id="([^"]+)"', attrs).group(1)

        def f(name, d):
            mm = re.search(name + r'="([^"]+)"', attrs)
            if not mm:
                return d
            v = mm.group(1)
            return float(v[:-1]) / 100 if v.endswith("%") else float(v)

        grads[gid] = ("linear", read_stops(body),
                      f("x1", 0), f("y1", 0), f("x2", 1), f("y2", 0))
    for m in re.finditer(r"<radialGradient\s+([^>]*)>(.*?)</radialGradient>",
                         svg, re.S):
        attrs, body = m.group(1), m.group(2)
        gid = re.search(r'id="([^"]+)"', attrs).group(1)
        grads[gid] = ("radial", read_stops(body), 0.5, 0.5, 0.5, 0)
    return grads


# ---------------------------------------------------------------- 主转换
def convert(svg_path):
    svg = open(svg_path, encoding="utf-8").read()
    grads = parse_gradients(svg)

    # 单独取出 mask 里的镂空路径（logo_mark 用它挖掉书页白线）
    cutouts = []
    for mm in re.finditer(r"<mask[^>]*>(.*?)</mask>", svg, re.S):
        for pm in re.finditer(r'<path[^>]*\sd="([^"]+)"[^>]*>', mm.group(1)):
            cutouts.append(parse_path(pm.group(1)))

    vb = re.search(r'viewBox="([^"]+)"', svg)
    vw, vh = [float(v) for v in NUM.findall(vb.group(1))][2:4]

    # 去掉 defs / mask / filter，避免把它们内部的图形当普通元素画出来
    body = re.sub(r"<defs.*?</defs>", "", svg, flags=re.S)
    body = re.sub(r"<mask.*?</mask>", "", body, flags=re.S)
    body = re.sub(r"<filter.*?</filter>", "", body, flags=re.S)

    shapes = []
    stack = [(1.0, 1.0, 0.0, 0.0)]

    def fill_spec(value):
        if not value or value == "none":
            return None
        if value.startswith("url(#"):
            gid = value[5:-1]
            if gid not in grads:
                return ("solid", "0xFF000000")
            return grads[gid]
        return ("solid", norm_color(value))

    for m in re.finditer(r"<(/?)(g|path|rect|circle)([^>]*?)(/?)>", body):
        closing, tag, attrs, selfclose = m.groups()
        if tag == "g":
            if closing:
                if len(stack) > 1:
                    stack.pop()
            elif not selfclose:
                tm = re.search(r'transform="([^"]+)"', attrs)
                stack.append(compose(stack[-1],
                                     parse_transform(tm.group(1) if tm else "")))
            continue

        xf = stack[-1]
        fill_a = re.search(r'fill="([^"]+)"', attrs)
        stroke_a = re.search(r'stroke="([^"]+)"', attrs)
        sw_a = re.search(r'stroke-width="([^"]+)"', attrs)
        op_a = re.search(r'opacity="([^"]+)"', attrs)
        opacity = float(op_a.group(1)) if op_a else 1.0

        if tag == "path":
            dm = re.search(r'\sd="([^"]+)"', attrs)
            if not dm:
                continue
            ops = transform_ops(parse_path(dm.group(1)), xf)
            if not ops:
                continue  # 纯弧线（spinner），交给 Flutter 动画
            shapes.append({
                "kind": "path", "ops": ops,
                "fill": fill_spec(fill_a.group(1) if fill_a else None),
                "stroke": fill_spec(stroke_a.group(1) if stroke_a else None),
                "strokeWidth": (float(sw_a.group(1)) if sw_a else 0) * abs(xf[0]),
                "opacity": opacity,
            })
        elif tag == "rect":
            x = float((re.search(r'\sx="([^"]+)"', attrs) or [None, 0])[1])
            y = float((re.search(r'\sy="([^"]+)"', attrs) or [None, 0])[1])
            w = float(re.search(r'\swidth="([^"]+)"', attrs).group(1))
            h = float(re.search(r'\sheight="([^"]+)"', attrs).group(1))
            x0, y0 = apply(xf, x, y)
            x1, y1 = apply(xf, x + w, y + h)
            shapes.append({
                "kind": "path",
                "ops": [("M", [x0, y0]), ("L", [x1, y0]),
                        ("L", [x1, y1]), ("L", [x0, y1]), ("Z", [])],
                "fill": fill_spec(fill_a.group(1) if fill_a else None),
                "stroke": None, "strokeWidth": 0, "opacity": opacity,
            })
        elif tag == "circle":
            # 开屏 spinner 的轨道圆环没有 cx/cy（以 0,0 为心），缺省按 0 处理
            cxm = re.search(r'\scx="([^"]+)"', attrs)
            cym = re.search(r'\scy="([^"]+)"', attrs)
            cx = float(cxm.group(1)) if cxm else 0.0
            cy = float(cym.group(1)) if cym else 0.0
            r = float(re.search(r'\sr="([^"]+)"', attrs).group(1))
            a, b = apply(xf, cx - r, cy - r)
            c, d2 = apply(xf, cx + r, cy + r)
            shapes.append({
                "kind": "oval", "oval": (a, b, c, d2), "ops": [],
                "fill": fill_spec(fill_a.group(1) if fill_a else None),
                "stroke": fill_spec(stroke_a.group(1) if stroke_a else None),
                "strokeWidth": (float(sw_a.group(1)) if sw_a else 0) * abs(xf[0]),
                "opacity": opacity,
            })

    cut_shapes = []
    for ops in cutouts:
        if ops:
            cut_shapes.append({"kind": "path", "ops": ops, "fill": None,
                               "stroke": None, "strokeWidth": 0, "opacity": 1.0,
                               "cutout": True})

    return {"vw": vw, "vh": vh, "shapes": shapes, "cutouts": cut_shapes}


def bounds_of(shapes):
    boxes = [ops_bounds(s["ops"]) for s in shapes
             if s["kind"] == "path" and s["ops"]]
    boxes = [b for b in boxes if b]
    if not boxes:
        return 0, 0, 1, 1
    return (min(b[0] for b in boxes), min(b[1] for b in boxes),
            max(b[2] for b in boxes), max(b[3] for b in boxes))


def emit_shape(sh, indent="  "):
    if sh["kind"] == "oval":
        a, b, c, d = sh["oval"]
        path_code = ("Path()\n    ..addOval(Rect.fromLTRB(%s, %s, %s, %s))"
                     % (fmt(a), fmt(b), fmt(c), fmt(d)))
    else:
        parts = ["Path()"]
        for cmd, arr in sh["ops"]:
            if cmd == "M":
                parts.append("..moveTo(%s, %s)" % (fmt(arr[0]), fmt(arr[1])))
            elif cmd == "L":
                parts.append("..lineTo(%s, %s)" % (fmt(arr[0]), fmt(arr[1])))
            elif cmd == "C":
                parts.append("..cubicTo(%s, %s, %s, %s, %s, %s)"
                             % tuple(fmt(v) for v in arr))
            elif cmd == "Q":
                parts.append("..quadraticBezierTo(%s, %s, %s, %s)"
                             % tuple(fmt(v) for v in arr))
            elif cmd == "Z":
                parts.append("..close()")
        path_code = "\n    ".join(parts)

    args = [path_code]
    fill = sh["fill"]
    if fill:
        if fill[0] == "solid":
            args.append("color: Color(%s)" % fill[1])
        else:
            # fill = (kind, [(offset, color), ...], ...)
            colors = ", ".join("Color(%s)" % c for _, c in fill[1])
            stops = ", ".join(fmt(o) for o, _ in fill[1])
            if fill[0] == "linear":
                args.append("gradient: BrandGradient.linear(<Color>[%s], "
                            "<double>[%s], %s, %s, %s, %s)"
                            % (colors, stops, fmt(fill[2]), fmt(fill[3]),
                               fmt(fill[4]), fmt(fill[5])))
            else:
                args.append("gradient: BrandGradient.radial(<Color>[%s], "
                            "<double>[%s], %s, %s, %s)"
                            % (colors, stops, fmt(fill[2]), fmt(fill[3]),
                               fmt(fill[4])))
    if sh["stroke"]:
        color = sh["stroke"][1] if sh["stroke"][0] == "solid" else "0xFF000000"
        args.append("strokeColor: Color(%s), strokeWidth: %s"
                    % (color, fmt(sh["strokeWidth"])))
    if abs(sh["opacity"] - 1.0) > 1e-6:
        args.append("opacity: %s" % fmt(sh["opacity"]))
    if sh.get("cutout"):
        args.append("isCutout: true")
    return "%sBrandShape(%s)," % (indent, (",\n" + indent + "  ").join(args))


def emit_block(info, prefix):
    out = []
    out.append("/// %s 的画布尺寸（取自 SVG viewBox）。" % prefix)
    out.append("const %sSize = Size(%s, %s);" % (prefix, fmt(info["vw"]), fmt(info["vh"])))
    out.append("")
    b = bounds_of(info["shapes"])
    out.append("/// %s 内容的实际包围盒，用于居中缩放。" % prefix)
    out.append("const %sBounds = Rect.fromLTRB(%s, %s, %s, %s);"
               % (prefix, fmt(b[0]), fmt(b[1]), fmt(b[2]), fmt(b[3])))
    out.append("")
    out.append("List<BrandShape> %sShapes() => <BrandShape>[" % prefix)
    for sh in info["shapes"]:
        out.append(emit_shape(sh))
    for sh in info["cutouts"]:
        out.append(emit_shape(sh, "  "))
    out.append("];")
    out.append("")
    return "\n".join(out)


def main():
    mapping = [
        (sys.argv[1], "brandMark"),
        (sys.argv[2], "brandSplashPhone"),
        (sys.argv[3], "brandSplashTv"),
        (sys.argv[4], "brandIcon"),
    ]
    blocks = []
    for path, prefix in mapping:
        info = convert(path)
        sys.stderr.write("%-18s shapes=%d cutouts=%d\n"
                         % (prefix, len(info["shapes"]), len(info["cutouts"])))
        blocks.append(emit_block(info, prefix))

    header = """// GENERATED FILE —— 由 scripts/gen_brand_art.py 从品牌 SVG 生成，请勿手改。
//
// 为什么生成 Path 代码而不是引入 flutter_svg：本机没有 Flutter SDK，
// 无法验证新依赖在 CI（Flutter 3.47.4）上能否解析成功，一旦解析失败
// 整个构建就挂。这里把 SVG 路径直接编译成 Path 指令：零依赖、零解析
// 风险，而且是真正的矢量，任意分辨率下都清晰。
//
// 重新生成：
//   python scripts/gen_brand_art.py \\
//     assets/brand/logo_mark.svg assets/brand/mobile_splash.svg \\
//     assets/brand/tv_splash.svg assets/brand/app_icon.svg > lib/brand_art.dart

import 'package:flutter/painting.dart';

/// 一块矢量图形：实色或渐变的填充 / 描边。
class BrandShape {
  const BrandShape(
    this.path, {
    this.color,
    this.gradient,
    this.strokeColor,
    this.strokeWidth = 0,
    this.opacity = 1,
    this.isCutout = false,
  });

  final Path path;
  final Color? color;
  final BrandGradient? gradient;
  final Color? strokeColor;
  final double strokeWidth;
  final double opacity;

  /// 蒙版镂空：用 dstOut 在已画内容上挖洞（对应 SVG 的 mask 黑块）。
  final bool isCutout;
}

/// 渐变描述。坐标是相对路径包围盒的比例（对应 SVG 的 objectBoundingBox）。
class BrandGradient {
  const BrandGradient.linear(
    this.colors,
    this.stops,
    this.x1,
    this.y1,
    this.x2,
    this.y2,
  )   : isRadial = false,
        cx = 0,
        cy = 0,
        radius = 0;

  const BrandGradient.radial(this.colors, this.stops, this.cx, this.cy, this.radius)
      : isRadial = true,
        x1 = 0,
        y1 = 0,
        x2 = 0,
        y2 = 0;

  final List<Color> colors;
  final List<double> stops;
  final bool isRadial;
  final double x1, y1, x2, y2, cx, cy, radius;
}

"""
    sys.stdout.write(header + "\n".join(blocks))


if __name__ == "__main__":
    main()
