import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

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

  static const _userAgent = 'zhenguojianTV-updater';

  /// 检查更新。[currentVersion] 是当前版本号（如 `0.8.17`）。
  ///
  /// 返回 null 表示「已是最新」；抛 [UpdateException] 表示检查失败。
  static Future<UpdateCheckResult?> check({
    required String currentVersion,
  }) async {
    final config = await _getJSONObject(_configURL, const Duration(seconds: 8));
    final mirrors = _mirrorsFromConfig(config);
    final release = await _fetchRelease(config);
    if (release == null) {
      throw const UpdateException('检查更新失败，请稍后再试');
    }
    if (!_isNewer(release.version, currentVersion)) return null;
    return UpdateCheckResult(release: release, mirrors: mirrors);
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

  /// 下载并校验。[onProgress] 回调（已下载字节, 总字节）。
  ///
  /// 镜像按顺序尝试，任意一个成功即返回；全部失败抛 [UpdateException]。
  /// 支持断点续传：`.part` 已存在时带 `Range` 续传，服务端不认（返回 200）
  /// 就重头写。
  static Future<File> download({
    required UpdateRelease release,
    required UpdateAsset asset,
    required List<String> mirrors,
    void Function(int received, int total)? onProgress,
  }) async {
    final directory = await getTemporaryDirectory();
    final target = File('${directory.path}/update-${release.version}.apk');
    final part = File('${target.path}.part');
    final base = asset.url.isNotEmpty ? asset.url : _assetURL(release, asset);

    UpdateException? lastError;
    for (final template in mirrors) {
      final url = _applyTemplate(template, base);
      if (url == null) continue;
      try {
        await _downloadOne(url, part, asset.size, onProgress);
        final digest = await _sha256Of(part);
        if (asset.sha256.isNotEmpty && digest != asset.sha256.toLowerCase()) {
          await _deleteQuietly(part);
          throw const UpdateException('安装包校验失败（sha256 不匹配），已丢弃');
        }
        if (await target.exists()) await target.delete();
        await part.rename(target.path);
        return target;
      } on UpdateException catch (error) {
        // 校验失败说明文件本身有问题，换镜像重试没有意义。
        if (error.message.contains('校验失败')) rethrow;
        // 其他失败**保留 .part**：下一个镜像可以带 Range 接着下，
        // 这正是断点续传的意义（镜像 A 中途断了，不用从头再来）。
        lastError = error;
      } catch (_) {
        lastError = const UpdateException('下载失败，请稍后再试');
      }
    }
    throw lastError ?? const UpdateException('下载失败，请稍后再试');
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

  static Future<void> _downloadOne(
    String url,
    File part,
    int expected,
    void Function(int received, int total)? onProgress,
  ) async {
    final client = HttpClient()
      ..connectionTimeout = const Duration(seconds: 15);
    try {
      var offset = 0;
      if (await part.exists()) {
        offset = await part.length();
        // 已经下完了就别再请求一次。
        if (expected > 0 && offset == expected) {
          onProgress?.call(offset, expected);
          return;
        }
        // 明显超过预期说明是坏文件，重来。
        if (expected > 0 && offset > expected) {
          await _deleteQuietly(part);
          offset = 0;
        }
      }
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, _userAgent);
      if (offset > 0) {
        request.headers.set(HttpHeaders.rangeHeader, 'bytes=$offset-');
      }
      final response = await request.close();
      if (response.statusCode != 200 && response.statusCode != 206) {
        await response.drain<void>();
        throw UpdateException('镜像返回 ${response.statusCode}');
      }
      final resumed = response.statusCode == 206 && offset > 0;
      final total = resumed
          ? offset + (response.contentLength > 0 ? response.contentLength : 0)
          : response.contentLength;
      final sink = part.openWrite(
        mode: resumed ? FileMode.append : FileMode.write,
      );
      var received = resumed ? offset : 0;
      try {
        await for (final chunk in response) {
          sink.add(chunk);
          received += chunk.length;
          onProgress?.call(received, total > 0 ? total : expected);
        }
      } finally {
        await sink.flush();
        await sink.close();
      }
      if (expected > 0 && received != expected) {
        throw UpdateException('下载不完整（$received/$expected 字节）');
      }
    } finally {
      client.close(force: true);
    }
  }

  static Future<void> _deleteQuietly(File file) async {
    try {
      if (await file.exists()) await file.delete();
    } catch (_) {
      // 删不掉就算了，下次写入会覆盖。
    }
  }

  static Future<String> _sha256Of(File file) async {
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

  /// 版本比较只看 semver（0.8.17 > 0.8.16）。build 号不参与决策：
  /// 通用包的 versionCode 带 +4000 偏移，跟文件名里的 build 号对不上，
  /// 掺进来只会算错。
  static bool _isNewer(String candidate, String current) {
    final a = _versionParts(candidate);
    final b = _versionParts(current);
    for (var i = 0; i < 3; i++) {
      if (a[i] != b[i]) return a[i] > b[i];
    }
    return false;
  }

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
      request.headers.set(HttpHeaders.userAgentHeader, _userAgent);
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
