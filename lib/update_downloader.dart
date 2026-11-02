import 'dart:io';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';

import 'updater.dart';

/// 安装包下载器（0.8.20）。
///
/// 为什么单独抽出来：0.8.18 的下载逻辑写在 `UpdateEntry` 的 State 里，
/// 用户一关「关于」对话框 State 就 dispose，`await` 回来 `!mounted` 直接
/// return —— 下载链路跟着断，下次还得从头再来。用户明确抱怨过这一点。
///
/// 现在下载挂在全局单例上，UI 只是订阅者：
/// - 关掉对话框照样下，重新打开能看到当前进度；
/// - 下完了再打开，直接给「安装」按钮。
///
/// 顺带解决另外两个问题：
/// - **节点择优**：先给所有镜像各发一个 64 KB 的 Range 请求测速，从快的开始
///   用，而不是"第一个能连上就一直用"（那个可能只有几十 KB/s）。
/// - **分片并发**：支持 `Range` 的镜像切成 4 段同时下。单连接往往跑不满带宽，
///   四个镜像/四条连接并行才有明显收益；镜像不认 Range 就自动退回单连接。
///
/// 还有两个看门狗，专门对付国内加速站的常见毛病：
/// - **卡死**：连续 20 秒一个字节都没收到，判这条连接死了，换节点。
/// - **太慢**：12 秒窗口内平均低于 12 KB/s，主动放弃换节点 —— 比"一直转圈
///   转到天亮"有用。
class UpdateDownloader extends ChangeNotifier {
  UpdateDownloader._();

  /// 全局唯一实例：下载不跟着任何页面的生命周期走。
  static final UpdateDownloader instance = UpdateDownloader._();

  /// 分片数。4 段在手机上足够跑满家用带宽，再多只会被镜像限速。
  static const shardCount = 4;

  /// 小于这个体积不分片：开 4 条连接的开销比省下来的时间还多。
  static const _minShardBytes = 4 << 20;

  static const _probeBytes = 64 << 10;
  static const _probeTimeout = Duration(seconds: 6);
  static const _connectTimeout = Duration(seconds: 12);

  /// 连续这么久没收到任何字节 → 判定连接已死。
  static const _stallTimeout = Duration(seconds: 20);

  /// 低速判定的观察窗口与阈值。
  static const _speedWindow = Duration(seconds: 12);
  static const _minBytesPerSecond = 12 << 10;

  UpdateDownloadStage stage = UpdateDownloadStage.idle;
  String version = '';
  UpdateAsset? asset;
  File? file;
  String error = '';
  String mirror = '';
  int received = 0;
  int total = 0;
  int bytesPerSecond = 0;

  /// 每次 start/cancel 递增，用来让上一轮的异步回调安静退出。
  int _generation = 0;
  bool _cancelled = false;
  DateTime _lastNotify = DateTime.fromMillisecondsSinceEpoch(0);

  bool get busy =>
      stage == UpdateDownloadStage.probing ||
      stage == UpdateDownloadStage.downloading ||
      stage == UpdateDownloadStage.verifying;

  double get progress =>
      total > 0 ? (received / total).clamp(0.0, 1.0) : 0.0;

  /// 开始（或继续）下载。已经在下载时重复调用会被忽略。
  Future<void> start({
    required UpdateRelease release,
    required UpdateAsset asset,
    required List<String> mirrors,
  }) async {
    if (busy) return;
    final generation = ++_generation;
    _cancelled = false;
    version = release.version;
    this.asset = asset;
    file = null;
    error = '';
    mirror = '';
    received = 0;
    total = asset.size;
    bytesPerSecond = 0;
    _emit(UpdateDownloadStage.probing);

    try {
      final urls = Updater.mirrorURLs(
        release: release,
        asset: asset,
        mirrors: mirrors,
      );
      if (urls.isEmpty) throw const UpdateException('没有可用的下载地址');

      final ranked = await _rank(urls);
      if (_aborted(generation)) return;
      if (ranked.isEmpty) {
        throw const UpdateException('所有下载节点都连不上，请检查网络后重试');
      }

      final directory = await getTemporaryDirectory();
      final target = File('${directory.path}/update-${release.version}.apk');
      final work = Directory('${directory.path}/update-${release.version}.shards');

      if (total <= 0) total = await _resolveSize(ranked.first);
      if (_aborted(generation)) return;
      _emit(UpdateDownloadStage.downloading);

      if (total <= 0) {
        // 连总大小都问不出来，只能单连接硬下，靠 sha256 兜底。
        await _downloadWhole(ranked, target, generation);
      } else {
        await work.create(recursive: true);
        try {
          await _downloadSharded(ranked, work, target, total, generation);
        } on UpdateException catch (failure) {
          // 所有节点都不认 Range（`206` 拿不到）：分片拼不起来，退回单连接整包下。
          if (!failure.message.contains('不支持分片下载')) rethrow;
          await _deleteQuietly(work);
          await _downloadWhole(ranked, target, generation);
        }
      }
      if (_aborted(generation)) return;
      await _deleteQuietly(work);
      file = target;
      bytesPerSecond = 0;
      _emit(UpdateDownloadStage.ready);
    } on _Cancelled {
      // 用户主动取消：保留已下的分片，下次接着下。
    } on UpdateException catch (failure) {
      if (!_aborted(generation)) _fail(failure.message);
    } catch (_) {
      if (!_aborted(generation)) _fail('下载失败，请稍后再试');
    }
  }

  /// 取消当前下载。已下载的分片留在磁盘上，下次 [start] 会接着下。
  void cancel() {
    if (!busy) return;
    _cancelled = true;
    _generation++;
    bytesPerSecond = 0;
    _emit(UpdateDownloadStage.idle);
  }

  /// 回到初始态（安装成功后调用）。已下载的文件不删，留着手动安装也能用。
  void reset() {
    if (busy) cancel();
    stage = UpdateDownloadStage.idle;
    file = null;
    error = '';
    mirror = '';
    received = 0;
    total = 0;
    bytesPerSecond = 0;
    _notify(force: true);
  }

  // —— 节点测速 ——

  /// 给每个镜像发一个小请求测吞吐，按从快到慢排序，连不上的直接剔掉。
  Future<List<String>> _rank(List<String> urls) async {
    final speeds = await Future.wait(urls.map(_probe));
    final scored = <MapEntry<String, int>>[];
    for (var i = 0; i < urls.length; i++) {
      if (speeds[i] > 0) scored.add(MapEntry(urls[i], speeds[i]));
    }
    scored.sort((a, b) => b.value.compareTo(a.value));
    return [for (final entry in scored) entry.key];
  }

  /// 返回估算吞吐（字节/秒），失败返回 0。
  ///
  /// 测的是"前 64 KB 用了多久"，而不是单纯的 TCP 握手时间 —— 加速站普遍
  /// 握手很快但限速很狠，只看 RTT 会挑到最慢的那个。
  Future<int> _probe(String url) async {
    final client = HttpClient()..connectionTimeout = _connectTimeout;
    final watch = Stopwatch()..start();
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, Updater.userAgent);
      request.headers.set(
        HttpHeaders.rangeHeader,
        'bytes=0-${_probeBytes - 1}',
      );
      final response = await request.close().timeout(_probeTimeout);
      if (response.statusCode != 200 && response.statusCode != 206) {
        await response.drain<void>();
        return 0;
      }
      var bytes = 0;
      await for (final chunk in response.timeout(_probeTimeout)) {
        bytes += chunk.length;
        if (bytes >= _probeBytes) break;
      }
      final elapsed = watch.elapsedMilliseconds;
      if (bytes <= 0 || elapsed <= 0) return 0;
      return bytes * 1000 ~/ elapsed;
    } catch (_) {
      return 0;
    } finally {
      client.close(force: true);
    }
  }

  /// 用 `Content-Range: bytes 0-0/12345` 问出文件总大小，问不出来返回 0。
  Future<int> _resolveSize(String url) async {
    final client = HttpClient()..connectionTimeout = _connectTimeout;
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, Updater.userAgent);
      request.headers.set(HttpHeaders.rangeHeader, 'bytes=0-0');
      final response = await request.close().timeout(_probeTimeout);
      final range =
          response.headers.value(HttpHeaders.contentRangeHeader) ?? '';
      await response.drain<void>();
      final match = RegExp(r'/(\d+)\s*$').firstMatch(range);
      return int.tryParse(match?.group(1) ?? '') ?? 0;
    } catch (_) {
      return 0;
    } finally {
      client.close(force: true);
    }
  }

  // —— 分片下载 ——

  Future<void> _downloadSharded(
    List<String> urls,
    Directory work,
    File target,
    int size,
    int generation,
  ) async {
    final count = size < _minShardBytes ? 1 : shardCount;
    final shardSize = (size + count - 1) ~/ count;
    final shards = <_Shard>[];
    for (var i = 0; i < count; i++) {
      final start = i * shardSize;
      final end = math.min(size - 1, start + shardSize - 1);
      if (start > end) break;
      shards.add(
        _Shard(
          start: start,
          end: end,
          file: File('${work.path}/shard-$i'),
        ),
      );
    }
    // 续传：先把已经躺在磁盘上的字节算进进度，界面才不会从 0 开始跳。
    for (final shard in shards) {
      shard.done = await shard.file.exists()
          ? math.min(await shard.file.length(), shard.span)
          : 0;
    }
    received = shards.fold(0, (sum, shard) => sum + shard.done);
    _notify(force: true);

    final session = _Session(generation: generation, urls: urls);
    try {
      await Future.wait([
        for (final shard in shards) _fetchShard(shard, session),
      ]);
    } finally {
      // 任意一片失败都要让其他片停下，否则它们会继续吃流量。
      session.stopped = true;
    }
    await _assemble(shards, target, generation);
  }

  Future<void> _fetchShard(_Shard shard, _Session session) async {
    if (shard.done >= shard.span) return;
    UpdateException? lastError;
    for (final url in session.urls) {
      if (session.stopped || _aborted(session.generation)) return;
      try {
        await _fetchRange(url, shard, session);
        return;
      } on _Cancelled {
        rethrow;
      } on UpdateException catch (failure) {
        lastError = failure;
      } catch (_) {
        lastError = const UpdateException('下载中断');
      }
    }
    session.stopped = true;
    throw lastError ?? const UpdateException('下载失败，请稍后再试');
  }

  /// 下 `[shard.start + done, shard.end]` 这一段，追加到分片文件末尾。
  Future<void> _fetchRange(String url, _Shard shard, _Session session) async {
    if (shard.done >= shard.span) return;
    final from = shard.start + shard.done;
    final client = HttpClient()..connectionTimeout = _connectTimeout;
    IOSink? sink;
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, Updater.userAgent);
      request.headers.set(
        HttpHeaders.rangeHeader,
        'bytes=$from-${shard.end}',
      );
      final response = await request.close().timeout(_connectTimeout);
      final ignoredRange = response.statusCode == 200;
      if (!ignoredRange && response.statusCode != 206) {
        await response.drain<void>();
        throw UpdateException('节点返回 ${response.statusCode}');
      }
      if (ignoredRange) {
        // 服务器忽略了 Range，从 0 开始给整份文件。只有"这一片正好就是整个
        // 文件"时才能用；真正的分片模式下拼不起来，换下一个节点。
        if (shard.start != 0 || shard.done != 0 || shard.span != total) {
          await response.drain<void>();
          throw const UpdateException('节点不支持分片下载');
        }
      }
      _switchMirror(url);
      sink = shard.file.openWrite(
        mode: ignoredRange ? FileMode.write : FileMode.append,
      );
      await _pump(
        response,
        sink,
        stopped: () => session.stopped || _aborted(session.generation),
        onBytes: (bytes) {
          shard.done += bytes;
          received += bytes;
        },
      );
      await sink.flush();
    } finally {
      await sink?.close();
      client.close(force: true);
    }
    if (shard.done < shard.span) {
      throw UpdateException('下载不完整（${shard.done}/${shard.span} 字节）');
    }
  }

  /// 合并分片 → 校验 sha256 → 落成正式文件名。
  Future<void> _assemble(
    List<_Shard> shards,
    File target,
    int generation,
  ) async {
    if (_aborted(generation)) throw const _Cancelled();
    _emit(UpdateDownloadStage.verifying);
    final part = File('${target.path}.part');
    final sink = part.openWrite();
    try {
      for (final shard in shards) {
        if (_aborted(generation)) throw const _Cancelled();
        await sink.addStream(shard.file.openRead());
      }
      await sink.flush();
    } finally {
      await sink.close();
    }

    final expected = (asset?.sha256 ?? '').toLowerCase();
    if (expected.isNotEmpty) {
      final digest = await Updater.sha256OfFile(part);
      if (digest != expected) {
        // 校验失败说明文件本身有问题，留着重下没意义，直接丢。
        await _deleteQuietly(part);
        for (final shard in shards) {
          await _deleteQuietly(shard.file);
        }
        throw const UpdateException('安装包校验失败（sha256 不匹配），已丢弃');
      }
    }
    if (await target.exists()) await target.delete();
    await part.rename(target.path);
    for (final shard in shards) {
      await _deleteQuietly(shard.file);
    }
  }

  // —— 单连接兜底（总大小未知时） ——

  Future<void> _downloadWhole(
    List<String> urls,
    File target,
    int generation,
  ) async {
    final part = File('${target.path}.part');
    var offset = await part.exists() ? await part.length() : 0;
    UpdateException? lastError;
    for (final url in urls) {
      if (_aborted(generation)) throw const _Cancelled();
      try {
        await _fetchWhole(url, part, offset, generation);
        final expected = (asset?.sha256 ?? '').toLowerCase();
        if (expected.isNotEmpty) {
          final digest = await Updater.sha256OfFile(part);
          if (digest != expected) {
            await _deleteQuietly(part);
            throw const UpdateException('安装包校验失败（sha256 不匹配），已丢弃');
          }
        }
        if (await target.exists()) await target.delete();
        await part.rename(target.path);
        return;
      } on _Cancelled {
        rethrow;
      } on UpdateException catch (failure) {
        if (failure.message.contains('校验失败')) rethrow;
        lastError = failure;
        offset = await part.exists() ? await part.length() : 0;
      } catch (_) {
        lastError = const UpdateException('下载失败，请稍后再试');
        offset = await part.exists() ? await part.length() : 0;
      }
    }
    throw lastError ?? const UpdateException('下载失败，请稍后再试');
  }

  Future<void> _fetchWhole(
    String url,
    File part,
    int offset,
    int generation,
  ) async {
    final client = HttpClient()..connectionTimeout = _connectTimeout;
    IOSink? sink;
    try {
      final request = await client.getUrl(Uri.parse(url));
      request.headers.set(HttpHeaders.userAgentHeader, Updater.userAgent);
      if (offset > 0) {
        request.headers.set(HttpHeaders.rangeHeader, 'bytes=$offset-');
      }
      final response = await request.close().timeout(_connectTimeout);
      if (response.statusCode != 200 && response.statusCode != 206) {
        await response.drain<void>();
        throw UpdateException('节点返回 ${response.statusCode}');
      }
      final resumed = response.statusCode == 206 && offset > 0;
      _switchMirror(url);
      received = resumed ? offset : 0;
      sink = part.openWrite(mode: resumed ? FileMode.append : FileMode.write);
      await _pump(
        response,
        sink,
        stopped: () => _aborted(generation),
        onBytes: (bytes) => received += bytes,
      );
      await sink.flush();
    } finally {
      await sink?.close();
      client.close(force: true);
    }
  }

  // —— 公共泵：卡死与低速看门狗 ——

  Future<void> _pump(
    HttpClientResponse response,
    IOSink sink, {
    required bool Function() stopped,
    required void Function(int bytes) onBytes,
  }) async {
    var windowBytes = 0;
    final window = Stopwatch()..start();
    await for (final chunk in response.timeout(_stallTimeout)) {
      if (stopped()) throw const _Cancelled();
      sink.add(chunk);
      onBytes(chunk.length);
      windowBytes += chunk.length;
      if (window.elapsed >= _speedWindow) {
        final speed = windowBytes * 1000 / window.elapsedMilliseconds;
        bytesPerSecond = speed.round();
        if (speed < _minBytesPerSecond) {
          throw const UpdateException('节点太慢，已换下一个');
        }
        window.reset();
        windowBytes = 0;
      }
      _notify();
    }
  }

  // —— 杂项 ——

  void _switchMirror(String url) {
    if (mirror == url) return;
    mirror = url;
    _notify(force: true);
  }

  bool _aborted(int generation) => _cancelled || generation != _generation;

  void _emit(UpdateDownloadStage next) {
    stage = next;
    _notify(force: true);
  }

  void _fail(String message) {
    error = message;
    bytesPerSecond = 0;
    _emit(UpdateDownloadStage.failed);
  }

  /// 节流通知：一个 30 MB 的包会回调上千次，无条件 notify 会把 UI 拖死。
  void _notify({bool force = false}) {
    final now = DateTime.now();
    if (!force && now.difference(_lastNotify).inMilliseconds < 200) return;
    _lastNotify = now;
    notifyListeners();
  }

  Future<void> _deleteQuietly(FileSystemEntity entity) async {
    try {
      if (await entity.exists()) await entity.delete(recursive: true);
    } catch (_) {
      // 删不掉就算了，下次写入会覆盖。
    }
  }
}

enum UpdateDownloadStage { idle, probing, downloading, verifying, ready, failed }

class _Shard {
  _Shard({required this.start, required this.end, required this.file});

  final int start;
  final int end;
  final File file;

  /// 已写入的字节数（含上一次续传留下的）。
  int done = 0;

  int get span => end - start + 1;
}

class _Session {
  _Session({required this.generation, required this.urls});

  final int generation;
  final List<String> urls;

  /// 任意一片彻底失败后置位，其他片据此收手。
  bool stopped = false;
}

class _Cancelled implements Exception {
  const _Cancelled();
}
