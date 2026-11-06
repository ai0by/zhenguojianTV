import 'package:flutter/material.dart';

import 'app_layout.dart';
import 'catalog_filters.dart';
import 'core_bridge.dart';
import 'detail_screen.dart';
import 'local_store.dart';
import 'models.dart';
import 'ranking_models.dart';
import 'remote_widgets.dart';
import 'widgets.dart';

class RankingsScreen extends StatefulWidget {
  const RankingsScreen({
    super.key,
    required this.repository,
    required this.store,
    required this.initialGroup,
  });
  final AppRepository repository;
  final LocalStore store;
  final String initialGroup;

  @override
  State<RankingsScreen> createState() => _RankingsScreenState();
}

class _RankingsScreenState extends State<RankingsScreen> {
  final _scroll = ScrollController();
  List<RankingBoard> _boards = [];
  RankingBoard? _board;
  List<RankingItem> _items = [];
  bool _loading = true;
  bool _more = false;
  bool _hasMore = false;
  bool _failedMore = false;
  int _page = 1;
  int _generation = 0;
  String? _error;
  String _updated = '';

  @override
  void initState() {
    super.initState();
    widget.repository.catalogUpdates.addListener(_metadataChanged);
    _initialize();
  }

  @override
  void dispose() {
    widget.repository.catalogUpdates.removeListener(_metadataChanged);
    _generation++;
    _scroll.dispose();
    super.dispose();
  }

  void _metadataChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _initialize() async {
    final generation = ++_generation;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      // 按「可展示站源」过滤，而不是 `allowsSource`：黑洞站源（黄豆/黄剧/51…）
      // 关闭时不该在榜单页冒出来，已隐藏的站源（0.8.25 青空）同理。
      // `allowsSource` 只判权限，黑洞开关不在里面（见 LocalStore.sources）。
      final visible = widget.store.sources.map((site) => site.id).toSet();
      final boards = (await widget.repository.rankingBoards())
          .where((board) => visible.contains(board.source))
          .toList();
      if (!mounted || generation != _generation) return;
      setState(() {
        _boards = boards;
        _board =
            boards
                .where((board) => board.groupId == widget.initialGroup)
                .firstOrNull ??
            boards.firstOrNull;
        _loading = false;
      });
      if (_board != null) await _load();
    } catch (error) {
      if (mounted && generation == _generation) {
        setState(() {
          _loading = false;
          _error = error.toString();
        });
      }
    }
  }

  Future<void> _load({bool more = false, bool force = false}) async {
    final board = _board;
    if (board == null || more && (_loading || _more || !_hasMore)) return;
    final generation = ++_generation;
    setState(() {
      _loading = !more;
      _more = more;
      _error = null;
      _failedMore = false;
    });
    try {
      final result = await widget.repository.rankings(
        board.id,
        page: more ? _page + 1 : 1,
        force: force,
      );
      if (!mounted || generation != _generation) return;
      setState(() {
        final rows = <String, RankingItem>{
          if (more)
            for (final item in _items) item.drama.id: item,
          for (final item in result.items) item.drama.id: item,
        };
        _items = rows.values.toList()..sort((a, b) => a.rank.compareTo(b.rank));
        _page = result.page;
        _hasMore = result.hasMore;
        _updated = result.updatedText;
        _error = result.warning.isEmpty ? null : result.warning;
        _loading = false;
        _more = false;
      });
    } catch (error) {
      if (mounted && generation == _generation) {
        setState(() {
          _error = error.toString();
          _failedMore = more;
          _loading = false;
          _more = false;
        });
      }
    }
  }

  void _select(RankingBoard board) {
    if (_board?.id == board.id) return;
    setState(() {
      _board = board;
      _items = [];
      _updated = '';
      _page = 1;
      _hasMore = false;
    });
    if (_scroll.hasClients) _scroll.jumpTo(0);
    _load();
  }

  /// 榜单站源入口（0.8.2）。
  ///
  /// 原先是 `PopupMenuButton` 的裸文字：电视上不可聚焦（遥控器停不到），
  /// 弹出菜单又贴着 AppBar、没有焦点环。现在统一成 [SourceSwitchPill]，
  /// 电视下按确定打开可聚焦的居中列表。
  Widget _sourceSwitcher(List<SourceGroup> groups) => SourceSwitchPill(
    key: const ValueKey('rankings-source-switch'),
    label: _board == null
        ? '榜单'
        : '${SourceSite.byId(_board!.source).groupName}榜单',
    count: groups.length,
    onPressed: groups.length > 1 ? () => _pickGroup(groups) : null,
  );

  Future<void> _pickGroup(List<SourceGroup> groups) async {
    final options = [
      for (final group in groups)
        (group.id, group.name, true),
    ];
    String? picked;
    if (AppLayout.isTelevision(context)) {
      picked = await showDialog<String>(
        context: context,
        barrierColor: Colors.black.withValues(alpha: .66),
        builder: (dialogContext) => Dialog(
          backgroundColor: Colors.transparent,
          elevation: 0,
          insetPadding: const EdgeInsets.symmetric(
            horizontal: 48,
            vertical: 32,
          ),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560),
            child: RemoteOptionSheet(
              title: '选择榜单站源',
              keyPrefix: 'rank-group',
              options: options,
            ),
          ),
        ),
      );
    } else {
      picked = await showModalBottomSheet<String>(
        context: context,
        backgroundColor: Colors.transparent,
        builder: (sheetContext) => SafeArea(
          top: false,
          child: Container(
            margin: const EdgeInsets.fromLTRB(12, 0, 12, 12),
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 10),
            decoration: BoxDecoration(
              color: Theme.of(sheetContext).colorScheme.surface,
              borderRadius: BorderRadius.circular(18),
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Text(
                  '选择榜单站源',
                  style: TextStyle(fontSize: 16, fontWeight: FontWeight.w800),
                ),
                const SizedBox(height: 4),
                for (final group in groups)
                  ListTile(
                    key: ValueKey('rank-group-${group.id}'),
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    title: Text(group.name),
                    trailing: group.id == _board?.groupId
                        ? const Icon(Icons.check_rounded)
                        : null,
                    onTap: () => Navigator.of(sheetContext).pop(group.id),
                  ),
              ],
            ),
          ),
        ),
      );
    }
    if (picked == null || !mounted) return;
    for (final board in _boards) {
      if (board.groupId == picked) {
        _select(board);
        return;
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final groups = SourceGroup.fromSources(
      _boards.map((board) => SourceSite.byId(board.source)).toSet(),
    );
    final boards = _boards
        .where((board) => board.groupId == _board?.groupId)
        .toList();
    final items = _items
        .map(
          (item) => RankingItem(
            item.rank,
            widget.repository.catalogUpdates.current(item.drama),
            metric: item.metric,
          ),
        )
        .where(
          (item) =>
              widget.store.allowsSource(item.drama.source) &&
              !(item.drama.source == 'huangdou' &&
                  widget.store.hideVip &&
                  item.drama.vip),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: !AppLayout.isTelevision(context),
        title: _sourceSwitcher(groups),
        actions: [
          RefreshAction(
            loading: _loading || _more,
            tooltip: '更新榜单',
            onPressed: _board == null ? _initialize : () => _load(force: true),
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            if (boards.isNotEmpty)
              CatalogFilters(
                key: ValueKey('ranking-boards-${_board!.groupId}'),
                categories: [
                  for (final board in boards)
                    CatalogCategory(board.id, board.name),
                ],
                category: _board!.id,
                onCategory: (id) =>
                    _select(boards.firstWhere((board) => board.id == id)),
                onRetry: () => _load(force: true),
              ),
            if (_board != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    [
                      _board!.description,
                      if (_updated.isNotEmpty) _updated,
                    ].join(' '),
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ),
              ),
            if (_loading && items.isNotEmpty)
              const LinearProgressIndicator(minHeight: 2),
            if (_error != null && items.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                    TextButton(
                      onPressed: _loading || _more
                          ? null
                          : () => _load(more: _failedMore, force: true),
                      child: const Text('重试'),
                    ),
                  ],
                ),
              ),
            Expanded(
              child: _loading && items.isEmpty
                  ? const LoadingHint(message: '正在获取榜单…')
                  : items.isEmpty
                  ? StatusPanel(
                      title: _error == null ? '暂无榜单内容' : '榜单暂时无法加载',
                      message: _error ?? '当前站源暂无可显示的榜单。',
                      onRetry: _board == null
                          ? _initialize
                          : () => _load(force: true),
                    )
                  : RefreshIndicator(
                      onRefresh: () => _load(force: true),
                      child: ListView.builder(
                        key: ValueKey('ranking-${_board!.id}'),
                        controller: _scroll,
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(12, 0, 12, 24),
                        itemCount: items.length + 1,
                        itemBuilder: (context, index) {
                          if (index == items.length) {
                            return Center(
                              child: _more
                                  ? const Padding(
                                      padding: EdgeInsets.all(16),
                                      child: LoadingHint(
                                        message: '正在加载更多…',
                                        compact: true,
                                      ),
                                    )
                                  : _hasMore
                                  ? OutlinedButton(
                                      onPressed: () => _load(more: true),
                                      child: const Text('加载更多'),
                                    )
                                  : const Padding(
                                      padding: EdgeInsets.all(16),
                                      child: Text('已显示全部榜单'),
                                    ),
                            );
                          }
                          final item = items[index];
                          return Card(
                            child: InkWell(
                              key: ValueKey(
                                'rank-${item.rank}-${item.drama.id}',
                              ),
                              borderRadius: BorderRadius.circular(12),
                              onFocusChange: (focused) {
                                if (focused) {
                                  Scrollable.ensureVisible(
                                    context,
                                    alignment: .4,
                                  );
                                }
                              },
                              onTap: () => Navigator.push<void>(
                                context,
                                MaterialPageRoute(
                                  builder: (_) => DetailScreen(
                                    drama: item.drama,
                                    repository: widget.repository,
                                    store: widget.store,
                                  ),
                                ),
                              ),
                              child: Padding(
                                padding: const EdgeInsets.all(12),
                                child: Row(
                                  children: [
                                    SizedBox(
                                      width: 36,
                                      child: Text(
                                        '${item.rank}',
                                        style: TextStyle(
                                          fontSize: 24,
                                          fontWeight: FontWeight.w800,
                                          color: item.rank <= 3
                                              ? Theme.of(
                                                  context,
                                                ).colorScheme.primary
                                              : Theme.of(
                                                  context,
                                                ).colorScheme.onSurfaceVariant,
                                        ),
                                      ),
                                    ),
                                    SizedBox(
                                      width: 72,
                                      height: 108,
                                      child: DramaCover(
                                        drama: item.drama,
                                        repository: widget.repository,
                                        radius: 8,
                                      ),
                                    ),
                                    const SizedBox(width: 14),
                                    Expanded(
                                      child: Column(
                                        crossAxisAlignment:
                                            CrossAxisAlignment.start,
                                        children: [
                                          Text(
                                            item.drama.title,
                                            maxLines: 2,
                                            overflow: TextOverflow.ellipsis,
                                            style: Theme.of(
                                              context,
                                            ).textTheme.titleMedium,
                                          ),
                                          if (item.drama.category.isNotEmpty)
                                            Text(
                                              item.drama.category,
                                              maxLines: 1,
                                              overflow: TextOverflow.ellipsis,
                                            ),
                                          if (item.metric.isNotEmpty)
                                            Padding(
                                              padding: const EdgeInsets.only(
                                                top: 8,
                                              ),
                                              child: Text(
                                                item.metric,
                                                style: TextStyle(
                                                  color: Theme.of(
                                                    context,
                                                  ).colorScheme.primary,
                                                ),
                                              ),
                                            ),
                                        ],
                                      ),
                                    ),
                                    const Icon(Icons.chevron_right_rounded),
                                  ],
                                ),
                              ),
                            ),
                          );
                        },
                      ),
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
