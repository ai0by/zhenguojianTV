/// 播放预缓存偏好（0.8.12）。
///
/// 用户口径："观看时应该支持预缓存 2 集，应该支持配置预缓存集数，
/// 支持自动清理缓存避免越增越大。"
///
/// 所以这里有两个旋钮：
///   - [episodes]：播这一集时，往后预缓存几集（0 表示关闭）；
///   - [limitMB]：磁盘缓存的总容量上限，超了按 LRU 自动回收。
///
/// 预缓存走的是 `native/core/app_precache.go`：把分片落到磁盘，
/// 与播放会话解耦，切集 / 退出播放页都不会丢。
class PlaybackCachePreferences {
  const PlaybackCachePreferences({this.episodes = 2, this.limitMB = 1024});

  /// 预缓存集数。默认 2 集 —— 短剧一集 40~120 秒，两集足够覆盖"顺手切下一集"。
  final int episodes;

  /// 缓存容量上限（MB）。
  final int limitMB;

  static const episodeOptions = [0, 1, 2, 3];
  static const limitOptions = [512, 1024, 2048, 4096];

  PlaybackCachePreferences copyWith({int? episodes, int? limitMB}) =>
      PlaybackCachePreferences(
        episodes: episodes ?? this.episodes,
        limitMB: limitMB ?? this.limitMB,
      );

  /// 预缓存是否开启。
  bool get enabled => episodes > 0;

  String get episodeLabel => episodes <= 0 ? '关闭' : '$episodes 集';

  String get limitLabel => limitMB >= 1024
      ? '${(limitMB / 1024).toStringAsFixed(limitMB % 1024 == 0 ? 0 : 1)} GB'
      : '$limitMB MB';

  Map<String, dynamic> toJson() => {'episodes': episodes, 'limitMB': limitMB};

  factory PlaybackCachePreferences.fromJson(Map<String, dynamic> value) {
    final episodes = value['episodes'] as int? ?? 2;
    final limitMB = value['limitMB'] as int? ?? 1024;
    if (!episodeOptions.contains(episodes) || !limitOptions.contains(limitMB)) {
      throw const FormatException('预缓存偏好无效');
    }
    return PlaybackCachePreferences(episodes: episodes, limitMB: limitMB);
  }
}

/// 缓存用量快照（来自 native 的 `mediaCacheStats`）。
class PlaybackCacheUsage {
  const PlaybackCacheUsage({
    this.bytes = 0,
    this.files = 0,
    this.limitBytes = 0,
  });

  final int bytes;
  final int files;
  final int limitBytes;

  String get bytesLabel => formatBytes(bytes);

  String get limitLabel => formatBytes(limitBytes);

  static String formatBytes(int value) {
    if (value >= 1 << 30) {
      return '${(value / (1 << 30)).toStringAsFixed(2)} GB';
    }
    if (value >= 1 << 20) {
      return '${(value / (1 << 20)).toStringAsFixed(1)} MB';
    }
    if (value >= 1 << 10) return '${(value / (1 << 10)).ceil()} KB';
    return '$value B';
  }

  factory PlaybackCacheUsage.fromJson(Map<String, dynamic> value) =>
      PlaybackCacheUsage(
        bytes: (value['bytes'] as num?)?.toInt() ?? 0,
        files: (value['files'] as num?)?.toInt() ?? 0,
        limitBytes: (value['limit'] as num?)?.toInt() ?? 0,
      );
}
