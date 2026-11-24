import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:duanju_app/app_layout.dart';
import 'package:duanju_app/app_splash.dart';
import 'package:duanju_app/tv_splash.dart';

void main() {
  testWidgets('TV 开屏渲染自绘深色开屏，不再用浅色位图', (tester) async {
    final ready = ValueNotifier<bool>(false);
    await tester.pumpWidget(
      AppLayout(
        television: true,
        child: MaterialApp(
          home: AppSplash(ready: ready, child: const SizedBox.expand()),
        ),
      ),
    );

    // TV 自绘开屏在树上：应用名 + 标语 + 明确的加载提示。
    expect(find.byType(TvSplashSurface), findsOneWidget);
    expect(find.text('正在准备片库…'), findsOneWidget);
    expect(find.text('好片随心看'), findsOneWidget);
  });

  testWidgets('手机开屏仍用成品位图，不渲染 TV 自绘开屏', (tester) async {
    final ready = ValueNotifier<bool>(false);
    await tester.pumpWidget(
      AppLayout(
        television: false,
        child: MaterialApp(
          home: AppSplash(ready: ready, child: const SizedBox.expand()),
        ),
      ),
    );

    expect(find.byType(TvSplashSurface), findsNothing);
    expect(find.text('正在准备片库…'), findsNothing);
  });
}
