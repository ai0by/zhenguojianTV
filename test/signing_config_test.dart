import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:duanju_app/signing_config.dart';

void main() {
  late Directory tmp;

  setUp(() {
    tmp = Directory.systemTemp.createTempSync('signcfg');
  });
  tearDown(() {
    try {
      tmp.deleteSync(recursive: true);
    } catch (_) {}
  });

  test('applyFromConfig 注入档名并写缓存', () async {
    Object? injected;
    final cache = path.join(tmp.path, 'c.json');
    final ok = await SigningConfig.applyFromConfig(
      {'profile': 'v04.06.04', 'note': 'test'},
      cache,
      (p) async => injected = p,
    );
    expect(ok, true);
    expect(injected, 'v04.06.04');
    final cached = jsonDecode(File(cache).readAsStringSync());
    expect(cached['profile'], 'v04.06.04');
  });

  test('applyFromConfig 支持完整档对象', () async {
    Object? injected;
    final profile = {'name': 'custom', 'medusa21': 312, 'deviceLevel': 1};
    final ok = await SigningConfig.applyFromConfig(
      {'profile': profile},
      path.join(tmp.path, 'c.json'),
      (p) async => injected = p,
    );
    expect(ok, true);
    expect(injected, isA<Map>());
    expect((injected as Map)['name'], 'custom');
  });

  test('注入失败时不写缓存', () async {
    final cache = path.join(tmp.path, 'c.json');
    final ok = await SigningConfig.applyFromConfig(
      {'profile': 'bad'},
      cache,
      (p) async => throw StateError('rejected'),
    );
    expect(ok, false);
    expect(File(cache).existsSync(), false);
  });

  test('空配置或无 profile 键返回 false', () async {
    expect(
      await SigningConfig.applyFromConfig(null, 'x', (p) async {}),
      false,
    );
    expect(
      await SigningConfig.applyFromConfig({'note': 'x'}, 'x', (p) async {}),
      false,
    );
  });
}
