import 'models.dart';

class RankingBoard {
  const RankingBoard({
    required this.id,
    required this.source,
    required this.name,
    this.description = '',
  });
  final String id;
  final String source;
  final String name;
  final String description;
  String get groupId => SourceSite.byId(source).groupId;
  static String sourceForID(String id) => switch (id) {
    'hongguo-hot' ||
    'hongguo-real' ||
    'hongguo-comic' ||
    'hongguo-ai' => 'hongguo',
    'huangdou-all' ||
    'huangdou-mogai' ||
    'huangdou-search' ||
    'huangdou-favorite' ||
    'huangdou-finish' => 'huangdou',
    'huangguo-hot' ||
    'huangguo-recommend' ||
    'huangguo-potential' => 'huangguoai',
    // 0.8.25 第三批：黄剧 / 51 / E站 / 网果 / 发果 / 囧次元的榜单。
    // 必须与 native 侧 provider_rankings.go 的 board ID 一一对应 ——
    // 榜单列表是 native 下发的，但 board 的 source 由这里反推，
    // 漏一个该榜单就会被 store.allowsSource 过滤掉（表现为「榜单消失」）。
    'huangju-hot' || 'huangju-new' => 'huangju',
    '51duanju-total' || '51duanju-week' || '51duanju-month' => '51duanju',
    'ezdmw-season' => 'ezdmw',
    'wangguo-top' => 'wangguo',
    'faguo-duanju' || 'faguo-anime' => 'faguo',
    'jcydm-riman' || 'jcydm-juchang' => 'jcydm',
    _ => '',
  };
  factory RankingBoard.fromJson(Map<String, dynamic> json) => RankingBoard(
    id: json['id'] as String? ?? '',
    source: sourceForID(json['id'] as String? ?? ''),
    name: json['name'] as String? ?? '',
    description: json['description'] as String? ?? '',
  );
}

class RankingItem {
  const RankingItem(this.rank, this.drama, {this.metric = ''});
  final int rank;
  final Drama drama;
  final String metric;
  factory RankingItem.fromJson(Map<String, dynamic> json) => RankingItem(
    intValue(json['rank']),
    Drama.fromJson(Map<String, dynamic>.from(json['drama'] as Map)),
    metric: json['metric'] as String? ?? '',
  );
}

class RankingPage {
  const RankingPage({
    required this.items,
    this.page = 1,
    this.hasMore = false,
    this.warning = '',
    this.updatedText = '',
    this.stale = false,
  });
  final List<RankingItem> items;
  final int page;
  final bool hasMore;
  final String warning;
  final String updatedText;
  final bool stale;
  factory RankingPage.fromJson(Map<String, dynamic> json) => RankingPage(
    items: [
      for (final row in json['items'] as List? ?? [])
        RankingItem.fromJson(Map<String, dynamic>.from(row as Map)),
    ],
    page: intValue(json['page']),
    hasMore: json['hasMore'] == true,
    warning: json['warning'] as String? ?? '',
    updatedText: json['updatedText'] as String? ?? '',
    stale: json['stale'] == true,
  );
}
