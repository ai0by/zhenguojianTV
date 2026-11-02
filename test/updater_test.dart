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

  // 0.8.20：同一个 tag 下会连发 +99、+100、+101。
  //
  // 只比 semver 的话，装在 +99 上的用户永远看不到 +100 —— 这一版就永远
  // 发不出去。所以 semver 相同时必须比 build 号。
  test('同版本号下 build 变大也算有更新', () {
    expect(
      Updater.isNewer('0.8.20', '0.8.20', candidateBuild: 100, currentBuild: 99),
      isTrue,
    );
    expect(
      Updater.isNewer('0.8.20', '0.8.20', candidateBuild: 99, currentBuild: 99),
      isFalse,
    );
    expect(
      Updater.isNewer('0.8.20', '0.8.20', candidateBuild: 98, currentBuild: 99),
      isFalse,
    );
    // build 取不到（0）时退化成只比 semver，绝不能因为"0 > 0"就提示有更新。
    expect(Updater.isNewer('0.8.20', '0.8.20'), isFalse);
    // semver 更高时 build 再小也是更新。
    expect(
      Updater.isNewer('0.8.21', '0.8.20', candidateBuild: 1, currentBuild: 999),
      isTrue,
    );
    expect(
      Updater.isNewer('0.8.19', '0.8.20', candidateBuild: 999, currentBuild: 1),
      isFalse,
    );
  });

  // versionCode = build + 1000 的整数倍（armv7 分包 +1000、arm64 分包 +2000、
  // 通用包再加 4000），所以取模 1000 就能拿回 build。
  test('从已安装的 versionCode 反推 build 号', () {
    expect(Updater.buildFromVersionCode(99), 99);
    expect(Updater.buildFromVersionCode(1099), 99);
    expect(Updater.buildFromVersionCode(2099), 99);
    expect(Updater.buildFromVersionCode(4099), 99);
    expect(Updater.buildFromVersionCode(0), 0);
  });

  // 下载器拿到的是一组"已展开、已去重"的地址，它自己不再管模板语法。
  test('镜像模板展开成下载地址时会去重并丢掉非 https', () {
    final release = UpdateRelease(
      version: '0.8.20',
      build: 99,
      tag: 'v0.8.20',
      notes: '',
      assets: const [],
    );
    final asset = UpdateAsset(
      abi: 'all',
      name: 'zhenguojianTV-0.8.20+99-all.apk',
      size: 1,
      sha256: 'x',
    );
    final urls = Updater.mirrorURLs(
      release: release,
      asset: asset,
      mirrors: const [
        'https://gh-proxy.org/{url}',
        'https://gh-proxy.org/{url}',
        'http://insecure.example/{url}',
        'https://other.example/{url_q}',
      ],
    );
    expect(urls.length, 2);
    final base =
        'https://github.com/ai0by/zhenguojianTV/releases/download/v0.8.20/'
        'zhenguojianTV-0.8.20%2B99-all.apk';
    // 文件名里的 `+` 必须编码成 `%2B`，否则服务端按字面量找不到文件。
    expect(urls.first, 'https://gh-proxy.org/$base');
    expect(urls[1], 'https://other.example/${Uri.encodeComponent(base)}');
  });
}

String _abi(String name) {
  final lower = name.toLowerCase();
  for (final abi in const ['arm64-v8a', 'armeabi-v7a', 'x86_64', 'x86']) {
    if (lower.endsWith('-$abi.apk')) return abi;
  }
  return 'all';
}
