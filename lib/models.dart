import 'dart:convert';

import 'app_build.dart';

class SourceSite {
  const SourceSite(this.id, this.name, this.description);
  final String id;
  final String name;
  final String description;
  bool get onlineSearch =>
      id == 'hongguo' ||
      id == 'huangju' ||
      id == '51duanju' ||
      id == 'yeguo' ||
      id == 'xiangjiao' ||
      id == 'ezdmw' ||
      // 青空次元（0.8.15）：搜索走同一个 JSON 目录接口，支持翻页。
      id == 'sorani' ||
      // 河马剧场（0.8.17）：搜索走 /seo/video/6007，index 就是页码。
      id == 'hema' ||
      // 苹果CMS 采集站（0.8.11）：网果的搜索页被 WAF 拦死（返回"安全验证"），
      // 只走本地缓存搜索，所以不在这个列表里。
      id == 'huaguo' ||
      id == 'faguo' ||
      id == 'wuguo';
  bool get pagedSearch => switch (id) {
    // 花果的搜索页不认 page 参数（第 2 页返回空结果），只给第一页。
    'huangju' ||
    '51duanju' ||
    'yeguo' ||
    'ezdmw' ||
    'sorani' ||
    'hema' ||
    'faguo' ||
    'wuguo' => true,
    _ => false,
  };
  bool get searchSuggestions => id == 'hongguo';
  String get groupId => switch (id) {
    'huangguo-video' || 'huangguoai' || 'cloudfront' => 'huangguo',
    _ => id,
  };
  String get groupName => groupId == 'huangguo' ? '黄果' : name;
  String get entryName => switch (id) {
    'huangguo-video' => '剧场',
    'huangguoai' => 'AI',
    'cloudfront' => '旧版',
    _ => name,
  };

  static const hongguo = SourceSite('hongguo', '红果', '短剧 · 漫剧 · AI 剧');
  static const huangdou = SourceSite('huangdou', '黄豆', '精选短剧');
  static const huangju = SourceSite('huangju', '黄剧', '热门 · 最新 · 分类短剧');
  static const duanju51 = SourceSite('51duanju', '51', '短剧 · 首页 · 榜单');
  static const yeguo = SourceSite('yeguo', '野果', '热播 · 全集正片校验');
  static const xiangjiao = SourceSite('xiangjiao', '香蕉', '热播 · 分集权限识别');
  static const ezdmw = SourceSite('ezdmw', 'E站', '动漫 · 新番 · 完结');
  // 青空次元（0.8.15）：番剧 / 剧场动画 / 特摄，纯 JSON 接口。
  static const sorani = SourceSite('sorani', '青空', '番剧 · 剧场 · 特摄');
  // 河马剧场（0.8.17）：网页只放出前 5 集，付费集走 App 门户接口取流。
  static const hema = SourceSite('hema', '河马', '短剧 · 全集合集');
  // 苹果CMS 采集站（0.8.11）。四个站都是整部剧一个视频，只有发果按集拆开。
  static const huaguo = SourceSite('huaguo', '花果', '短剧 · 花生短剧');
  static const wangguo = SourceSite('wangguo', '网果', '短剧 · 短剧网');
  static const faguo = SourceSite('faguo', '发果', '短剧 · 168 影院');
  static const wuguo = SourceSite('wuguo', '伍果', '短剧 · 五五短剧');
  static const huangguoVideo = SourceSite('huangguo-video', '剧场', '视频剧集');
  static const huangguoAI = SourceSite('huangguoai', 'AI', 'AI 短剧');
  static const cloudfront = SourceSite('cloudfront', '旧版', '旧 API 剧库');

  /// 全部**已知**站源，含已下线但存档里可能出现的。
  ///
  /// 用途是"认名字"而不是"给用户选"：局域网同步、旧存档、下载记录里
  /// 出现 `xiangjiao:xxx` 时，[byId] 仍然能解析出「香蕉」而不是掉回红果。
  static const knownValues = [
    hongguo,
    huangdou,
    huangju,
    duanju51,
    yeguo,
    xiangjiao,
    ezdmw,
    sorani,
    hema,
    huaguo,
    wangguo,
    faguo,
    wuguo,
    huangguoVideo,
    huangguoAI,
    cloudfront,
  ];

  /// 当前**可用 / 可展示**的站源。
  ///
  /// 0.8.10 第三批：香蕉（[xiangjiao]）暂时下线。
  /// 用户反馈它的目录 / 搜索 / 详情整条链路都拿不到数据（客户端能做的
  /// 鉴权与 IPv4 兜底都试过了），留着入口只会让人以为"这个源坏了"，
  /// 所以先从可展示列表里摘掉 —— 不删代码、不动 native 侧，
  /// 等站点恢复把这一项加回下面的列表即可。
  static const values = allSourcesEnabled
      ? [
          hongguo,
          huangdou,
          huangju,
          duanju51,
          yeguo,
          ezdmw,
          sorani,
          hema,
          huaguo,
          wangguo,
          faguo,
          wuguo,
          huangguoVideo,
          huangguoAI,
          cloudfront,
        ]
      : [hongguo];
  static bool isAvailable(String id) => values.any((site) => site.id == id);
  static SourceSite byId(String id) =>
      knownValues.firstWhere((site) => site.id == id, orElse: () => hongguo);

  /// 「黑洞」站源（0.8.15 第二批）。
  ///
  /// 用户要求：这几站在设置里打开「黑洞」之后才展示，默认关闭。
  /// 关闭时它们只是**不出现在站源列表里**（也就不会被拉目录 / 搜索），
  /// 已经下载或历史记录里指向它们的剧集仍然能播 ——
  /// 因此判定放在 `LocalStore.sources`，**不要**塞进 `isAvailable`。
  static const blackholeIds = {
    'huangdou',
    'huangju',
    '51duanju',
    'yeguo',
    // 黄果是一组三入口（剧场 / AI / 旧版），一起算黑洞。
    'huangguo-video',
    'huangguoai',
    'cloudfront',
  };

  static bool isBlackhole(String id) => blackholeIds.contains(id);

  /// 开启「黑洞」站源需要的口令（0.8.16）。
  ///
  /// **这不是安全边界**：口令是客户端常量，翻源码就能看到。它只是把入口
  /// 从「随手一点就开」变成「得知道口令」，免得普通用户误开这一批站源。
  /// 关闭不需要口令。
  static const blackholePassword = 'github-zhenguojianTV';
}

class SourceGroup {
  const SourceGroup(this.id, this.name, this.sources);
  final String id;
  final String name;
  final List<SourceSite> sources;

  static List<SourceGroup> fromSources(Iterable<SourceSite> sources) {
    final groups = <String, List<SourceSite>>{};
    for (final source in sources) {
      (groups[source.groupId] ??= []).add(source);
    }
    return [
      for (final group in groups.entries)
        SourceGroup(group.key, group.value.first.groupName, group.value),
    ];
  }
}

class CatalogCategory {
  const CatalogCategory(this.id, this.name, {this.local = false});
  static const all = CatalogCategory('', '全部');
  final String id;
  final String name;
  final bool local;
  factory CatalogCategory.fromJson(Map<String, dynamic> json) =>
      CatalogCategory(
        json['id'] as String? ?? '',
        json['name'] as String? ?? '全部',
      );
}

int intValue(Object? value) =>
    value is num ? value.toInt() : int.tryParse('$value') ?? 0;

class Drama {
  const Drama({
    required this.id,
    required this.source,
    required this.title,
    this.sourceId = '',
    this.description = '',
    this.cover = '',
    this.episodes = 0,
    this.category = '',
    bool? vip,
    this.heat = '',
    this.views = '',
    this.onlineDate = '',
    this.tags = const [],
    this.releaseStatus = '',
  }) : vipStatus = vip;
  final String id;
  final String source;
  final String sourceId;
  final String title;
  final String description;
  final String cover;
  final int episodes;
  final String category;
  final bool? vipStatus;
  bool get vip => vipStatus == true;
  final String heat;
  final String views;
  final String onlineDate;
  final List<String> tags;
  final String releaseStatus;
  String get releaseLabel => switch (releaseStatus) {
    'finished' || 'completed' => '已完结',
    'ongoing' => '连载中',
    _ => '状态未知',
  };

  factory Drama.fromJson(Map<String, dynamic> json) => Drama(
    id: json['id'] as String? ?? '',
    source: json['source'] as String? ?? 'hongguo',
    sourceId: json['sourceId'] as String? ?? '',
    title: json['title'] as String? ?? '短剧',
    description: json['description'] as String? ?? '',
    cover: json['cover'] as String? ?? '',
    episodes: intValue(json['episodes']),
    category: json['category'] as String? ?? '',
    vip: json['vip'] == true
        ? true
        : json['vip'] == false &&
              (json['source'] != 'huangdou' ||
                  intValue(json['metadataSchema']) >= 1)
        ? false
        : null,
    heat: json['heat']?.toString() ?? '',
    views: json['views']?.toString() ?? '',
    onlineDate: json['onlineDate'] as String? ?? '',
    tags: (json['tags'] as List? ?? const []).whereType<String>().toList(),
    releaseStatus: json['releaseStatus'] as String? ?? '',
  );
  Map<String, dynamic> toJson() => {
    'metadataSchema': 1,
    'id': id,
    'source': source,
    'sourceId': sourceId,
    'title': title,
    'description': description,
    'cover': cover,
    'episodes': episodes,
    'category': category,
    'vip': vipStatus,
    'heat': heat,
    'views': views,
    'onlineDate': onlineDate,
    'tags': tags,
    'releaseStatus': releaseStatus,
  };

  Drama merge(Drama fresh) {
    if (id != fresh.id) return fresh;
    const genericCategories = {
      '短剧',
      '真人剧',
      '漫剧',
      'AI剧',
      'AI 剧',
      'AI短剧',
      'AI 短剧',
      'AI漫剧',
      'AI 漫剧',
    };
    return Drama(
      id: id,
      source: fresh.source.isEmpty ? source : fresh.source,
      sourceId: fresh.sourceId.isEmpty ? sourceId : fresh.sourceId,
      title: fresh.title.isEmpty || fresh.title == '短剧' ? title : fresh.title,
      description: fresh.description.isEmpty ? description : fresh.description,
      cover: fresh.cover.isEmpty ? cover : fresh.cover,
      episodes: fresh.episodes > 0 ? fresh.episodes : episodes,
      category:
          fresh.category.isEmpty ||
              genericCategories.contains(fresh.category) &&
                  category.isNotEmpty &&
                  !genericCategories.contains(category)
          ? category
          : fresh.category,
      vip: fresh.vipStatus ?? vipStatus,
      heat: fresh.heat.isEmpty ? heat : fresh.heat,
      views: fresh.views.isEmpty ? views : fresh.views,
      onlineDate: fresh.onlineDate.isEmpty ? onlineDate : fresh.onlineDate,
      tags: fresh.tags.isEmpty ? tags : fresh.tags,
      releaseStatus:
          fresh.releaseStatus.isEmpty || fresh.releaseStatus == 'unknown'
          ? releaseStatus
          : fresh.releaseStatus,
    );
  }
}

class Episode {
  Episode(this.raw, int fallback)
    : id = raw['id'] as String? ?? '',
      title = raw['title'] as String? ?? '第$fallback集',
      number = intValue(raw['currentEpisode']) > 0
          ? intValue(raw['currentEpisode'])
          : fallback,
      vip = raw['vip'] == true;
  final Map<String, dynamic> raw;
  final String id;
  final String title;
  final int number;
  final bool vip;
}

class DramaDetail {
  DramaDetail(this.drama, this.episodes, {this.warning = ''});
  final Drama drama;
  final List<Episode> episodes;
  final String warning;
  factory DramaDetail.fromJson(Map<String, dynamic> json) {
    final rows = json['chapters'] as List? ?? const [];
    return DramaDetail(
      Drama.fromJson(Map<String, dynamic>.from(json['drama'] as Map)),
      [
        for (var i = 0; i < rows.length; i++)
          Episode(Map<String, dynamic>.from(rows[i] as Map), i + 1),
      ],
      warning: json['warning'] as String? ?? '',
    );
  }
}

class CatalogPage {
  CatalogPage(
    this.items, {
    this.hasMore = false,
    this.warning = '',
    this.page = 1,
    this.fresh = false,
  });
  final List<Drama> items;
  final bool hasMore;
  final String warning;
  final int page;
  final bool fresh;
  factory CatalogPage.fromJson(Map<String, dynamic> json) => CatalogPage(
    [
      for (final row in json['items'] as List? ?? const [])
        Drama.fromJson(Map<String, dynamic>.from(row as Map)),
    ],
    hasMore: json['hasMore'] == true,
    warning: json['warning'] as String? ?? '',
    page: intValue(json['page']) > 0 ? intValue(json['page']) : 1,
    fresh: json['fresh'] == true,
  );
}

class PlaybackPlan {
  const PlaybackPlan({
    required this.url,
    this.headers = const {},
    this.decryptionKey = '',
    this.quality = 0,
    this.qualities = const [],
    this.session = '',
    this.prefetchedBytes = 0,
    this.expiresAt = 0,
    this.routeIndex = 0,
    this.routeCount = 1,
    this.local = false,
    this.danmakuId = '',
  });
  final String url;
  final Map<String, String> headers;
  final String decryptionKey;
  final int quality;
  final List<int> qualities;
  final String session;
  final int prefetchedBytes;
  final int expiresAt;
  final int routeIndex;
  final int routeCount;
  final bool local;

  /// 弹幕（0.8.11）：只有红果会返回这个 id，其余站源为空字符串。
  /// 为空表示本集没有弹幕可看，播放器据此隐藏弹幕开关。
  final String danmakuId;
  bool get hasAlternative => session.isNotEmpty && routeIndex + 1 < routeCount;
  bool get supportsDanmaku => !local && session.isNotEmpty && danmakuId.isNotEmpty;
  factory PlaybackPlan.fromJson(Map<String, dynamic> json) => PlaybackPlan(
    url: json['url'] as String? ?? '',
    local: json['local'] == true,
    headers: (json['headers'] as Map? ?? {}).map(
      (key, value) => MapEntry(key.toString(), value.toString()),
    ),
    decryptionKey: json['decryptionKey'] as String? ?? '',
    quality: intValue(json['quality']),
    qualities: (json['qualities'] as List? ?? []).map(intValue).toSet().toList()
      ..sort((a, b) => b.compareTo(a)),
    session: json['session'] as String? ?? '',
    prefetchedBytes: intValue(json['prefetchedBytes']),
    expiresAt: intValue(json['expiresAt']),
    routeIndex: intValue(json['routeIndex']),
    routeCount: intValue(json['routeCount']) > 0
        ? intValue(json['routeCount'])
        : 1,
    danmakuId: json['danmakuId'] as String? ?? '',
  );
}

class WatchEntry {
  WatchEntry({
    required this.drama,
    required this.episode,
    required this.position,
    required this.duration,
    required this.updatedAt,
  });
  final Drama drama;
  final int episode;
  final double position;
  final double duration;
  final DateTime updatedAt;
  bool get finished => duration > 1 && position >= duration - 1;
  Map<String, dynamic> toJson() => {
    'drama': drama.toJson(),
    'episode': episode,
    'position': position,
    'duration': duration,
    'updatedAt': updatedAt.toIso8601String(),
  };
  factory WatchEntry.fromJson(Map<String, dynamic> json) => WatchEntry(
    drama: Drama.fromJson(Map<String, dynamic>.from(json['drama'] as Map)),
    episode: intValue(json['episode']),
    position: (json['position'] as num?)?.toDouble() ?? 0,
    duration: (json['duration'] as num?)?.toDouble() ?? 0,
    updatedAt:
        DateTime.tryParse(json['updatedAt'].toString()) ?? DateTime(2000),
  );
}

class DownloadJob {
  const DownloadJob({
    required this.id,
    required this.drama,
    required this.episode,
    required this.state,
    this.bytes = 0,
    this.total = 0,
    this.progress = 0,
    this.quality = 0,
    this.actualQuality = 0,
    this.error = '',
    this.created = 0,
    this.revision = 0,
    this.archived = false,
  });
  final int created;
  final int revision;
  final bool archived;
  final String id;
  final Drama drama;
  final Episode episode;
  final String state;
  final int bytes;
  final int total;
  final double progress;
  final int quality;
  final int actualQuality;
  final String error;
  bool get completed => state == 'completed';
  bool get active => state == 'queued' || state == 'downloading';
  bool get resumable => state == 'paused' || state == 'failed';
  String get stateLabel => switch (state) {
    'queued' => '等待下载',
    'downloading' => '正在下载',
    'paused' => '已暂停',
    'failed' => '下载失败',
    'completed' => '已下载',
    'removing' => '正在取消',
    _ => '等待更新',
  };
  factory DownloadJob.fromJson(Map<String, dynamic> value) => DownloadJob(
    id: value['id'] as String? ?? '',
    drama: Drama.fromJson(Map<String, dynamic>.from(value['drama'] as Map)),
    episode: Episode(
      Map<String, dynamic>.from(value['chapter'] as Map),
      intValue(value['index']),
    ),
    state: value['state'] as String? ?? 'failed',
    bytes: intValue(value['bytes']),
    total: intValue(value['total']),
    progress: ((value['progress'] as num?)?.toDouble() ?? 0).clamp(0, 1),
    quality: intValue(value['quality']),
    actualQuality: intValue(value['actualQuality']),
    error: value['error'] as String? ?? '',
    created: intValue(value['created']),
    revision: intValue(value['revision']),
    archived: value['archived'] == true,
  );
}

List<Map<String, dynamic>> readJsonList(String? value) {
  try {
    return (jsonDecode(value ?? '[]') as List)
        .whereType<Map>()
        .map((row) => Map<String, dynamic>.from(row))
        .toList();
  } catch (_) {
    return [];
  }
}
