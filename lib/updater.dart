import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';

/// 远程升级（0.8.18）。
///
/// 只在「关于真果鉴TV」里点按钮时检查 —— **没有**启动自动检查，也**没有**
/// 任何弹窗提醒。用户不点就永远不发一个请求。
///
/// 三个环节：
/// 1. 检查：先问 `api.github.com`（实时、带附件 sha256），失败再退回 jsDelivr
///    上的 `config/update.json`（国内更稳，但边缘缓存 12 小时，可能滞后半天）。
/// 2. 下载：GitHub 的 release 资产在国内常常连不上，走镜像前缀链
///    （`https://gh-proxy.org/{url}` 这种形式），失败就换下一个，支持断点续传。
/// 3. 校验：**sha256 必须对上 GitHub 给的 digest** 才允许安装。镜像站理论上
///    可以替换文件，digest 是唯一与镜像无关的凭据。
///
/// 安装交给原生（`installApk`）：FileProvider + 系统安装器。
class Updater {
  const Updater._();

  static const repo = 'ai0by/zhenguojianTV';
  static const channel = MethodChannel('duanju/device');

  static const _apiURL = 'https://api.github.com/repos/$repo/releases/latest';
  static const _configURL =
      'https://cdn.jsdelivr.net/gh/ai0by/zhenguojianTV@main/config/update.json';

  /// 内置镜像（0.8.18 实测排序：gh-proxy.org 最快且支持真实续传）。
  ///
  /// 远程 `config/update.json` 里的 `mirrors` 会覆盖它 —— 加速站死得很快，
  /// 写死在客户端里意味着每次换站都要发一个版本。
  static const defaultMirrors = <String>[
    'https://gh-proxy.org/{url}',
    'https://v6.gh-proxy.org/{url}',
    'https://ghproxy.net/{url}',
    'https://v4.gh-proxy.org/{url}',
    'https://gh-proxy.com/{url}',
  ];

  static const userAgent = 'zhenguojianTV-updater';

  /// 检查更新。[currentVersion] 是当前版本号（如 `0.8.17`）。
  ///
  /// 返回 null 表示「已是最新」；抛 [UpdateException] 表示检查失败。
  ///
  /// 0.8.20 起**同版本也要比 build 号**：tag 只有一个（`v0.8.20`），同一个
  /// tag 下可以连着发 `+99`、`+100`、`+101`。只比 semver 的话，装在 +99 上
  /// 的用户永远看不到 +100，等于这一版永远发不出去。
  static Future<UpdateCheckResult?> check({
    required String currentVersion,
  }) async {
    final config = await _getJSONObject(_configURL, const Duration(seconds: 8));
    final mirrors = _mirrorsFromConfig(config);
    final release = await _fetchRelease(config);
    if (release == null) {
      throw const UpdateException('检查更新失败，请稍后再试');
    }
    final environment = await environment();
    if (!isNewer(
      release.version,
      currentVersion,
      candidateBuild: release.build,
      currentBuild: buildFromVersionCode(environment.versionCode),
    )) {
      return null;
    }
    return UpdateCheckResult(release: release, mirrors: mirrors);
  }

  /// 从已安装 APK 的 versionCode 反推 build 号。
  ///
  /// versionCode = build + 1000 的整数倍（Flutter 分包的 arm64/armv7 偏移是
  /// 2000/1000，通用包再加 `universalVersionCodeOffset` = 4000），所以取模
  /// 1000 就能拿回 build 本身：99 / 2099 / 4099 都是 99。
  ///
  /// 取不到（0）就返回 0，调用方据此退化成"只比 semver"，不会误判成降级。
  static int buildFromVersionCode(int versionCode) {
    if (versionCode <= 0) return 0;
    return versionCode % 1000;
  }

  /// 候选版本是否比当前新。semver 相同时才比 build（0 表示未知，不参与）。
  static bool isNewer(
    String candidate,
    String current, {
    int candidateBuild = 0,
    int currentBuild = 0,
  }) {
    final a = _versionParts(candidate);
    final b = _versionParts(current);
    for (var i = 0; i < 3; i++) {
      if (a[i] != b[i]) return a[i] > b[i];
    }
    if (candidateBuild <= 0 || currentBuild <= 0) return false;
    return candidateBuild > currentBuild;
  }

  /// 挑一个适合当前设备的安装包。
  ///
  /// 通用包的 versionCode 比单 ABI 包高 4000（见 `build.gradle.kts` 的
  /// `universalVersionCodeOffset`），所以**装的是通用包就必须继续给通用包**：
  /// 拿 arm64 包（2096）去覆盖通用包（4096）会被系统判成版本降级，直接拒装。
  static UpdateAsset? pickAsset(
    UpdateRelease release, {
    required int versionCode,
    required String abi,
  }) {
    UpdateAsset? universal;
    UpdateAsset? exact;
    UpdateAsset? fallback;
    for (final asset in release.assets) {
      if (asset.isUniversal) {
        universal ??= asset;
      } else if (asset.abi == abi) {
        exact ??= asset;
      } else if (asset.abi == 'armeabi-v7a' && abi == 'arm64-v8a') {
        // arm64 设备能跑 32 位包，反过来不行。
        fallback ??= asset;
      }
    }
    if (versionCode >= 4000) return universal ?? exact ?? fallback;
    return exact ?? fallback ?? universal;
  }

  /// 把镜像模板展开成一组真实下载地址（去重、只留 https）。
  ///
  /// 0.8.20 起下载逻辑搬到了 `lib/update_downloader.dart`：它要先给这些地址
  /// 测速排个序，再决定从哪个开始下、能不能分片并发。这里只负责"地址怎么拼"。
  static List<String> mirrorURLs({
    required UpdateRelease release,
    required UpdateAsset asset,
    required List<String> mirrors,
  }) {
    final base = asset.url.isNotEmpty ? asset.url : _assetURL(release, asset);
    final out = <String>[];
    for (final template in mirrors) {
      final url = _applyTemplate(template, base);
      if (url == null || out.contains(url)) continue;
      out.add(url);
    }
    return out;
  }

  /// 交给系统安装器。返回是否成功唤起。
  static Future<bool> install(String path) async {
    try {
      final ok = await channel
          .invokeMethod<bool>('installApk', {'path': path})
          .timeout(const Duration(seconds: 5), onTimeout: () => false);
      return ok ?? false;
    } on PlatformException {
      return false;
    } on MissingPluginException {
      return false;
    }
  }

  /// 是否已允许「安装未知应用」。
  static Future<bool> canInstall() async {
    try {
      final ok = await channel
          .invokeMethod<bool>('canInstallApk')
          .timeout(const Duration(seconds: 3), onTimeout: () => false);
      return ok ?? false;
    } on PlatformException {
      return false;
    } on MissingPluginException {
      return false;
    }
  }

  /// 跳到「安装未知应用」授权页。
  static Future<void> openInstallSettings() async {
    try {
      await channel
          .invokeMethod<void>('openInstallSettings')
          .timeout(const Duration(seconds: 3), onTimeout: () {});
    } on PlatformException {
      // 忽略：个别 ROM 没有这个页面。
    } on MissingPluginException {
      // 忽略。
    }
  }

  /// 当前 versionCode 与首选 ABI，用来挑变体。
  static Future<UpdateEnvironment> environment() async {
    try {
      final value = await channel
          .invokeMethod<Map<Object?, Object?>>('updateEnvironment')
          .timeout(const Duration(seconds: 3), onTimeout: () => null);
      return UpdateEnvironment(
        versionCode: (value?['versionCode'] as num?)?.toInt() ?? 0,
        abi: value?['abi'] as String? ?? '',
      );
    } on PlatformException {
      return const UpdateEnvironment(versionCode: 0, abi: '');
    } on MissingPluginException {
      return const UpdateEnvironment(versionCode: 0, abi: '');
    }
  }

  // —— 内部实现 ——

  /// GitHub 上的原始下载地址。文件名里的 `+`（`0.8.17+96`）必须编码成
  /// `%2B`，否则服务端按字面量找不到文件；这里手工替换而不用 `Uri.https`，
  /// 因为路径里的 `+` 是合法字符，Uri 不会替我们编码。
  static String _assetURL(UpdateRelease release, UpdateAsset asset) {
    final name = asset.name.replaceAll('+', '%2B');
    return 'https://github.com/$repo/releases/download/${release.tag}/$name';
  }

  /// 把镜像模板展开成真实地址。`{url}` 保留原样（路径前缀式），
  /// `{url_q}` 做一次百分号编码（查询参数式）。
  static String? _applyTemplate(String template, String base) {
    var out = template;
    if (out.contains('{url_q}')) {
      out = out.replaceAll('{url_q}', Uri.encodeComponent(base));
    }
    if (out.contains('{url}')) {
      out = out.replaceAll('{url}', base);
    }
    if (!out.startsWith('https://')) return null;
    return out;
  }

  static List<String> _mirrorsFromConfig(Map<String, dynamic>? config) {
    final list = config?['mirrors'];
    if (list is! List) return defaultMirrors;
    final out = <String>[];
    for (final item in list) {
      if (item is String && item.contains('{url') && item.length <= 200) {
        out.add(item);
      }
      if (out.length >= 8) break;
    }
    return out.isEmpty ? defaultMirrors : out;
  }

  /// 文件 sha256（十六进制小写）。下载器校验安装包用。
  static Future<String> sha256OfFile(File file) async {
    final sink = _DigestSink();
    final input = sha256.startChunkedConversion(sink);
    await for (final chunk in file.openRead()) {
      input.add(chunk);
    }
    input.close();
    return (sink.value ?? sha256.convert(const <int>[])).toString();
  }

  static Future<UpdateRelease?> _fetchRelease(
    Map<String, dynamic>? config,
  ) async {
    final remote = await _getJSONObject(_apiURL, const Duration(seconds: 10));
    if (remote != null) {
      final parsed = _parseGitHubRelease(remote);
      if (parsed != null) return parsed;
    }
    final latest = config?['latest'];
    if (latest is Map) {
      return _parseConfigRelease(_asStringMap(latest));
    }
    return null;
  }

  static UpdateRelease? _parseGitHubRelease(Map<String, dynamic> json) {
    final tag = (json['tag_name'] as String? ?? '').trim();
    if (tag.isEmpty) return null;
    final version = tag.replaceFirst(RegExp(r'^[vV]'), '');
    final assets = <UpdateAsset>[];
    var build = 0;
    final rawAssets = json['assets'];
    if (rawAssets is List) {
      for (final item in rawAssets) {
        if (item is! Map) continue;
        final map = _asStringMap(item);
        final name = map['name'] as String? ?? '';
        final abi = _abiFromName(name);
        if (abi.isEmpty) continue;
        if (build == 0) build = _buildFromName(name);
        final digest = map['digest'] as String? ?? '';
        assets.add(
          UpdateAsset(
            abi: abi,
            name: name,
            size: (map['size'] as num?)?.toInt() ?? 0,
            sha256: digest.startsWith('sha256:')
                ? digest.substring(7)
                : digest,
            url: map['browser_download_url'] as String? ?? '',
          ),
        );
      }
    }
    if (assets.isEmpty) return null;
    return UpdateRelease(
      version: version,
      build: build,
      tag: tag,
      notes: (json['body'] as String? ?? '').trim(),
      assets: assets,
    );
  }

  static UpdateRelease? _parseConfigRelease(Map<String, dynamic> json) {
    final version = (json['version'] as String? ?? '').trim();
    if (version.isEmpty) return null;
    final assets = <UpdateAsset>[];
    final rawAssets = json['assets'];
    if (rawAssets is List) {
      for (final item in rawAssets) {
        if (item is! Map) continue;
        final map = _asStringMap(item);
        final name = map['name'] as String? ?? '';
        final abi = (map['abi'] as String? ?? '').trim();
        if (name.isEmpty || abi.isEmpty) continue;
        assets.add(
          UpdateAsset(
            abi: abi,
            name: name,
            size: (map['size'] as num?)?.toInt() ?? 0,
            sha256: (map['sha256'] as String? ?? '').toLowerCase(),
          ),
        );
      }
    }
    if (assets.isEmpty) return null;
    return UpdateRelease(
      version: version,
      build: (json['build'] as num?)?.toInt() ?? 0,
      tag: (json['tag'] as String? ?? 'v$version').trim(),
      notes: (json['notes'] as String? ?? '').trim(),
      assets: assets,
    );
  }

  /// `zhenguojianTV-0.8.17+96-arm64-v8a.apk` → `arm64-v8a`，通用包 → `all`。
  static String _abiFromName(String name) {
    final lower = name.toLowerCase();
    if (!lower.endsWith('.apk')) return '';
    final trimmed = lower.substring(0, lower.length - 4);
    for (final abi in const ['arm64-v8a', 'armeabi-v7a', 'x86_64', 'x86']) {
      if (trimmed.endsWith('-$abi')) return abi;
    }
    if (trimmed.endsWith('-all')) return 'all';
    return '';
  }

  /// 文件名里的 build 号（`+96`）；通用包的 versionCode 带 +4000 偏移，
  /// 所以**不能用 versionCode 反推**。
  static int _buildFromName(String name) {
    final match = RegExp(r'\+(\d+)').firstMatch(name);
    if (match == null) return 0;
    return int.tryParse(match.group(1) ?? '') ?? 0;
  }

  /// 版本比较只看 semver（0.8.17 > 0.8.16），build 号的处理见 [isNewer]。
  static List<int> _versionParts(String raw) {
    final out = <int>[0, 0, 0];
    final parts = raw.trim().replaceFirst(RegExp(r'^[vV]'), '').split('.');
    for (var i = 0; i < 3 && i < parts.length; i++) {
      final match = RegExp(r'^\d+').firstMatch(parts[i]);
      if (match != null) out[i] = int.tryParse(match.group(0) ?? '') ?? 0;
    }
    return out;
  }

  static Map<String, dynamic> _asStringMap(Map<dynamic, dynamic> raw) {
    final out = <String, dynamic>{};
    raw.forEach((key, value) {
      out['$key'] = value;
    });
    return out;
  }

  static Future<Object?> _getJSON(String url, Duration timeout) async {
    final client = HttpClient()
      ..connectionTimeout = const Duration(seconds: 8);
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, userAgent);
      request.headers.set(HttpHeaders.acceptHeader, 'application/json');
      final response = await request.close().timeout(timeout);
      if (response.statusCode != 200) {
        await response.drain<void>();
        return null;
      }
      final text = await response
          .transform(utf8.decoder)
          .join()
          .timeout(timeout);
      return jsonDecode(text);
    } catch (_) {
      return null;
    } finally {
      client.close(force: true);
    }
  }

  static Future<Map<String, dynamic>?> _getJSONObject(
    String url,
    Duration timeout,
  ) async {
    final decoded = await _getJSON(url, timeout);
    if (decoded is Map) return _asStringMap(decoded);
    return null;
  }
}

/// 一次检查的结果：新版本信息 + 可用镜像。
class UpdateCheckResult {
  const UpdateCheckResult({required this.release, required this.mirrors});

  final UpdateRelease release;
  final List<String> mirrors;
}

/// 一个可下载的安装包。
class UpdateAsset {
  const UpdateAsset({
    required this.abi,
    required this.name,
    required this.size,
    required this.sha256,
    this.url = '',
  });

  final String abi;
  final String name;
  final int size;
  final String sha256;
  final String url;

  bool get isUniversal => abi == 'all';
}

/// 一次 Release 的信息。
class UpdateRelease {
  const UpdateRelease({
    required this.version,
    required this.build,
    required this.tag,
    required this.notes,
    required this.assets,
  });

  final String version;
  final int build;
  final String tag;
  final String notes;
  final List<UpdateAsset> assets;
}

/// 设备侧信息，用来挑变体。
class UpdateEnvironment {
  const UpdateEnvironment({required this.versionCode, required this.abi});

  final int versionCode;
  final String abi;
}

class UpdateException implements Exception {
  const UpdateException(this.message);

  final String message;

  @override
  String toString() => message;
}

class _DigestSink implements Sink<Digest> {
  Digest? value;

  @override
  void add(Digest data) => value = data;

  @override
  void close() {}
}
