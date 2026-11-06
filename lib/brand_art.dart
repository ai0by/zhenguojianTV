// GENERATED FILE —— 由 scripts/gen_brand_art.py 从品牌 SVG 生成，请勿手改。
//
// 为什么生成 Path 代码而不是引入 flutter_svg：本机没有 Flutter SDK，
// 无法验证新依赖在 CI（Flutter 3.47.4）上能否解析成功，一旦解析失败
// 整个构建就挂。这里把 SVG 路径直接编译成 Path 指令：零依赖、零解析
// 风险，而且是真正的矢量，任意分辨率下都清晰。
//
// 重新生成：
//   python scripts/gen_brand_art.py \
//     assets/brand/logo_mark.svg assets/brand/mobile_splash.svg \
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

/// brandMark 的画布尺寸（取自 SVG viewBox）。
const brandMarkSize = Size(1024, 1024);

/// brandMark 内容的实际包围盒，用于居中缩放。
const brandMarkBounds = Rect.fromLTRB(112.85, 125.25, 911.8, 915.5);

List<BrandShape> brandMarkShapes() => <BrandShape>[
  BrandShape(Path()
    ..moveTo(292.65, 242.7)
    ..cubicTo(292.65, 162.95, 324.55, 125.25, 378.2, 136.85)
    ..cubicTo(395.6, 139.75, 410.1, 147, 426.05, 155.7)
    ..lineTo(840.75, 402.2)
    ..cubicTo(887.15, 429.75, 911.8, 471.8, 911.8, 521.1)
    ..cubicTo(911.8, 573.3, 884.25, 621.15, 840.75, 647.25)
    ..lineTo(732, 713.95)
    ..cubicTo(803.05, 657.4, 810.3, 579.1, 749.4, 531.25)
    ..cubicTo(740.7, 524, 727.65, 516.75, 716.05, 509.5)
    ..lineTo(292.65, 242.7)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFAA000A), Color(0xFFD30012), Color(0xFFFF4647), Color(0xFFFF7770)], <double>[0, 0.3, 0.71, 1], 0.18, 0.05, 0.86, 0.9)),
  BrandShape(Path()
    ..moveTo(292.65, 764.7)
    ..lineTo(658.05, 547.2)
    ..cubicTo(684.15, 531.25, 695.75, 515.3, 682.7, 489.2)
    ..lineTo(736.35, 519.65)
    ..cubicTo(804.5, 558.8, 807.4, 645.8, 732, 698)
    ..lineTo(426.05, 874.9)
    ..cubicTo(357.9, 915.5, 292.65, 860.4, 292.65, 764.7)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFB90010), Color(0xFFD30918), Color(0xFFE92830), Color(0xFFFF5B55)], <double>[0, 0.4, 0.84, 1], 0.06, 0.25, 0.95, 0.8)),
  BrandShape(Path()
    ..moveTo(112.85, 360.15)
    ..cubicTo(112.85, 239.8, 192.6, 145.55, 308.6, 133.95)
    ..cubicTo(340.5, 129.6, 369.5, 133.95, 394.15, 142.65)
    ..cubicTo(331.8, 135.4, 292.65, 176, 292.65, 245.6)
    ..lineTo(292.65, 767.6)
    ..cubicTo(292.65, 832.85, 328.9, 883.6, 397.05, 892.3)
    ..cubicTo(240.45, 902.45, 112.85, 832.85, 112.85, 718.3)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFEC000A), Color(0xFFEE111D), Color(0xFFFF5B58), Color(0xFFFF8C83)], <double>[0, 0.46, 0.82, 1], 0.1, 0.65, 1, 0.1)),
  BrandShape(Path()
    ..moveTo(294.1, 244.15)
    ..cubicTo(294.1, 164.4, 327.45, 128.15, 385.45, 141.2),
    strokeColor: Color(0xFFFFACA6), strokeWidth: 4.35,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(292.65, 764.7)
    ..cubicTo(292.65, 844.45, 324.55, 885.05, 392.7, 889.4),
    strokeColor: Color(0xFFFF847B), strokeWidth: 4.35,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(397, 168)
    ..lineTo(650, 319)
    ..cubicTo(672, 332, 674, 350, 653, 364)
    ..lineTo(397, 525)
    ..close(),
    isCutout: true),
  BrandShape(Path()
    ..moveTo(312, 177)
    ..quadraticBezierTo(313, 173, 322, 170)
    ..lineTo(353, 160)
    ..quadraticBezierTo(361, 158, 360, 166)
    ..lineTo(357, 193)
    ..quadraticBezierTo(357, 199, 350, 202)
    ..lineTo(316, 212)
    ..quadraticBezierTo(307, 214, 307, 206)
    ..close(),
    isCutout: true),
  BrandShape(Path()
    ..moveTo(307, 263)
    ..quadraticBezierTo(308, 257, 316, 255)
    ..lineTo(347, 247)
    ..quadraticBezierTo(356, 245, 356, 253)
    ..lineTo(356, 281)
    ..quadraticBezierTo(355, 288, 347, 291)
    ..lineTo(313, 298)
    ..quadraticBezierTo(304, 299, 304, 291)
    ..close(),
    isCutout: true),
  BrandShape(Path()
    ..moveTo(305, 345)
    ..quadraticBezierTo(306, 340, 313, 338)
    ..lineTo(347, 331)
    ..quadraticBezierTo(355, 329, 356, 337)
    ..lineTo(356, 364)
    ..quadraticBezierTo(355, 371, 347, 373)
    ..lineTo(313, 377)
    ..quadraticBezierTo(304, 378, 304, 370)
    ..close(),
    isCutout: true),
  BrandShape(Path()
    ..moveTo(306, 422)
    ..quadraticBezierTo(307, 417, 314, 416)
    ..lineTo(347, 411)
    ..quadraticBezierTo(356, 409, 356, 418)
    ..lineTo(356, 448)
    ..quadraticBezierTo(356, 455, 348, 456)
    ..lineTo(313, 459)
    ..quadraticBezierTo(304, 460, 304, 452)
    ..close(),
    isCutout: true),
  BrandShape(Path()
    ..moveTo(312, 501)
    ..lineTo(346, 501)
    ..quadraticBezierTo(354, 501, 356, 509)
    ..lineTo(359, 534)
    ..quadraticBezierTo(360, 542, 353, 543)
    ..lineTo(321, 540)
    ..quadraticBezierTo(313, 539, 311, 531)
    ..lineTo(307, 509)
    ..quadraticBezierTo(306, 502, 312, 501)
    ..close(),
    isCutout: true),
];

/// brandSplashPhone 的画布尺寸（取自 SVG viewBox）。
const brandSplashPhoneSize = Size(1080, 1920);

/// brandSplashPhone 内容的实际包围盒，用于居中缩放。
const brandSplashPhoneBounds = Rect.fromLTRB(0, 0, 1080.32, 1926.4);

List<BrandShape> brandSplashPhoneShapes() => <BrandShape>[
  BrandShape(Path()
    ..moveTo(0, 0)
    ..lineTo(1080, 0)
    ..lineTo(1080, 1920)
    ..lineTo(0, 1920)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFFFFFFF), Color(0xFFFFFDFD), Color(0xFFF6F3F3)], <double>[0, 0.58, 1], 0, 0, 1, 1)),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(55, 320, 1025, 1290)),
    gradient: BrandGradient.radial(<Color>[Color(0xBFFFE4E1), Color(0x4CFFF0EE), Color(0x00FFFFFF)], <double>[0, 0.7, 1], 0.5, 0.5, 0.5)),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(150, 415, 930, 1195)),
    strokeColor: Color(0xFFED9E9E), strokeWidth: 1.5,
    opacity: 0.23),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(98, 363, 982, 1247)),
    strokeColor: Color(0xFFED9E9E), strokeWidth: 1,
    opacity: 0.12),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(125, 740, 135, 750)),
    color: Color(0xFFF56C6B),
    opacity: 0.55),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(951.5, 856.5, 958.5, 863.5)),
    color: Color(0xFFF56C6B),
    opacity: 0.4),
  BrandShape(Path()
    ..moveTo(6.33, 1610.7)
    ..cubicTo(119.215, 1762.5, 261.64, 1867, 448.375, 1885.7)
    ..cubicTo(675.2, 1914.3, 837.67, 1715.2, 1080.32, 1682.2)
    ..lineTo(1080.32, 1926.4)
    ..lineTo(6.33, 1926.4)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFFF6A64), Color(0xFFFFB9B7), Color(0xFFFFE6E6)], <double>[0, 0.5, 1], 0, 0, 1, 1),
    opacity: 0.85),
  BrandShape(Path()
    ..moveTo(6.33, 1759.2)
    ..cubicTo(179.35, 1854.9, 322.83, 1913.2, 494.795, 1908.8)
    ..cubicTo(684.695, 1903.3, 831.34, 1758.1, 1080.32, 1779)
    ..lineTo(1080.32, 1926.4)
    ..lineTo(6.33, 1926.4)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFE1000A), Color(0xFFED0A15), Color(0xFFFF5354)], <double>[0, 0.55, 1], 0, 1, 1, 0)),
  BrandShape(Path()
    ..moveTo(6.33, 1790)
    ..cubicTo(173.02, 1894.5, 319.665, 1925.3, 494.795, 1912.1)
    ..cubicTo(291.18, 1904.4, 121.325, 1803.2, 6.33, 1610.7)
    ..close(),
    color: Color(0xFFFF544F),
    opacity: 0.8),
  BrandShape(Path()
    ..moveTo(432.21, 634.38)
    ..cubicTo(432.21, 583.23, 452.67, 559.05, 487.08, 566.49)
    ..cubicTo(498.24, 568.35, 507.54, 573, 517.77, 578.58)
    ..lineTo(783.75, 736.68)
    ..cubicTo(813.51, 754.35, 829.32, 781.32, 829.32, 812.94)
    ..cubicTo(829.32, 846.42, 811.65, 877.11, 783.75, 893.85)
    ..lineTo(714, 936.63)
    ..cubicTo(759.57, 900.36, 764.22, 850.14, 725.16, 819.45)
    ..cubicTo(719.58, 814.8, 711.21, 810.15, 703.77, 805.5)
    ..lineTo(432.21, 634.38)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFAA000A), Color(0xFFD30012), Color(0xFFFF4647), Color(0xFFFF7770)], <double>[0, 0.3, 0.71, 1], 0.18, 0.05, 0.86, 0.9)),
  BrandShape(Path()
    ..moveTo(432.21, 969.18)
    ..lineTo(666.57, 829.68)
    ..cubicTo(683.31, 819.45, 690.75, 809.22, 682.38, 792.48)
    ..lineTo(716.79, 812.01)
    ..cubicTo(760.5, 837.12, 762.36, 892.92, 714, 926.4)
    ..lineTo(517.77, 1039.86)
    ..cubicTo(474.06, 1065.9, 432.21, 1030.56, 432.21, 969.18)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFB90010), Color(0xFFD30918), Color(0xFFE92830), Color(0xFFFF5B55)], <double>[0, 0.4, 0.84, 1], 0.06, 0.25, 0.95, 0.8)),
  BrandShape(Path()
    ..moveTo(316.89, 709.71)
    ..cubicTo(316.89, 632.52, 368.04, 572.07, 442.44, 564.63)
    ..cubicTo(462.9, 561.84, 481.5, 564.63, 497.31, 570.21)
    ..cubicTo(457.32, 565.56, 432.21, 591.6, 432.21, 636.24)
    ..lineTo(432.21, 971.04)
    ..cubicTo(432.21, 1012.89, 455.46, 1045.44, 499.17, 1051.02)
    ..cubicTo(398.73, 1057.53, 316.89, 1012.89, 316.89, 939.42)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFEC000A), Color(0xFFEE111D), Color(0xFFFF5B58), Color(0xFFFF8C83)], <double>[0, 0.46, 0.82, 1], 0.1, 0.65, 1, 0.1)),
  BrandShape(Path()
    ..moveTo(432.21, 636.24)
    ..lineTo(667.5, 776.67)
    ..cubicTo(687.96, 788.76, 689.82, 805.5, 670.29, 818.52)
    ..lineTo(432.21, 968.25)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(353.16, 644.61)
    ..quadraticBezierTo(354.09, 640.89, 362.46, 638.1)
    ..lineTo(391.29, 628.8)
    ..quadraticBezierTo(398.73, 626.94, 397.8, 634.38)
    ..lineTo(395.01, 659.49)
    ..quadraticBezierTo(395.01, 665.07, 388.5, 667.86)
    ..lineTo(356.88, 677.16)
    ..quadraticBezierTo(348.51, 679.02, 348.51, 671.58)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(348.51, 724.59)
    ..quadraticBezierTo(349.44, 719.01, 356.88, 717.15)
    ..lineTo(385.71, 709.71)
    ..quadraticBezierTo(394.08, 707.85, 394.08, 715.29)
    ..lineTo(394.08, 741.33)
    ..quadraticBezierTo(393.15, 747.84, 385.71, 750.63)
    ..lineTo(354.09, 757.14)
    ..quadraticBezierTo(345.72, 758.07, 345.72, 750.63)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(346.65, 800.85)
    ..quadraticBezierTo(347.58, 796.2, 354.09, 794.34)
    ..lineTo(385.71, 787.83)
    ..quadraticBezierTo(393.15, 785.97, 394.08, 793.41)
    ..lineTo(394.08, 818.52)
    ..quadraticBezierTo(393.15, 825.03, 385.71, 826.89)
    ..lineTo(354.09, 830.61)
    ..quadraticBezierTo(345.72, 831.54, 345.72, 824.1)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(347.58, 872.46)
    ..quadraticBezierTo(348.51, 867.81, 355.02, 866.88)
    ..lineTo(385.71, 862.23)
    ..quadraticBezierTo(394.08, 860.37, 394.08, 868.74)
    ..lineTo(394.08, 896.64)
    ..quadraticBezierTo(394.08, 903.15, 386.64, 904.08)
    ..lineTo(354.09, 906.87)
    ..quadraticBezierTo(345.72, 907.8, 345.72, 900.36)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(353.16, 945.93)
    ..lineTo(384.78, 945.93)
    ..quadraticBezierTo(392.22, 945.93, 394.08, 953.37)
    ..lineTo(396.87, 976.62)
    ..quadraticBezierTo(397.8, 984.06, 391.29, 984.99)
    ..lineTo(361.53, 982.2)
    ..quadraticBezierTo(354.09, 981.27, 352.23, 973.83)
    ..lineTo(348.51, 953.37)
    ..quadraticBezierTo(347.58, 946.86, 353.16, 945.93)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(433.14, 635.31)
    ..cubicTo(433.14, 584.16, 454.53, 560.91, 491.73, 569.28),
    strokeColor: Color(0xFFFFACA6), strokeWidth: 2.79,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(432.21, 969.18)
    ..cubicTo(432.21, 1020.33, 452.67, 1046.37, 496.38, 1049.16),
    strokeColor: Color(0xFFFF847B), strokeWidth: 2.79,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(297.552, 1285.137)
    ..lineTo(311.516, 1270.625)
    ..quadraticBezierTo(320.109, 1272.941, 329.162, 1275.951)
    ..quadraticBezierTo(338.216, 1278.962, 346.272, 1282.049)
    ..quadraticBezierTo(354.328, 1285.137, 359.852, 1287.916)
    ..lineTo(341.131, 1302.89)
    ..quadraticBezierTo(336.221, 1300.112, 329.162, 1296.87)
    ..quadraticBezierTo(322.104, 1293.628, 313.971, 1290.54)
    ..quadraticBezierTo(305.838, 1287.452, 297.552, 1285.137)
    ..close()
    ..moveTo(220.213, 1256.576)
    ..lineTo(357.704, 1256.576)
    ..lineTo(357.704, 1274.793)
    ..lineTo(220.213, 1274.793)
    ..close()
    ..moveTo(224.049, 1165.184)
    ..lineTo(354.021, 1165.184)
    ..lineTo(354.021, 1183.709)
    ..lineTo(224.049, 1183.709)
    ..close()
    ..moveTo(262.258, 1272.169)
    ..lineTo(280.365, 1285.291)
    ..quadraticBezierTo(274.381, 1288.842, 266.478, 1292.238)
    ..quadraticBezierTo(258.575, 1295.635, 250.136, 1298.491)
    ..quadraticBezierTo(241.696, 1301.347, 234.023, 1303.045)
    ..quadraticBezierTo(231.261, 1299.803, 226.888, 1295.326)
    ..quadraticBezierTo(222.515, 1290.849, 218.832, 1287.761)
    ..quadraticBezierTo(226.658, 1286.217, 234.867, 1283.67)
    ..quadraticBezierTo(243.077, 1281.123, 250.366, 1278.035)
    ..quadraticBezierTo(257.655, 1274.948, 262.258, 1272.169)
    ..close()
    ..moveTo(250.596, 1206.866)
    ..lineTo(323.945, 1206.866)
    ..lineTo(323.945, 1219.062)
    ..lineTo(250.596, 1219.062)
    ..close()
    ..moveTo(250.596, 1223.076)
    ..lineTo(323.945, 1223.076)
    ..lineTo(323.945, 1235.118)
    ..lineTo(250.596, 1235.118)
    ..close()
    ..moveTo(250.596, 1239.44)
    ..lineTo(323.945, 1239.44)
    ..lineTo(323.945, 1251.482)
    ..lineTo(250.596, 1251.482)
    ..close()
    ..moveTo(239.701, 1188.804)
    ..lineTo(338.83, 1188.804)
    ..lineTo(338.83, 1263.215)
    ..lineTo(316.273, 1263.215)
    ..lineTo(316.273, 1202.389)
    ..lineTo(261.184, 1202.389)
    ..lineTo(261.184, 1263.215)
    ..lineTo(239.701, 1263.215)
    ..close()
    ..moveTo(281.133, 1155.612)
    ..lineTo(304.61, 1157.773)
    ..quadraticBezierTo(303.383, 1165.029, 301.925, 1172.053)
    ..quadraticBezierTo(300.467, 1179.078, 299.086, 1185.176)
    ..quadraticBezierTo(297.705, 1191.274, 296.324, 1195.905)
    ..lineTo(275.301, 1193.435)
    ..quadraticBezierTo(276.682, 1188.186, 277.833, 1181.625)
    ..quadraticBezierTo(278.984, 1175.064, 279.905, 1168.271)
    ..quadraticBezierTo(280.826, 1161.478, 281.133, 1155.612)
    ..close()),
  BrandShape(Path()
    ..moveTo(373.049, 1236.97)
    ..lineTo(511.921, 1236.97)
    ..lineTo(511.921, 1257.348)
    ..lineTo(373.049, 1257.348)
    ..close()
    ..moveTo(430.9, 1170.741)
    ..lineTo(454.684, 1170.741)
    ..lineTo(454.684, 1302.736)
    ..lineTo(430.9, 1302.736)
    ..close()
    ..moveTo(425.376, 1247.314)
    ..lineTo(443.789, 1255.341)
    ..quadraticBezierTo(436.577, 1264.759, 426.91, 1273.172)
    ..quadraticBezierTo(417.243, 1281.586, 406.041, 1288.224)
    ..quadraticBezierTo(394.839, 1294.863, 383.023, 1299.185)
    ..quadraticBezierTo(381.335, 1296.407, 378.727, 1292.933)
    ..quadraticBezierTo(376.118, 1289.459, 373.279, 1286.14)
    ..quadraticBezierTo(370.44, 1282.821, 367.985, 1280.505)
    ..quadraticBezierTo(376.578, 1278.035, 384.865, 1274.562)
    ..quadraticBezierTo(393.151, 1271.088, 400.747, 1266.688)
    ..quadraticBezierTo(408.343, 1262.289, 414.634, 1257.426)
    ..quadraticBezierTo(420.925, 1252.563, 425.376, 1247.314)
    ..close()
    ..moveTo(458.06, 1245.924)
    ..quadraticBezierTo(462.664, 1251.019, 469.185, 1255.959)
    ..quadraticBezierTo(475.707, 1260.899, 483.456, 1265.222)
    ..quadraticBezierTo(491.206, 1269.544, 499.722, 1273.095)
    ..quadraticBezierTo(508.239, 1276.646, 516.678, 1279.116)
    ..quadraticBezierTo(514.223, 1281.277, 511.384, 1284.674)
    ..quadraticBezierTo(508.545, 1288.07, 505.937, 1291.621)
    ..quadraticBezierTo(503.328, 1295.171, 501.64, 1298.105)
    ..quadraticBezierTo(493.047, 1294.708, 484.53, 1290)
    ..quadraticBezierTo(476.014, 1285.291, 468.111, 1279.579)
    ..quadraticBezierTo(460.209, 1273.867, 453.227, 1267.306)
    ..quadraticBezierTo(446.245, 1260.745, 440.721, 1253.643)
    ..close()
    ..moveTo(411.565, 1205.014)
    ..lineTo(411.565, 1211.498)
    ..lineTo(473.098, 1211.498)
    ..lineTo(473.098, 1205.014)
    ..close()
    ..moveTo(411.565, 1181.548)
    ..lineTo(411.565, 1187.877)
    ..lineTo(473.098, 1187.877)
    ..lineTo(473.098, 1181.548)
    ..close()
    ..moveTo(388.241, 1163.022)
    ..lineTo(497.65, 1163.022)
    ..lineTo(497.65, 1230.023)
    ..lineTo(388.241, 1230.023)
    ..close()),
  BrandShape(Path()
    ..moveTo(564.248, 1229.56)
    ..lineTo(627.162, 1229.56)
    ..lineTo(627.162, 1244.998)
    ..lineTo(564.248, 1244.998)
    ..close()
    ..moveTo(548.442, 1270.934)
    ..lineTo(567.624, 1264.913)
    ..quadraticBezierTo(569.465, 1268.309, 571.537, 1272.4)
    ..quadraticBezierTo(573.608, 1276.492, 574.682, 1279.27)
    ..lineTo(554.427, 1286.063)
    ..quadraticBezierTo(553.66, 1283.13, 551.895, 1278.807)
    ..quadraticBezierTo(550.13, 1274.485, 548.442, 1270.934)
    ..close()
    ..moveTo(622.405, 1266.148)
    ..lineTo(645.116, 1269.699)
    ..quadraticBezierTo(641.587, 1276.337, 638.057, 1282.281)
    ..quadraticBezierTo(634.528, 1288.224, 631.612, 1292.547)
    ..lineTo(613.812, 1288.688)
    ..quadraticBezierTo(615.5, 1285.291, 617.111, 1281.354)
    ..quadraticBezierTo(618.723, 1277.418, 620.104, 1273.481)
    ..quadraticBezierTo(621.485, 1269.544, 622.405, 1266.148)
    ..close()
    ..moveTo(539.542, 1249.784)
    ..lineTo(653.556, 1249.784)
    ..lineTo(653.556, 1266.148)
    ..lineTo(539.542, 1266.148)
    ..close()
    ..moveTo(527.573, 1281.432)
    ..lineTo(663.07, 1281.432)
    ..lineTo(663.07, 1299.494)
    ..lineTo(527.573, 1299.494)
    ..close()
    ..moveTo(584.043, 1235.426)
    ..lineTo(606.907, 1235.426)
    ..lineTo(606.907, 1291.775)
    ..lineTo(584.043, 1291.775)
    ..close()
    ..moveTo(609.976, 1169.506)
    ..lineTo(665.371, 1169.506)
    ..lineTo(665.371, 1188.032)
    ..lineTo(609.976, 1188.032)
    ..close()
    ..moveTo(605.679, 1156.075)
    ..lineTo(626.855, 1160.861)
    ..quadraticBezierTo(623.019, 1173.52, 616.421, 1185.021)
    ..quadraticBezierTo(609.822, 1196.523, 602.15, 1203.933)
    ..quadraticBezierTo(600.155, 1202.08, 596.779, 1199.765)
    ..quadraticBezierTo(593.403, 1197.449, 589.951, 1195.21)
    ..quadraticBezierTo(586.498, 1192.972, 583.889, 1191.582)
    ..quadraticBezierTo(591.408, 1185.407, 597.086, 1175.913)
    ..quadraticBezierTo(602.764, 1166.419, 605.679, 1156.075)
    ..close()
    ..moveTo(613.812, 1196.831)
    ..lineTo(626.395, 1183.092)
    ..quadraticBezierTo(630.845, 1185.87, 636.446, 1189.498)
    ..quadraticBezierTo(642.047, 1193.126, 647.418, 1196.677)
    ..quadraticBezierTo(652.788, 1200.228, 656.164, 1203.007)
    ..lineTo(642.814, 1218.599)
    ..quadraticBezierTo(639.745, 1215.666, 634.605, 1211.883)
    ..quadraticBezierTo(629.464, 1208.101, 623.94, 1204.087)
    ..quadraticBezierTo(618.416, 1200.073, 613.812, 1196.831)
    ..close()
    ..moveTo(559.951, 1156.384)
    ..lineTo(581.894, 1156.384)
    ..lineTo(581.894, 1214.122)
    ..lineTo(559.951, 1214.122)
    ..close()
    ..moveTo(593.557, 1199.919)
    ..lineTo(611.664, 1209.182)
    ..quadraticBezierTo(602.15, 1218.445, 589.49, 1225.315)
    ..quadraticBezierTo(576.831, 1232.184, 562.33, 1237.047)
    ..quadraticBezierTo(547.829, 1241.91, 532.791, 1245.152)
    ..quadraticBezierTo(531.103, 1240.984, 527.803, 1235.89)
    ..quadraticBezierTo(524.504, 1230.795, 520.975, 1227.09)
    ..quadraticBezierTo(534.785, 1224.929, 548.596, 1221.301)
    ..quadraticBezierTo(562.406, 1217.673, 574.222, 1212.347)
    ..quadraticBezierTo(586.038, 1207.021, 593.557, 1199.919)
    ..close()
    ..moveTo(603.991, 1207.947)
    ..quadraticBezierTo(609.362, 1211.034, 616.958, 1213.89)
    ..quadraticBezierTo(624.554, 1216.746, 633.607, 1219.139)
    ..quadraticBezierTo(642.661, 1221.532, 652.251, 1223.385)
    ..quadraticBezierTo(661.842, 1225.237, 671.202, 1226.472)
    ..quadraticBezierTo(669.054, 1228.479, 666.522, 1231.644)
    ..quadraticBezierTo(663.99, 1234.809, 661.765, 1238.051)
    ..quadraticBezierTo(659.54, 1241.293, 658.159, 1243.763)
    ..quadraticBezierTo(648.799, 1242.065, 639.131, 1239.44)
    ..quadraticBezierTo(629.464, 1236.816, 620.027, 1233.419)
    ..quadraticBezierTo(610.59, 1230.023, 602.15, 1225.932)
    ..quadraticBezierTo(593.71, 1221.841, 586.805, 1217.364)
    ..close()
    ..moveTo(530.335, 1162.868)
    ..lineTo(551.818, 1162.868)
    ..lineTo(551.818, 1210.88)
    ..lineTo(530.335, 1210.88)
    ..close()),
  BrandShape(Path()
    ..moveTo(685.45, 1154.15)
    ..lineTo(781.24, 1154.15)
    ..lineTo(775.66, 1181.12)
    ..lineTo(744.97, 1181.12)
    ..lineTo(721.72, 1282.49)
    ..lineTo(699.4, 1282.49)
    ..lineTo(720.79, 1181.12)
    ..lineTo(679.87, 1181.12)
    ..close()),
  BrandShape(Path()
    ..moveTo(785.89, 1154.15)
    ..lineTo(815.65, 1154.15)
    ..lineTo(831.46, 1239.71)
    ..lineTo(872.38, 1154.15)
    ..lineTo(903.07, 1154.15)
    ..lineTo(846.34, 1282.49)
    ..lineTo(813.79, 1282.49)
    ..close()),
  BrandShape(Path()
    ..moveTo(243.7, 1353.17)
    ..cubicTo(408.31, 1290.86, 637.09, 1289, 837.04, 1347.59)
    ..cubicTo(637.09, 1301.09, 412.96, 1301.09, 243.7, 1353.17)
    ..close(),
    color: Color(0xFFF20B1B)),
  BrandShape(Path()
    ..moveTo(244.63, 1357.82)
    ..cubicTo(415.75, 1306.67, 631.51, 1304.81, 831.46, 1350.38),
    strokeColor: Color(0xFFFFC3C0), strokeWidth: 2.79,
    opacity: 0.75),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(509, 1489, 571, 1551)),
    strokeColor: Color(0xFFF6CECE), strokeWidth: 6),
  BrandShape(Path()
    ..moveTo(540, 1489),
    strokeColor: Color(0xFFED0614), strokeWidth: 6),
];

/// brandSplashTv 的画布尺寸（取自 SVG viewBox）。
const brandSplashTvSize = Size(1920, 1080);

/// brandSplashTv 内容的实际包围盒，用于居中缩放。
const brandSplashTvBounds = Rect.fromLTRB(0, 0, 1920, 1082.88);

List<BrandShape> brandSplashTvShapes() => <BrandShape>[
  BrandShape(Path()
    ..moveTo(0, 0)
    ..lineTo(1920, 0)
    ..lineTo(1920, 1080)
    ..lineTo(0, 1080)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFFFFFFF), Color(0xFFFFFDFD), Color(0xFFF6F3F3)], <double>[0, 0.58, 1], 0, 0, 1, 1)),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(555, -53, 1365, 757)),
    gradient: BrandGradient.radial(<Color>[Color(0xBFFFE4E1), Color(0x4CFFF0EE), Color(0x00FFFFFF)], <double>[0, 0.7, 1], 0.5, 0.5, 0.5)),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(650, 42, 1270, 662)),
    strokeColor: Color(0xFFED9E9E), strokeWidth: 1.5,
    opacity: 0.23),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(598, -10, 1322, 714)),
    strokeColor: Color(0xFFED9E9E), strokeWidth: 1,
    opacity: 0.12),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(625, 287, 635, 297)),
    color: Color(0xFFF56C6B),
    opacity: 0.55),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(1291.5, 403.5, 1298.5, 410.5)),
    color: Color(0xFFF56C6B),
    opacity: 0.4),
  BrandShape(Path()
    ..moveTo(11.25, 833.19)
    ..cubicTo(211.875, 953.25, 465, 1035.9, 796.875, 1050.69)
    ..cubicTo(1200, 1073.31, 1488.75, 915.84, 1920, 889.74)
    ..lineTo(1920, 1082.88)
    ..lineTo(11.25, 1082.88)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFFF6A64), Color(0xFFFFB9B7), Color(0xFFFFE6E6)], <double>[0, 0.5, 1], 0, 0, 1, 1),
    opacity: 0.85),
  BrandShape(Path()
    ..moveTo(11.25, 950.64)
    ..cubicTo(318.75, 1026.33, 573.75, 1072.44, 879.375, 1068.96)
    ..cubicTo(1216.875, 1064.61, 1477.5, 949.77, 1920, 966.3)
    ..lineTo(1920, 1082.88)
    ..lineTo(11.25, 1082.88)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFE1000A), Color(0xFFED0A15), Color(0xFFFF5354)], <double>[0, 0.55, 1], 0, 1, 1, 0)),
  BrandShape(Path()
    ..moveTo(11.25, 975)
    ..cubicTo(307.5, 1057.65, 568.125, 1082.01, 879.375, 1071.57)
    ..cubicTo(517.5, 1065.48, 215.625, 985.44, 11.25, 833.19)
    ..close(),
    color: Color(0xFFFF544F),
    opacity: 0.8),
  BrandShape(Path()
    ..moveTo(872.75, 218.5)
    ..cubicTo(872.75, 177.25, 889.25, 157.75, 917, 163.75)
    ..cubicTo(926, 165.25, 933.5, 169, 941.75, 173.5)
    ..lineTo(1156.25, 301)
    ..cubicTo(1180.25, 315.25, 1193, 337, 1193, 362.5)
    ..cubicTo(1193, 389.5, 1178.75, 414.25, 1156.25, 427.75)
    ..lineTo(1100, 462.25)
    ..cubicTo(1136.75, 433, 1140.5, 392.5, 1109, 367.75)
    ..cubicTo(1104.5, 364, 1097.75, 360.25, 1091.75, 356.5)
    ..lineTo(872.75, 218.5)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFAA000A), Color(0xFFD30012), Color(0xFFFF4647), Color(0xFFFF7770)], <double>[0, 0.3, 0.71, 1], 0.18, 0.05, 0.86, 0.9)),
  BrandShape(Path()
    ..moveTo(872.75, 488.5)
    ..lineTo(1061.75, 376)
    ..cubicTo(1075.25, 367.75, 1081.25, 359.5, 1074.5, 346)
    ..lineTo(1102.25, 361.75)
    ..cubicTo(1137.5, 382, 1139, 427, 1100, 454)
    ..lineTo(941.75, 545.5)
    ..cubicTo(906.5, 566.5, 872.75, 538, 872.75, 488.5)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFB90010), Color(0xFFD30918), Color(0xFFE92830), Color(0xFFFF5B55)], <double>[0, 0.4, 0.84, 1], 0.06, 0.25, 0.95, 0.8)),
  BrandShape(Path()
    ..moveTo(779.75, 279.25)
    ..cubicTo(779.75, 217, 821, 168.25, 881, 162.25)
    ..cubicTo(897.5, 160, 912.5, 162.25, 925.25, 166.75)
    ..cubicTo(893, 163, 872.75, 184, 872.75, 220)
    ..lineTo(872.75, 490)
    ..cubicTo(872.75, 523.75, 891.5, 550, 926.75, 554.5)
    ..cubicTo(845.75, 559.75, 779.75, 523.75, 779.75, 464.5)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFEC000A), Color(0xFFEE111D), Color(0xFFFF5B58), Color(0xFFFF8C83)], <double>[0, 0.46, 0.82, 1], 0.1, 0.65, 1, 0.1)),
  BrandShape(Path()
    ..moveTo(872.75, 220)
    ..lineTo(1062.5, 333.25)
    ..cubicTo(1079, 343, 1080.5, 356.5, 1064.75, 367)
    ..lineTo(872.75, 487.75)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(809, 226.75)
    ..quadraticBezierTo(809.75, 223.75, 816.5, 221.5)
    ..lineTo(839.75, 214)
    ..quadraticBezierTo(845.75, 212.5, 845, 218.5)
    ..lineTo(842.75, 238.75)
    ..quadraticBezierTo(842.75, 243.25, 837.5, 245.5)
    ..lineTo(812, 253)
    ..quadraticBezierTo(805.25, 254.5, 805.25, 248.5)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(805.25, 291.25)
    ..quadraticBezierTo(806, 286.75, 812, 285.25)
    ..lineTo(835.25, 279.25)
    ..quadraticBezierTo(842, 277.75, 842, 283.75)
    ..lineTo(842, 304.75)
    ..quadraticBezierTo(841.25, 310, 835.25, 312.25)
    ..lineTo(809.75, 317.5)
    ..quadraticBezierTo(803, 318.25, 803, 312.25)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(803.75, 352.75)
    ..quadraticBezierTo(804.5, 349, 809.75, 347.5)
    ..lineTo(835.25, 342.25)
    ..quadraticBezierTo(841.25, 340.75, 842, 346.75)
    ..lineTo(842, 367)
    ..quadraticBezierTo(841.25, 372.25, 835.25, 373.75)
    ..lineTo(809.75, 376.75)
    ..quadraticBezierTo(803, 377.5, 803, 371.5)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(804.5, 410.5)
    ..quadraticBezierTo(805.25, 406.75, 810.5, 406)
    ..lineTo(835.25, 402.25)
    ..quadraticBezierTo(842, 400.75, 842, 407.5)
    ..lineTo(842, 430)
    ..quadraticBezierTo(842, 435.25, 836, 436)
    ..lineTo(809.75, 438.25)
    ..quadraticBezierTo(803, 439, 803, 433)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(809, 469.75)
    ..lineTo(834.5, 469.75)
    ..quadraticBezierTo(840.5, 469.75, 842, 475.75)
    ..lineTo(844.25, 494.5)
    ..quadraticBezierTo(845, 500.5, 839.75, 501.25)
    ..lineTo(815.75, 499)
    ..quadraticBezierTo(809.75, 498.25, 808.25, 492.25)
    ..lineTo(805.25, 475.75)
    ..quadraticBezierTo(804.5, 470.5, 809, 469.75)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(873.5, 219.25)
    ..cubicTo(873.5, 178, 890.75, 159.25, 920.75, 166),
    strokeColor: Color(0xFFFFACA6), strokeWidth: 2.25,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(872.75, 488.5)
    ..cubicTo(872.75, 529.75, 889.25, 550.75, 924.5, 553),
    strokeColor: Color(0xFFFF847B), strokeWidth: 2.25,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(762.929, 756.885)
    ..lineTo(774.19, 745.181)
    ..quadraticBezierTo(781.12, 747.049, 788.421, 749.477)
    ..quadraticBezierTo(795.722, 751.904, 802.219, 754.395)
    ..quadraticBezierTo(808.716, 756.885, 813.171, 759.125)
    ..lineTo(798.074, 771.202)
    ..quadraticBezierTo(794.114, 768.961, 788.421, 766.346)
    ..quadraticBezierTo(782.729, 763.732, 776.17, 761.242)
    ..quadraticBezierTo(769.611, 758.752, 762.929, 756.885)
    ..close()
    ..moveTo(700.559, 733.852)
    ..lineTo(811.439, 733.852)
    ..lineTo(811.439, 748.543)
    ..lineTo(700.559, 748.543)
    ..close()
    ..moveTo(703.653, 660.148)
    ..lineTo(808.469, 660.148)
    ..lineTo(808.469, 675.088)
    ..lineTo(703.653, 675.088)
    ..close()
    ..moveTo(734.466, 746.427)
    ..lineTo(749.069, 757.009)
    ..quadraticBezierTo(744.243, 759.873, 737.869, 762.611)
    ..quadraticBezierTo(731.496, 765.351, 724.69, 767.654)
    ..quadraticBezierTo(717.884, 769.957, 711.696, 771.327)
    ..quadraticBezierTo(709.469, 768.712, 705.942, 765.101)
    ..quadraticBezierTo(702.415, 761.491, 699.445, 759.001)
    ..quadraticBezierTo(705.756, 757.756, 712.377, 755.702)
    ..quadraticBezierTo(718.997, 753.648, 724.876, 751.158)
    ..quadraticBezierTo(730.754, 748.668, 734.466, 746.427)
    ..close()
    ..moveTo(725.061, 693.763)
    ..lineTo(784.214, 693.763)
    ..lineTo(784.214, 703.599)
    ..lineTo(725.061, 703.599)
    ..close()
    ..moveTo(725.061, 706.836)
    ..lineTo(784.214, 706.836)
    ..lineTo(784.214, 716.547)
    ..lineTo(725.061, 716.547)
    ..close()
    ..moveTo(725.061, 720.033)
    ..lineTo(784.214, 720.033)
    ..lineTo(784.214, 729.744)
    ..lineTo(725.061, 729.744)
    ..close()
    ..moveTo(716.275, 679.197)
    ..lineTo(796.217, 679.197)
    ..lineTo(796.217, 739.206)
    ..lineTo(778.026, 739.206)
    ..lineTo(778.026, 690.153)
    ..lineTo(733.6, 690.153)
    ..lineTo(733.6, 739.206)
    ..lineTo(716.275, 739.206)
    ..close()
    ..moveTo(749.688, 652.429)
    ..lineTo(768.621, 654.172)
    ..quadraticBezierTo(767.631, 660.024, 766.456, 665.688)
    ..quadraticBezierTo(765.28, 671.353, 764.166, 676.271)
    ..quadraticBezierTo(763.053, 681.188, 761.939, 684.923)
    ..lineTo(744.985, 682.931)
    ..quadraticBezierTo(746.099, 678.698, 747.027, 673.407)
    ..quadraticBezierTo(747.955, 668.116, 748.697, 662.638)
    ..quadraticBezierTo(749.44, 657.16, 749.688, 652.429)
    ..close()),
  BrandShape(Path()
    ..moveTo(823.814, 718.04)
    ..lineTo(935.808, 718.04)
    ..lineTo(935.808, 734.475)
    ..lineTo(823.814, 734.475)
    ..close()
    ..moveTo(870.467, 664.63)
    ..lineTo(889.649, 664.63)
    ..lineTo(889.649, 771.077)
    ..lineTo(870.467, 771.077)
    ..close()
    ..moveTo(866.013, 726.382)
    ..lineTo(880.862, 732.856)
    ..quadraticBezierTo(875.046, 740.45, 867.25, 747.236)
    ..quadraticBezierTo(859.454, 754.021, 850.42, 759.374)
    ..quadraticBezierTo(841.386, 764.728, 831.858, 768.214)
    ..quadraticBezierTo(830.496, 765.973, 828.392, 763.172)
    ..quadraticBezierTo(826.289, 760.37, 823.999, 757.694)
    ..quadraticBezierTo(821.71, 755.017, 819.73, 753.149)
    ..quadraticBezierTo(826.66, 751.158, 833.342, 748.356)
    ..quadraticBezierTo(840.025, 745.555, 846.151, 742.007)
    ..quadraticBezierTo(852.276, 738.458, 857.35, 734.537)
    ..quadraticBezierTo(862.424, 730.615, 866.013, 726.382)
    ..close()
    ..moveTo(892.371, 725.262)
    ..quadraticBezierTo(896.084, 729.37, 901.343, 733.354)
    ..quadraticBezierTo(906.602, 737.338, 912.852, 740.824)
    ..quadraticBezierTo(919.101, 744.31, 925.969, 747.173)
    ..quadraticBezierTo(932.838, 750.037, 939.644, 752.029)
    ..quadraticBezierTo(937.664, 753.772, 935.374, 756.511)
    ..quadraticBezierTo(933.085, 759.25, 930.981, 762.113)
    ..quadraticBezierTo(928.878, 764.977, 927.516, 767.342)
    ..quadraticBezierTo(920.586, 764.604, 913.718, 760.806)
    ..quadraticBezierTo(906.85, 757.009, 900.477, 752.402)
    ..quadraticBezierTo(894.104, 747.796, 888.473, 742.505)
    ..quadraticBezierTo(882.842, 737.214, 878.388, 731.486)
    ..close()
    ..moveTo(854.875, 692.269)
    ..lineTo(854.875, 697.498)
    ..lineTo(904.499, 697.498)
    ..lineTo(904.499, 692.269)
    ..close()
    ..moveTo(854.875, 673.345)
    ..lineTo(854.875, 678.449)
    ..lineTo(904.499, 678.449)
    ..lineTo(904.499, 673.345)
    ..close()
    ..moveTo(836.065, 658.405)
    ..lineTo(924.299, 658.405)
    ..lineTo(924.299, 712.438)
    ..lineTo(836.065, 712.438)
    ..close()),
  BrandShape(Path()
    ..moveTo(978.006, 712.064)
    ..lineTo(1028.744, 712.064)
    ..lineTo(1028.744, 724.514)
    ..lineTo(978.006, 724.514)
    ..close()
    ..moveTo(965.26, 745.43)
    ..lineTo(980.729, 740.575)
    ..quadraticBezierTo(982.214, 743.314, 983.884, 746.613)
    ..quadraticBezierTo(985.555, 749.912, 986.421, 752.154)
    ..lineTo(970.086, 757.631)
    ..quadraticBezierTo(969.467, 755.266, 968.044, 751.78)
    ..quadraticBezierTo(966.621, 748.294, 965.26, 745.43)
    ..close()
    ..moveTo(1024.908, 741.571)
    ..lineTo(1043.222, 744.434)
    ..quadraticBezierTo(1040.376, 749.788, 1037.53, 754.581)
    ..quadraticBezierTo(1034.684, 759.375, 1032.332, 762.861)
    ..lineTo(1017.977, 759.748)
    ..quadraticBezierTo(1019.339, 757.009, 1020.638, 753.834)
    ..quadraticBezierTo(1021.938, 750.659, 1023.051, 747.485)
    ..quadraticBezierTo(1024.165, 744.31, 1024.908, 741.571)
    ..close()
    ..moveTo(958.082, 728.374)
    ..lineTo(1050.029, 728.374)
    ..lineTo(1050.029, 741.571)
    ..lineTo(958.082, 741.571)
    ..close()
    ..moveTo(948.43, 753.896)
    ..lineTo(1057.701, 753.896)
    ..lineTo(1057.701, 768.463)
    ..lineTo(948.43, 768.463)
    ..close()
    ..moveTo(993.97, 716.795)
    ..lineTo(1012.409, 716.795)
    ..lineTo(1012.409, 762.238)
    ..lineTo(993.97, 762.238)
    ..close()
    ..moveTo(1014.884, 663.634)
    ..lineTo(1059.557, 663.634)
    ..lineTo(1059.557, 678.574)
    ..lineTo(1014.884, 678.574)
    ..close()
    ..moveTo(1011.419, 652.803)
    ..lineTo(1028.496, 656.662)
    ..quadraticBezierTo(1025.403, 666.871, 1020.081, 676.146)
    ..quadraticBezierTo(1014.76, 685.422, 1008.572, 691.398)
    ..quadraticBezierTo(1006.964, 689.904, 1004.241, 688.036)
    ..quadraticBezierTo(1001.519, 686.168, 998.734, 684.363)
    ..quadraticBezierTo(995.95, 682.558, 993.846, 681.438)
    ..quadraticBezierTo(999.91, 676.457, 1004.489, 668.801)
    ..quadraticBezierTo(1009.067, 661.144, 1011.419, 652.803)
    ..close()
    ..moveTo(1017.977, 685.67)
    ..lineTo(1028.125, 674.59)
    ..quadraticBezierTo(1031.714, 676.831, 1036.231, 679.757)
    ..quadraticBezierTo(1040.747, 682.683, 1045.079, 685.546)
    ..quadraticBezierTo(1049.41, 688.409, 1052.132, 690.65)
    ..lineTo(1041.366, 703.225)
    ..quadraticBezierTo(1038.891, 700.86, 1034.746, 697.809)
    ..quadraticBezierTo(1030.6, 694.759, 1026.145, 691.522)
    ..quadraticBezierTo(1021.69, 688.285, 1017.977, 685.67)
    ..close()
    ..moveTo(974.541, 653.052)
    ..lineTo(992.237, 653.052)
    ..lineTo(992.237, 699.615)
    ..lineTo(974.541, 699.615)
    ..close()
    ..moveTo(1001.643, 688.16)
    ..lineTo(1016.245, 695.63)
    ..quadraticBezierTo(1008.572, 703.101, 998.363, 708.641)
    ..quadraticBezierTo(988.154, 714.181, 976.459, 718.103)
    ..quadraticBezierTo(964.765, 722.024, 952.638, 724.639)
    ..quadraticBezierTo(951.276, 721.278, 948.616, 717.169)
    ..quadraticBezierTo(945.955, 713.061, 943.109, 710.072)
    ..quadraticBezierTo(954.246, 708.33, 965.384, 705.404)
    ..quadraticBezierTo(976.521, 702.478, 986.05, 698.183)
    ..quadraticBezierTo(995.579, 693.888, 1001.643, 688.16)
    ..close()
    ..moveTo(1010.058, 694.635)
    ..quadraticBezierTo(1014.389, 697.124, 1020.514, 699.428)
    ..quadraticBezierTo(1026.64, 701.731, 1033.941, 703.661)
    ..quadraticBezierTo(1041.243, 705.591, 1048.977, 707.084)
    ..quadraticBezierTo(1056.711, 708.578, 1064.26, 709.574)
    ..quadraticBezierTo(1062.528, 711.193, 1060.486, 713.745)
    ..quadraticBezierTo(1058.444, 716.298, 1056.649, 718.912)
    ..quadraticBezierTo(1054.855, 721.526, 1053.741, 723.518)
    ..quadraticBezierTo(1046.193, 722.149, 1038.396, 720.033)
    ..quadraticBezierTo(1030.6, 717.916, 1022.989, 715.177)
    ..quadraticBezierTo(1015.379, 712.438, 1008.572, 709.139)
    ..quadraticBezierTo(1001.766, 705.84, 996.197, 702.229)
    ..close()
    ..moveTo(950.658, 658.281)
    ..lineTo(967.982, 658.281)
    ..lineTo(967.982, 697)
    ..lineTo(950.658, 697)
    ..close()),
  BrandShape(Path()
    ..moveTo(1075.75, 651.25)
    ..lineTo(1153, 651.25)
    ..lineTo(1148.5, 673)
    ..lineTo(1123.75, 673)
    ..lineTo(1105, 754.75)
    ..lineTo(1087, 754.75)
    ..lineTo(1104.25, 673)
    ..lineTo(1071.25, 673)
    ..close()),
  BrandShape(Path()
    ..moveTo(1156.75, 651.25)
    ..lineTo(1180.75, 651.25)
    ..lineTo(1193.5, 720.25)
    ..lineTo(1226.5, 651.25)
    ..lineTo(1251.25, 651.25)
    ..lineTo(1205.5, 754.75)
    ..lineTo(1179.25, 754.75)
    ..close()),
  BrandShape(Path()
    ..moveTo(719.5, 811.75)
    ..cubicTo(852.25, 761.5, 1036.75, 760, 1198, 807.25)
    ..cubicTo(1036.75, 769.75, 856, 769.75, 719.5, 811.75)
    ..close(),
    color: Color(0xFFF20B1B)),
  BrandShape(Path()
    ..moveTo(720.25, 815.5)
    ..cubicTo(858.25, 774.25, 1032.25, 772.75, 1193.5, 809.5),
    strokeColor: Color(0xFFFFC3C0), strokeWidth: 2.25,
    opacity: 0.75),
  BrandShape(Path()
    ..addOval(Rect.fromLTRB(929, 864, 991, 926)),
    strokeColor: Color(0xFFF6CECE), strokeWidth: 6),
  BrandShape(Path()
    ..moveTo(960, 864),
    strokeColor: Color(0xFFED0614), strokeWidth: 6),
];

/// brandIcon 的画布尺寸（取自 SVG viewBox）。
const brandIconSize = Size(1024, 1024);

/// brandIcon 内容的实际包围盒，用于居中缩放。
const brandIconBounds = Rect.fromLTRB(0, 0, 1024, 1024);

List<BrandShape> brandIconShapes() => <BrandShape>[
  BrandShape(Path()
    ..moveTo(0, 0)
    ..lineTo(1024, 0)
    ..lineTo(1024, 1024)
    ..lineTo(0, 1024)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(397, 166)
    ..cubicTo(397, 111, 419, 85, 456, 93)
    ..cubicTo(468, 95, 478, 100, 489, 106)
    ..lineTo(775, 276)
    ..cubicTo(807, 295, 824, 324, 824, 358)
    ..cubicTo(824, 394, 805, 427, 775, 445)
    ..lineTo(700, 491)
    ..cubicTo(749, 452, 754, 398, 712, 365)
    ..cubicTo(706, 360, 697, 355, 689, 350)
    ..lineTo(397, 166)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFAA000A), Color(0xFFD30012), Color(0xFFFF4647), Color(0xFFFF7770)], <double>[0, 0.3, 0.71, 1], 0.18, 0.05, 0.86, 0.9)),
  BrandShape(Path()
    ..moveTo(397, 526)
    ..lineTo(649, 376)
    ..cubicTo(667, 365, 675, 354, 666, 336)
    ..lineTo(703, 357)
    ..cubicTo(750, 384, 752, 444, 700, 480)
    ..lineTo(489, 602)
    ..cubicTo(442, 630, 397, 592, 397, 526)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFB90010), Color(0xFFD30918), Color(0xFFE92830), Color(0xFFFF5B55)], <double>[0, 0.4, 0.84, 1], 0.06, 0.25, 0.95, 0.8)),
  BrandShape(Path()
    ..moveTo(273, 247)
    ..cubicTo(273, 164, 328, 99, 408, 91)
    ..cubicTo(430, 88, 450, 91, 467, 97)
    ..cubicTo(424, 92, 397, 120, 397, 168)
    ..lineTo(397, 528)
    ..cubicTo(397, 573, 422, 608, 469, 614)
    ..cubicTo(361, 621, 273, 573, 273, 494)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFEC000A), Color(0xFFEE111D), Color(0xFFFF5B58), Color(0xFFFF8C83)], <double>[0, 0.46, 0.82, 1], 0.1, 0.65, 1, 0.1)),
  BrandShape(Path()
    ..moveTo(397, 168)
    ..lineTo(650, 319)
    ..cubicTo(672, 332, 674, 350, 653, 364)
    ..lineTo(397, 525)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(312, 177)
    ..quadraticBezierTo(313, 173, 322, 170)
    ..lineTo(353, 160)
    ..quadraticBezierTo(361, 158, 360, 166)
    ..lineTo(357, 193)
    ..quadraticBezierTo(357, 199, 350, 202)
    ..lineTo(316, 212)
    ..quadraticBezierTo(307, 214, 307, 206)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(307, 263)
    ..quadraticBezierTo(308, 257, 316, 255)
    ..lineTo(347, 247)
    ..quadraticBezierTo(356, 245, 356, 253)
    ..lineTo(356, 281)
    ..quadraticBezierTo(355, 288, 347, 291)
    ..lineTo(313, 298)
    ..quadraticBezierTo(304, 299, 304, 291)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(305, 345)
    ..quadraticBezierTo(306, 340, 313, 338)
    ..lineTo(347, 331)
    ..quadraticBezierTo(355, 329, 356, 337)
    ..lineTo(356, 364)
    ..quadraticBezierTo(355, 371, 347, 373)
    ..lineTo(313, 377)
    ..quadraticBezierTo(304, 378, 304, 370)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(306, 422)
    ..quadraticBezierTo(307, 417, 314, 416)
    ..lineTo(347, 411)
    ..quadraticBezierTo(356, 409, 356, 418)
    ..lineTo(356, 448)
    ..quadraticBezierTo(356, 455, 348, 456)
    ..lineTo(313, 459)
    ..quadraticBezierTo(304, 460, 304, 452)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(312, 501)
    ..lineTo(346, 501)
    ..quadraticBezierTo(354, 501, 356, 509)
    ..lineTo(359, 534)
    ..quadraticBezierTo(360, 542, 353, 543)
    ..lineTo(321, 540)
    ..quadraticBezierTo(313, 539, 311, 531)
    ..lineTo(307, 509)
    ..quadraticBezierTo(306, 502, 312, 501)
    ..close(),
    color: Color(0xFFFFFFFF)),
  BrandShape(Path()
    ..moveTo(398, 167)
    ..cubicTo(398, 112, 421, 87, 461, 96),
    strokeColor: Color(0xFFFFACA6), strokeWidth: 3,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(397, 526)
    ..cubicTo(397, 581, 419, 609, 466, 612),
    strokeColor: Color(0xFFFF847B), strokeWidth: 3,
    opacity: 0.55),
  BrandShape(Path()
    ..moveTo(247.905, 795.846)
    ..lineTo(262.92, 780.242)
    ..quadraticBezierTo(272.16, 782.732, 281.895, 785.969)
    ..quadraticBezierTo(291.63, 789.206, 300.293, 792.526)
    ..quadraticBezierTo(308.955, 795.846, 314.895, 798.834)
    ..lineTo(294.765, 814.936)
    ..quadraticBezierTo(289.485, 811.948, 281.895, 808.462)
    ..quadraticBezierTo(274.305, 804.976, 265.56, 801.656)
    ..quadraticBezierTo(256.815, 798.336, 247.905, 795.846)
    ..close()
    ..moveTo(164.745, 765.136)
    ..lineTo(312.585, 765.136)
    ..lineTo(312.585, 784.724)
    ..lineTo(164.745, 784.724)
    ..close()
    ..moveTo(168.87, 666.864)
    ..lineTo(308.625, 666.864)
    ..lineTo(308.625, 686.784)
    ..lineTo(168.87, 686.784)
    ..close()
    ..moveTo(209.955, 781.902)
    ..lineTo(229.425, 796.012)
    ..quadraticBezierTo(222.99, 799.83, 214.493, 803.482)
    ..quadraticBezierTo(205.995, 807.134, 196.92, 810.205)
    ..quadraticBezierTo(187.845, 813.276, 179.595, 815.102)
    ..quadraticBezierTo(176.625, 811.616, 171.923, 806.802)
    ..quadraticBezierTo(167.22, 801.988, 163.26, 798.668)
    ..quadraticBezierTo(171.675, 797.008, 180.502, 794.269)
    ..quadraticBezierTo(189.33, 791.53, 197.167, 788.21)
    ..quadraticBezierTo(205.005, 784.89, 209.955, 781.902)
    ..close()
    ..moveTo(197.415, 711.684)
    ..lineTo(276.285, 711.684)
    ..lineTo(276.285, 724.798)
    ..lineTo(197.415, 724.798)
    ..close()
    ..moveTo(197.415, 729.114)
    ..lineTo(276.285, 729.114)
    ..lineTo(276.285, 742.062)
    ..lineTo(197.415, 742.062)
    ..close()
    ..moveTo(197.415, 746.71)
    ..lineTo(276.285, 746.71)
    ..lineTo(276.285, 759.658)
    ..lineTo(197.415, 759.658)
    ..close()
    ..moveTo(185.7, 692.262)
    ..lineTo(292.29, 692.262)
    ..lineTo(292.29, 772.274)
    ..lineTo(268.035, 772.274)
    ..lineTo(268.035, 706.87)
    ..lineTo(208.8, 706.87)
    ..lineTo(208.8, 772.274)
    ..lineTo(185.7, 772.274)
    ..close()
    ..moveTo(230.25, 656.572)
    ..lineTo(255.495, 658.896)
    ..quadraticBezierTo(254.175, 666.698, 252.608, 674.251)
    ..quadraticBezierTo(251.04, 681.804, 249.555, 688.361)
    ..quadraticBezierTo(248.07, 694.918, 246.585, 699.898)
    ..lineTo(223.98, 697.242)
    ..quadraticBezierTo(225.465, 691.598, 226.702, 684.543)
    ..quadraticBezierTo(227.94, 677.488, 228.93, 670.184)
    ..quadraticBezierTo(229.92, 662.88, 230.25, 656.572)
    ..close()),
  BrandShape(Path()
    ..moveTo(329.085, 744.054)
    ..lineTo(478.41, 744.054)
    ..lineTo(478.41, 765.966)
    ..lineTo(329.085, 765.966)
    ..close()
    ..moveTo(391.29, 672.84)
    ..lineTo(416.865, 672.84)
    ..lineTo(416.865, 814.77)
    ..lineTo(391.29, 814.77)
    ..close()
    ..moveTo(385.35, 755.176)
    ..lineTo(405.15, 763.808)
    ..quadraticBezierTo(397.395, 773.934, 387, 782.981)
    ..quadraticBezierTo(376.605, 792.028, 364.56, 799.166)
    ..quadraticBezierTo(352.515, 806.304, 339.81, 810.952)
    ..quadraticBezierTo(337.995, 807.964, 335.19, 804.229)
    ..quadraticBezierTo(332.385, 800.494, 329.332, 796.925)
    ..quadraticBezierTo(326.28, 793.356, 323.64, 790.866)
    ..quadraticBezierTo(332.88, 788.21, 341.79, 784.475)
    ..quadraticBezierTo(350.7, 780.74, 358.868, 776.009)
    ..quadraticBezierTo(367.035, 771.278, 373.8, 766.049)
    ..quadraticBezierTo(380.565, 760.82, 385.35, 755.176)
    ..close()
    ..moveTo(420.495, 753.682)
    ..quadraticBezierTo(425.445, 759.16, 432.457, 764.472)
    ..quadraticBezierTo(439.47, 769.784, 447.803, 774.432)
    ..quadraticBezierTo(456.135, 779.08, 465.293, 782.898)
    ..quadraticBezierTo(474.45, 786.716, 483.525, 789.372)
    ..quadraticBezierTo(480.885, 791.696, 477.832, 795.348)
    ..quadraticBezierTo(474.78, 799, 471.975, 802.818)
    ..quadraticBezierTo(469.17, 806.636, 467.355, 809.79)
    ..quadraticBezierTo(458.115, 806.138, 448.957, 801.075)
    ..quadraticBezierTo(439.8, 796.012, 431.303, 789.87)
    ..quadraticBezierTo(422.805, 783.728, 415.298, 776.673)
    ..quadraticBezierTo(407.79, 769.618, 401.85, 761.982)
    ..close()
    ..moveTo(370.5, 709.692)
    ..lineTo(370.5, 716.664)
    ..lineTo(436.665, 716.664)
    ..lineTo(436.665, 709.692)
    ..close()
    ..moveTo(370.5, 684.46)
    ..lineTo(370.5, 691.266)
    ..lineTo(436.665, 691.266)
    ..lineTo(436.665, 684.46)
    ..close()
    ..moveTo(345.42, 664.54)
    ..lineTo(463.065, 664.54)
    ..lineTo(463.065, 736.584)
    ..lineTo(345.42, 736.584)
    ..close()),
  BrandShape(Path()
    ..moveTo(534.675, 736.086)
    ..lineTo(602.325, 736.086)
    ..lineTo(602.325, 752.686)
    ..lineTo(534.675, 752.686)
    ..close()
    ..moveTo(517.68, 780.574)
    ..lineTo(538.305, 774.1)
    ..quadraticBezierTo(540.285, 777.752, 542.513, 782.151)
    ..quadraticBezierTo(544.74, 786.55, 545.895, 789.538)
    ..lineTo(524.115, 796.842)
    ..quadraticBezierTo(523.29, 793.688, 521.392, 789.04)
    ..quadraticBezierTo(519.495, 784.392, 517.68, 780.574)
    ..close()
    ..moveTo(597.21, 775.428)
    ..lineTo(621.63, 779.246)
    ..quadraticBezierTo(617.835, 786.384, 614.04, 792.775)
    ..quadraticBezierTo(610.245, 799.166, 607.11, 803.814)
    ..lineTo(587.97, 799.664)
    ..quadraticBezierTo(589.785, 796.012, 591.517, 791.779)
    ..quadraticBezierTo(593.25, 787.546, 594.735, 783.313)
    ..quadraticBezierTo(596.22, 779.08, 597.21, 775.428)
    ..close()
    ..moveTo(508.11, 757.832)
    ..lineTo(630.705, 757.832)
    ..lineTo(630.705, 775.428)
    ..lineTo(508.11, 775.428)
    ..close()
    ..moveTo(495.24, 791.862)
    ..lineTo(640.935, 791.862)
    ..lineTo(640.935, 811.284)
    ..lineTo(495.24, 811.284)
    ..close()
    ..moveTo(555.96, 742.394)
    ..lineTo(580.545, 742.394)
    ..lineTo(580.545, 802.984)
    ..lineTo(555.96, 802.984)
    ..close()
    ..moveTo(583.845, 671.512)
    ..lineTo(643.41, 671.512)
    ..lineTo(643.41, 691.432)
    ..lineTo(583.845, 691.432)
    ..close()
    ..moveTo(579.225, 657.07)
    ..lineTo(601.995, 662.216)
    ..quadraticBezierTo(597.87, 675.828, 590.775, 688.195)
    ..quadraticBezierTo(583.68, 700.562, 575.43, 708.53)
    ..quadraticBezierTo(573.285, 706.538, 569.655, 704.048)
    ..quadraticBezierTo(566.025, 701.558, 562.312, 699.151)
    ..quadraticBezierTo(558.6, 696.744, 555.795, 695.25)
    ..quadraticBezierTo(563.88, 688.61, 569.985, 678.401)
    ..quadraticBezierTo(576.09, 668.192, 579.225, 657.07)
    ..close()
    ..moveTo(587.97, 700.894)
    ..lineTo(601.5, 686.12)
    ..quadraticBezierTo(606.285, 689.108, 612.308, 693.009)
    ..quadraticBezierTo(618.33, 696.91, 624.105, 700.728)
    ..quadraticBezierTo(629.88, 704.546, 633.51, 707.534)
    ..lineTo(619.155, 724.3)
    ..quadraticBezierTo(615.855, 721.146, 610.327, 717.079)
    ..quadraticBezierTo(604.8, 713.012, 598.86, 708.696)
    ..quadraticBezierTo(592.92, 704.38, 587.97, 700.894)
    ..close()
    ..moveTo(530.055, 657.402)
    ..lineTo(553.65, 657.402)
    ..lineTo(553.65, 719.486)
    ..lineTo(530.055, 719.486)
    ..close()
    ..moveTo(566.19, 704.214)
    ..lineTo(585.66, 714.174)
    ..quadraticBezierTo(575.43, 724.134, 561.818, 731.521)
    ..quadraticBezierTo(548.205, 738.908, 532.612, 744.137)
    ..quadraticBezierTo(517.02, 749.366, 500.85, 752.852)
    ..quadraticBezierTo(499.035, 748.37, 495.488, 742.892)
    ..quadraticBezierTo(491.94, 737.414, 488.145, 733.43)
    ..quadraticBezierTo(502.995, 731.106, 517.845, 727.205)
    ..quadraticBezierTo(532.695, 723.304, 545.4, 717.577)
    ..quadraticBezierTo(558.105, 711.85, 566.19, 704.214)
    ..close()
    ..moveTo(577.41, 712.846)
    ..quadraticBezierTo(583.185, 716.166, 591.352, 719.237)
    ..quadraticBezierTo(599.52, 722.308, 609.255, 724.881)
    ..quadraticBezierTo(618.99, 727.454, 629.303, 729.446)
    ..quadraticBezierTo(639.615, 731.438, 649.68, 732.766)
    ..quadraticBezierTo(647.37, 734.924, 644.648, 738.327)
    ..quadraticBezierTo(641.925, 741.73, 639.533, 745.216)
    ..quadraticBezierTo(637.14, 748.702, 635.655, 751.358)
    ..quadraticBezierTo(625.59, 749.532, 615.195, 746.71)
    ..quadraticBezierTo(604.8, 743.888, 594.652, 740.236)
    ..quadraticBezierTo(584.505, 736.584, 575.43, 732.185)
    ..quadraticBezierTo(566.355, 727.786, 558.93, 722.972)
    ..close()
    ..moveTo(498.21, 664.374)
    ..lineTo(521.31, 664.374)
    ..lineTo(521.31, 716)
    ..lineTo(498.21, 716)
    ..close()),
  BrandShape(Path()
    ..moveTo(665, 655)
    ..lineTo(768, 655)
    ..lineTo(762, 684)
    ..lineTo(729, 684)
    ..lineTo(704, 793)
    ..lineTo(680, 793)
    ..lineTo(703, 684)
    ..lineTo(659, 684)
    ..close()),
  BrandShape(Path()
    ..moveTo(773, 655)
    ..lineTo(805, 655)
    ..lineTo(822, 747)
    ..lineTo(866, 655)
    ..lineTo(899, 655)
    ..lineTo(838, 793)
    ..lineTo(803, 793)
    ..close()),
  BrandShape(Path()
    ..moveTo(190, 869)
    ..cubicTo(367, 802, 613, 800, 828, 863)
    ..cubicTo(613, 813, 372, 813, 190, 869)
    ..close(),
    color: Color(0xFFF20B1B)),
  BrandShape(Path()
    ..moveTo(191, 874)
    ..cubicTo(375, 819, 607, 817, 822, 866),
    strokeColor: Color(0xFFFFC3C0), strokeWidth: 3,
    opacity: 0.75),
  BrandShape(Path()
    ..moveTo(6, 737)
    ..cubicTo(113, 875, 248, 970, 425, 987)
    ..cubicTo(640, 1013, 794, 832, 1024, 802)
    ..lineTo(1024, 1024)
    ..lineTo(6, 1024)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFFF6A64), Color(0xFFFFB9B7), Color(0xFFFFE6E6)], <double>[0, 0.5, 1], 0, 0, 1, 1),
    opacity: 0.85),
  BrandShape(Path()
    ..moveTo(6, 872)
    ..cubicTo(170, 959, 306, 1012, 469, 1008)
    ..cubicTo(649, 1003, 788, 871, 1024, 890)
    ..lineTo(1024, 1024)
    ..lineTo(6, 1024)
    ..close(),
    gradient: BrandGradient.linear(<Color>[Color(0xFFE1000A), Color(0xFFED0A15), Color(0xFFFF5354)], <double>[0, 0.55, 1], 0, 1, 1, 0)),
  BrandShape(Path()
    ..moveTo(6, 900)
    ..cubicTo(164, 995, 303, 1023, 469, 1011)
    ..cubicTo(276, 1004, 115, 912, 6, 737)
    ..close(),
    color: Color(0xFFFF544F),
    opacity: 0.8),
];
