import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

/// 当前网络的粗略类型（0.7.3，`XQ-0107`）。
///
/// 只区分"是不是移动网络"，不追求精确分类：
/// - `wifi`：Wi-Fi 或有线；
/// - `mobile`：蜂窝网络（流量）；
/// - `unknown`：无法判断（非 Android、通道不可用/超时、VPN 等）。
///
/// `unknown` 按"允许自动播放"处理：宁可多播一次，
/// 也不要因为判断失败让用户在 Wi-Fi 下也得点一下（体感是"坏了"）。
enum NetworkKind { wifi, mobile, unknown }

/// 读取当前网络类型。
///
/// 走 `MainActivity` 已有的 `duanju/device` 通道（`networkType`），
/// **不引入任何第三方依赖**。
class NetworkState {
  const NetworkState._();

  static const channel = MethodChannel('duanju/device');

  /// 通道调用超时（0.7.3）。
  ///
  /// **必须有**：在 Flutter 单元测试里，没有 mock 的 MethodChannel 调用
  /// 不会抛异常，而是**永远不返回**（Future 挂起）。若 `_prepare()` 里
  /// 无超时地 await 它，整个视频加载也会被挂死（实测：预加载测试直接超时）。
  /// 真机上是毫秒级返回，这个超时只是保险。
  static const _timeout = Duration(milliseconds: 800);

  /// 结果缓存时长（0.8.19）。
  ///
  /// 换集时每集都要判一次"是不是移动网络"，而 `ConnectivityManager` 查询
  /// 要走一次平台通道往返。以前每集都现查，等于每次切集都在 `open()`
  /// 之前白等一个往返；缓存 15 秒后连续切集基本零开销。
  /// 网络真的变了最多迟 15 秒生效，对"要不要自动播放"这种判断完全够用。
  static const Duration _ttl = Duration(seconds: 15);

  static NetworkKind? _cached;
  static DateTime? _cachedAt;

  /// 正在进行的查询（0.8.19）。
  ///
  /// 预热和 `_prepare()` 里的实际判断几乎同时发生，不合并的话会并发两次
  /// 平台通道往返。合并之后两者共享同一个 Future。
  static Future<NetworkKind>? _pending;

  /// 预热缓存（0.8.19）：进播放页时先发一次，切集时就不用等。
  static void warmUp() {
    if (_cached != null && _fresh) return;
    unawaited(current());
  }

  static bool get _fresh {
    final at = _cachedAt;
    return _cached != null && at != null && DateTime.now().difference(at) < _ttl;
  }

  /// 清掉缓存（测试用；也供将来"网络切换广播"接入）。
  static void resetCache() {
    _cached = null;
    _cachedAt = null;
    _pending = null;
  }

  static Future<NetworkKind> current() {
    if (_fresh) return Future.value(_cached!);
    final pending = _pending;
    if (pending != null) return pending;
    final future = _read();
    _pending = future;
    return future.whenComplete(() {
      if (identical(_pending, future)) _pending = null;
    });
  }

  static Future<NetworkKind> _read() async {
    if (defaultTargetPlatform != TargetPlatform.android) {
      _cached = NetworkKind.unknown;
      _cachedAt = DateTime.now();
      return NetworkKind.unknown;
    }
    try {
      final type = await channel
          .invokeMethod<String>('networkType')
          .timeout(_timeout, onTimeout: () => null);
      final kind = switch (type) {
        'wifi' || 'ethernet' => NetworkKind.wifi,
        'mobile' => NetworkKind.mobile,
        _ => NetworkKind.unknown,
      };
      _cached = kind;
      _cachedAt = DateTime.now();
      return kind;
    } on PlatformException {
      return NetworkKind.unknown;
    } on MissingPluginException {
      return NetworkKind.unknown;
    }
  }
}
