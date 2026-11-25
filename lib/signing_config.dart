import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as path;

/// 红果取流签名档热更新（0.8.35）。
///
/// 上游会不定期切换它接受的播放签名档；过去每次都靠发版改一个常量。这里让 App
/// 启动时拉一份极小的远程配置（复用升级用的 jsDelivr 通道），注入原生核心，
/// **无需重新发版**即可切换签名档。
///
/// 设计要点：
/// - 远程配置不可达 / 格式坏 / 校验失败 → 一律忽略，继续用内置默认档（零风险）。
/// - 拉到配置后写本地缓存；离线时先注入上次成功配置，再尝试联网刷新。
/// - 配置内容只描述"用哪一档 / 档参数"，不含任何代码或凭据；原生侧强校验。
class SigningConfig {
  const SigningConfig._();

  static const repo = 'ai0by/zhenguojianTV';
  static const configPath = 'config/hongguo-signing.json';
  static const cacheFile = 'hongguo-signing-cache.json';
  static const userAgent = 'zhenguojianTV-signing';

  /// 拉取源，按顺序回退。国内 GitHub 直连经常不可达，jsDelivr 的两个入口
  /// （Cloudflare / Fastly）和 gcore 通常至少有一个能用；手机与 TV 走同一套。
  static const sources = <String>[
    'https://cdn.jsdelivr.net/gh/$repo@main/$configPath',
    'https://fastly.jsdelivr.net/gh/$repo@main/$configPath',
    'https://gcore.jsdelivr.net/gh/$repo@main/$configPath',
    'https://raw.githubusercontent.com/$repo/main/$configPath',
  ];

  /// 最近一次成功拉取的来源（诊断用，进程内有效）。
  static String? lastSource;

  /// 最近一次成功拉取的时间（诊断用）。
  static DateTime? lastFetched;

  /// 最近一次失败原因摘要（诊断用）。
  static String? lastError;

  /// 配置 JSON 里当前生效档的键。整份配置直接透传给原生，原生按
  /// `{"profile": <档名或档对象>}` 约定的 `profile` 字段读取。
  static const profileKey = 'profile';

  /// 启动时调用。返回是否成功注入了配置（默认档也算成功）。
  ///
  /// [inject] 接收原始配置 JSON 字符串（`signingProfile` 字段内容），由调用方
  /// 通过 native `_call({'action':'configureHongguoSigning', ...})` 注入。
  static Future<bool> bootstrap({
    required String supportDir,
    required Future<void> Function(Object? profile) inject,
  }) async {
    final cachePath = path.join(supportDir, cacheFile);

    // 1) 先注入本地缓存（离线可用）。
    var cached = await _readCache(cachePath);
    if (cached != null && cached[profileKey] != null) {
      try {
        await inject(cached[profileKey]);
      } catch (_) {
        cached = null; // 缓存里的档被原生判为无效，忽略它。
      }
    }

    // 2) 联网刷新（失败不影响启动）。
    final remote = await _fetchAny();
    return await applyFromConfig(remote, cachePath, inject) || cached != null;
  }

  /// 手动「立即检查」：跳过缓存，只走联网，成功即写缓存并生效。
  ///
  /// 返回 null 表示成功（已注入），否则返回可展示的失败原因。
  static Future<String?> refresh({
    required String supportDir,
    required Future<void> Function(Object? profile) inject,
  }) async {
    final cachePath = path.join(supportDir, cacheFile);
    final remote = await _fetchAny();
    if (remote == null) {
      return lastError ?? '无法连接更新源';
    }
    final applied = await applyFromConfig(remote, cachePath, inject);
    return applied ? null : '配置格式无效，已忽略';
  }

  /// 把一份配置应用并写缓存。返回是否成功注入。纯逻辑，便于测试。
  static Future<bool> applyFromConfig(
    Map<String, dynamic>? config,
    String cachePath,
    Future<void> Function(Object? profile) inject,
  ) async {
    if (config == null || config[profileKey] == null) return false;
    try {
      await inject(config[profileKey]);
      await _writeCache(cachePath, config);
      return true;
    } catch (_) {
      return false;
    }
  }

  static Future<Map<String, dynamic>?> _readCache(String cachePath) async {
    try {
      final file = File(cachePath);
      if (!await file.exists()) return null;
      final decoded = jsonDecode(await file.readAsString());
      if (decoded is Map) return Map<String, dynamic>.from(decoded);
    } catch (_) {}
    return null;
  }

  static Future<void> _writeCache(
    String cachePath,
    Map<String, dynamic> config,
  ) async {
    try {
      await File(cachePath).writeAsString(jsonEncode(config));
    } catch (_) {}
  }

  /// 依次尝试所有源，第一个解析成功的即采用。
  static Future<Map<String, dynamic>?> _fetchAny() async {
    final failed = <String>[];
    for (final url in sources) {
      final got = await _fetch(url);
      if (got != null) {
        lastSource = url;
        lastFetched = DateTime.now();
        lastError = null;
        return got;
      }
      failed.add(Uri.parse(url).host);
    }
    lastError = failed.isEmpty ? '无可用更新源' : '更新源均不可达（${failed.join('、')}）';
    return null;
  }

  /// 当前签名配置状态摘要（给设置页显示）。
  static Map<String, String> statusSummary() {
    final out = <String, String>{};
    if (lastSource != null) out['来源'] = Uri.parse(lastSource!).host;
    if (lastFetched != null) {
      final t = lastFetched!.toLocal();
      out['检查时间'] =
          '${t.month}-${t.day} ${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
    }
    if (lastError != null) out['状态'] = lastError!;
    return out;
  }

  static Future<Map<String, dynamic>?> _fetch(String url) async {
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 5);
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, userAgent);
      request.headers.set(HttpHeaders.acceptHeader, 'application/json');
      final response = await request.close().timeout(const Duration(seconds: 6));
      if (response.statusCode != 200) {
        await response.drain<void>();
        return null;
      }
      final text = await response
          .transform(utf8.decoder)
          .join()
          .timeout(const Duration(seconds: 6));
      final decoded = jsonDecode(text);
      if (decoded is Map && decoded.length <= 64) {
        return Map<String, dynamic>.from(decoded);
      }
    } catch (_) {}
    finally {
      client.close(force: true);
    }
    return null;
  }
}
