import 'package:duanju_app/updater.dart';
import 'package:flutter_test/flutter_test.dart';

/// 远程升级的变体选择（0.8.18）。
///
/// 这块逻辑错了的后果不是"功能不好用"，而是**装不上**：
/// 通用包的 versionCode 带 +4000 偏移（4096），拿 arm64 包（2096）去覆盖它
/// 会被系统判成版本降级，安装器直接拒绝。
void main() {
  UpdateRelease releaseWith(List<String> names) {
    return UpdateRelease(
      version: '0.8.18',
      build: 97,
      tag: 'v0.8.18',
      notes: '',
      assets: [
        for (final name in names)
          UpdateAsset(
            abi: _abi(name),
            name: name,
            size: 1,
            sha256: 'x',
          ),
      ],
    );
  }

  test('装了通用包就必须继续给通用包', () {
    final release = releaseWith(const [
      'zhenguojianTV-0.8.18+97-all.apk',
      'zhenguojianTV-0.8.18+97-arm64-v8a.apk',
    ]);
    final asset = Updater.pickAsset(
      release,
      versionCode: 4096,
      abi: 'arm64-v8a',
    );
    expect(asset?.abi, 'all');
  });

  test('单 ABI 包按设备架构挑', () {
    final release = releaseWith(const [
      'zhenguojianTV-0.8.18+97-all.apk',
      'zhenguojianTV-0.8.18+97-arm64-v8a.apk',
      'zhenguojianTV-0.8.18+97-armeabi-v7a.apk',
    ]);
    expect(
      Updater.pickAsset(release, versionCode: 2096, abi: 'arm64-v8a')?.abi,
      'arm64-v8a',
    );
    expect(
      Updater.pickAsset(release, versionCode: 1096, abi: 'armeabi-v7a')?.abi,
      'armeabi-v7a',
    );
  });

  test('arm64 设备缺 64 位包时退回 32 位包', () {
    final release = releaseWith(const [
      'zhenguojianTV-0.8.18+97-armeabi-v7a.apk',
    ]);
    expect(
      Updater.pickAsset(release, versionCode: 1096, abi: 'arm64-v8a')?.abi,
      'armeabi-v7a',
    );
  });

  test('没有匹配的变体时返回 null，不瞎推一个', () {
    final release = releaseWith(const [
      'zhenguojianTV-0.8.18+97-x86.apk',
    ]);
    expect(
      Updater.pickAsset(release, versionCode: 2096, abi: 'arm64-v8a'),
      isNull,
    );
  });

  test('内置镜像都是 https 且带 {url} 占位符', () {
    expect(Updater.defaultMirrors, isNotEmpty);
    for (final mirror in Updater.defaultMirrors) {
      expect(mirror.startsWith('https://'), isTrue, reason: mirror);
      expect(mirror.contains('{url}'), isTrue, reason: mirror);
    }
  });
}

String _abi(String name) {
  final lower = name.toLowerCase();
  for (final abi in const ['arm64-v8a', 'armeabi-v7a', 'x86_64', 'x86']) {
    if (lower.endsWith('-$abi.apk')) return abi;
  }
  return 'all';
}
