import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';

import 'app_layout.dart';
import 'about_credit.dart';
import 'app_bottom_navigation.dart';
import 'back_guard.dart';
import 'core_bridge.dart';
import 'catalog_filters.dart';
import 'catalog_browser.dart';
import 'catalog_auto_updater.dart';
import 'catalog_sort.dart';
import 'catalog_sort_sheet.dart';
import 'display_mode.dart';
import 'recommendations_screen.dart';
import 'tv_home.dart';
import 'vertical_feed_screen.dart';
import 'rankings_screen.dart';
import 'detail_screen.dart';
import 'downloads_screen.dart';
import 'local_store.dart';
import 'lan_screen.dart';
import 'models.dart';
import 'remote_widgets.dart';
import 'widgets.dart';
import 'vip_icon.dart';
import 'settings_screen.dart';
import 'profiles_screen.dart';
import 'pinyin.dart';
import 'search_input.dart';
import 'sources_screen.dart';
import 'batch_download_screen.dart';
import 'batch_downloads.dart';
import 'drama_actions.dart';
import 'library_updater.dart';
import 'saved_library.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({
    super.key,
    required this.repository,
    required this.store,
    this.initialTab,
    this.feedPlayerFactory,
    this.onFeedReady,
  });
  final AppRepository repository;
  final LocalStore store;

  /// 初始标签下标。为空时默认打开「首页」（竖滑视频流）。
  final int? initialTab;

  /// 测试用：注入首页假播放器。
  final Player Function()? feedPlayerFactory;

  /// 首页竖滑流拿到第一批可播放视频后回调（用于关闭开屏页）。
  final VoidCallback? onFeedReady;
  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
  static const _recommendationCategory = 'app:recommendations';
  final _search = TextEditingController();
  final _scroll = ScrollController();
  Timer? _debounce;
  late SourceSite _source;
  bool _allSources = false;

  /// 多选站源（非空时优先于单源/全部，作为当前浏览范围）。
  List<String> _multiSources = const [];
  List<Drama> _items = [];

  /// 当前生效的拼音/首字母关键词（0.8.4），空串表示没在用拼音搜索。
  ///
  /// 单独存一份而不是复用 `_search.text`：用户敲完 `hjdnz` 之后可能又手动
  /// 改了输入框但没提交，这时候列表应该保持上一次提交的结果，而不是抖成空。
  String _pinyinQuery = '';

  /// 拼音搜索的候选池 = 浏览过的全部剧目（见 `CatalogBrowser.library`）。
  List<Drama> _pinyinPool = const [];
  bool _loading = true;
  bool _loadingMore = false;
  bool _hasMore = true;
  String? _error;
  int _generation = 0;
  int _tab = 0;
  bool _downloadVisited = false;
  String _submittedQuery = '';
  bool _failedMore = false;
  final _categorySelections = <String, String>{};
  late final CatalogBrowser _browser;
  bool _searchVisible = false;
  bool _categoriesLoading = false;
  String? _categoriesError;
  int _categoryGeneration = 0;
  late final LibraryUpdater _updater;
  late final CatalogAutoUpdater _autoUpdater;
  final _changedSources = <String>{};
  final _selectedDramas = <String, Drama>{};
  Timer? _cacheRefreshTimer;
  bool _refreshingUpdatedCache = false;
  bool _updateNotice = false;
  bool _selectionMode = false;
  bool _showRecommendations = false;

  /// 双击「首页」标签时自增，通知竖滑流重新随机。
  final _feedRefresh = ValueNotifier<int>(0);

  /// 首页剧类筛选（0.7.2，`XQ-0106`）：all / real / comic。
  ///
  /// 用户明确限定：**只有单独选中红果时可用**；
  /// 其它源或多源（即使包含红果）都不支持——因为这些站源没有这套分类。
  String _feedGenre = 'all';

  /// 剧类选项（值, 标题, 说明）。
  static const _feedGenreOptions = <(String, String, String)>[
    ('all', '全部', '真人剧 + 漫剧 + AI 剧 混合随机'),
    ('real', '真人剧', '只刷红果真人剧'),
    ('comic', '漫剧', '漫剧 + AI 剧'),
  ];

  /// 当前是否允许使用剧类筛选：**恰好只有一个红果源**。
  bool get _feedGenreEnabled {
    final sources = _group.sources;
    return sources.length == 1 && sources.first.id == 'hongguo';
  }

  String get _feedGenreLabel => switch (_feedGenre) {
    'real' => '真人剧',
    'comic' => '漫剧',
    _ => '全部',
  };

  /// 传给竖滑流的红果分类；null = 默认三类混合。
  List<String>? get _feedHongguoGenres {
    if (!_feedGenreEnabled) return null;
    return switch (_feedGenre) {
      'real' => const ['short_play'],
      'comic' => const ['comic_series', 'ai_series'],
      _ => null,
    };
  }

  /// 打开剧类筛选弹窗（0.7.2，`XQ-0106`）。
  Future<void> _openFeedGenreSheet() async {
    if (!widget.store.sources.any((site) => site.id == 'hongguo')) {
      return;
    }
    if (!_feedGenreEnabled) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('剧类筛选只在「仅红果」时可用')));
      return;
    }
    final picked = await showModalBottomSheet<String>(
      context: context,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Padding(
              padding: EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Text(
                '剧类（仅红果）',
                style: TextStyle(fontWeight: FontWeight.w800, fontSize: 16),
              ),
            ),
            for (final option in _feedGenreOptions)
              ListTile(
                key: ValueKey('feed-genre-${option.$1}'),
                leading: Icon(
                  option.$1 == _feedGenre
                      ? Icons.radio_button_checked_rounded
                      : Icons.radio_button_off_rounded,
                ),
                title: Text(option.$2),
                subtitle: Text(option.$3),
                onTap: () => Navigator.of(sheetContext).pop(option.$1),
              ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
    if (picked == null || !mounted || picked == _feedGenre) return;
    setState(() => _feedGenre = picked);
  }

  List<SourceGroup> get _sourceGroups {
    final groups = SourceGroup.fromSources(widget.store.sources);
    return groups;
  }

  /// 多选模式下自定义的站源分组。
  SourceGroup? get _multiGroup {
    if (_multiSources.isEmpty) return null;
    final sites = widget.store.sources
        .where((site) => _multiSources.contains(site.id))
        .toList();
    if (sites.isEmpty) return null;
    // 0.6.11 修复（用户反馈"仅选两个播放器也会提示切换到全部源"）：
    //
    // 原先判据是 `sites.length == widget.store.sources.length` —— 只比**数量**。
    // 但源是分组的（黄果视频 / 黄果 AI / 黄果旧版同属 `huangguo` 组），
    // 用户勾选「黄果」这一项就会一次选中 3 个站点；
    // 若当前可用源恰好也是 3 个，数量相等 → 误判为「全部站源」。
    // 正确判据必须是**选中的 id 集合 == 可用的 id 集合**，而不是数量相等。
    final allIds = widget.store.sources.map((site) => site.id).toSet();
    final pickedIds = sites.map((site) => site.id).toSet();
    final isAll =
        allIds.length == pickedIds.length && allIds.containsAll(pickedIds);
    final name = isAll ? '全部站源' : sites.map((site) => site.entryName).join('+');
    return SourceGroup('multi', name, sites);
  }

  SourceGroup get _group {
    final multi = _multiGroup;
    if (multi != null) return multi;
    return _sourceGroups
            .where((group) => group.id == _source.groupId)
            .firstOrNull ??
        SourceGroup(_source.groupId, _source.groupName, [_source]);
  }

  bool get _onlineSearch => _group.sources.any((source) => source.onlineSearch);
  bool get _searchSuggestions =>
      _group.sources.any((source) => source.searchSuggestions);
  String get _searchHint {
    final online = _group.sources
        .where((source) => source.onlineSearch)
        .toList();
    if (online.isEmpty) {
      return '筛选本机已更新剧库';
    }
    final names = online.map((source) => source.name).join('、');
    // 「或拼音/首字母」（0.8.4）：这个能力不写在提示里，用户不会知道。
    return online.length == _group.sources.length
        ? '搜索${names}短剧，或拼音/首字母'
        : '搜索${names}及本机剧库，或拼音/首字母';
  }

  String get _category => _categorySelections[_group.id] ?? '';
  List<CatalogCategory> get _categories => _browser.categories(_group);
  String get _displayCategory =>
      _showRecommendations ? _recommendationCategory : _category;
  List<CatalogCategory> get _displayCategories => [
    CatalogCategory.all,
    if (_group.id == 'hongguo')
      const CatalogCategory(_recommendationCategory, '推荐'),
    ..._categories.where((entry) => entry.id.isNotEmpty),
  ];

  Future<void> _loadCategories({
    bool force = false,
    bool cacheOnly = false,
  }) async {
    final generation = ++_categoryGeneration;
    final group = _group;
    setState(() {
      _categoriesLoading = true;
      _categoriesError = null;
    });
    final error = await _browser.loadCategories(
      group,
      force: force,
      cacheOnly: cacheOnly,
    );
    if (!mounted ||
        generation != _categoryGeneration ||
        group.id != _group.id) {
      return;
    }
    setState(() {
      _categoriesLoading = false;
      _categoriesError = error;
    });
    if (!_showRecommendations &&
        _category.isNotEmpty &&
        !_categories.any((entry) => entry.id == _category)) {
      _changeCategory('');
    }
  }

  Future<void> _refreshCatalog() async {
    if (widget.repository.supportsSourceManagement) {
      if (_group.sources.any((source) => _updater.busy(source.id))) {
        // 0.8.10：原来这里是静默 return —— 用户下拉后看不到任何反应，
        // 只看到顶部横幅一直停在"查找新剧"，就是"下拉刷新一直不成功"。
        // 现在至少把横幅亮出来，让他看到"确实有一个任务在跑"。
        //
        // 这里刻意**不**顺手 `_load()`：任务结束时 `onCatalogChanged` 会触发
        // 缓存重载，这里再发一次会多打一轮目录请求（回归测试抓到的）。
        if (!_updateNotice) setState(() => _updateNotice = true);
        return;
      }
      _pauseCatalog();
      setState(() => _updateNotice = true);
      await _updater.update(_group.sources);
      return;
    }
    await _loadCategories(force: true);
    if (mounted) await _load(force: true);
  }

  void _updateChanged() {
    if (mounted) setState(() {});
  }

  void _autoUpdateChanged() {
    if (mounted) setState(() {});
  }

  void _catalogUpdated(String source) {
    if (!mounted) return;
    _changedSources.add(source);
    _cacheRefreshTimer?.cancel();
    _cacheRefreshTimer = Timer(const Duration(milliseconds: 100), () {
      unawaited(_reloadUpdatedCache());
    });
  }

  Future<void> _reloadUpdatedCache() async {
    if (_refreshingUpdatedCache) return;
    _refreshingUpdatedCache = true;
    final epoch = widget.store.profileEpoch;
    try {
      while (mounted &&
          _changedSources.isNotEmpty &&
          epoch == widget.store.profileEpoch) {
        final sources = Set.of(_changedSources);
        _changedSources.clear();
        final updates = <String, Drama>{};
        for (final source in sources) {
          if (!widget.store.allowsSource(source)) continue;
          try {
            final cached = await widget.repository.cached(source);
            for (final drama in cached.items) {
              if (widget.store.allowsSource(drama.source)) {
                updates[drama.id] = drama;
              }
            }
          } catch (error) {
            if (mounted && epoch == widget.store.profileEpoch) {
              setState(() => _error = '更新后读取缓存失败：$error');
            }
          }
        }
        if (!mounted || epoch != widget.store.profileEpoch) return;
        _browser.updateDramas(updates.values);
        setState(() {
          _items = [
            for (final item in _items)
              updates[item.id] == null ? item : item.merge(updates[item.id]!),
          ];
          for (final id in _selectedDramas.keys.toList()) {
            if (updates[id] != null) {
              _selectedDramas[id] = _selectedDramas[id]!.merge(updates[id]!);
            }
          }
        });
        await saveUserChange(
          context,
          () => widget.store.refreshDramas(updates.values),
        );
        if (!mounted || epoch != widget.store.profileEpoch) return;
        if (_group.sources.any((source) => sources.contains(source.id))) {
          await _loadCategories(cacheOnly: true);
          if (mounted &&
              !_loading &&
              !_loadingMore &&
              (!_onlineSearch || _search.text.trim().isEmpty)) {
            await _load(cacheOnly: true);
          }
        }
      }
    } finally {
      _refreshingUpdatedCache = false;
    }
  }

  /// AppBar 的站源入口：点开**弹窗**，勾选后按「确定」才生效。
  ///
  /// 0.6.11 改版（用户反馈）：
  /// 原先是 `PopupMenuButton`，**点一下菜单项就立刻生效并重载列表**，导致：
  /// ① 想选 3 个源要来回点 3 次，每次都会触发重载，界面卡顿；
  /// ② 中途误点无法取消，只能再点回来；
  /// ③ 下拉菜单在手机上偏窄、勾选状态不明显。
  /// 现在改为**底部弹窗 + 勾选 + 确定/取消**：弹窗内随便勾，只有按「确定」
  /// 才写回并重载一次，中途可按「取消」或点遮罩放弃。
  Widget _sourceMenu() {
    final enabled = widget.store.sources.isNotEmpty;
    return SourceSwitchPill(
      key: const ValueKey('source-switch'),
      label: _group.name,
      count: _group.sources.length,
      onPressed: enabled ? _openSourceSheet : null,
    );
  }

  /// 打开站源选择弹窗（0.6.11 新增）。
  ///
  /// 弹窗内维护**临时**选中集合，只有按「确定」才调用 [_applyMultiSources]。
  Future<void> _openSourceSheet() async {
    final groups = _sourceGroups;
    if (groups.isEmpty) return;
    // 当前已选中的 group id 集合：多选时取 `_multiSources`，单选时取当前源。
    final initial = <String>{
      if (_multiSources.isNotEmpty)
        for (final site in widget.store.sources)
          if (_multiSources.contains(site.id)) site.groupId,
      if (_multiSources.isEmpty) _source.groupId,
    };
    // TV 上用**居中对话框**而不是底部弹窗：
    // 底部弹窗在 16:9 电视上离视线中心太远，且容易被底部过扫描裁掉；
    // 同时遥控器的焦点在居中卡片里横向移动更自然。
    final television = AppLayout.isTelevision(context);
    final Set<String>? result;
    if (television) {
      result = await showDialog<Set<String>>(
        context: context,
        barrierColor: Colors.black.withValues(alpha: .66),
        builder: (dialogContext) => Dialog(
          backgroundColor: Colors.transparent,
          elevation: 0,
          insetPadding: EdgeInsets.symmetric(
            horizontal: 48,
            vertical: MediaQuery.sizeOf(dialogContext).height < 600 ? 12 : 32,
          ),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 760),
            child: _SourcePickerSheet(
              groups: groups,
              initial: initial,
              allSources: widget.store.sources,
              television: true,
            ),
          ),
        ),
      );
    } else {
      result = await showModalBottomSheet<Set<String>>(
        context: context,
        backgroundColor: Colors.transparent,
        isScrollControlled: true,
        builder: (sheetContext) => SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
            child: _SourcePickerSheet(
              groups: groups,
              initial: initial,
              allSources: widget.store.sources,
            ),
          ),
        ),
      );
    }
    if (result == null || !mounted) return;
    // 「确定」：把勾选的分组展开成站点 id 列表后应用。
    final ids = <String>[
      for (final site in widget.store.sources)
        if (result.contains(site.groupId)) site.id,
    ];
    if (ids.isEmpty) {
      // 一个都没勾 = 恢复单选当前源（不允许"什么都不选"的空状态）。
      _changeSource(SourceSite.byId(_source.id));
      return;
    }
    await _applyMultiSources(ids);
  }

  /// 切换到「多选站源」状态：同时聚合勾选的若干个源。
  ///
  /// 勾选数量语义：
  /// - 0 个：回到单选（使用 [_source]）。
  /// - 1 个：等价单选，直接切到该源。
  /// - ≥2 个：多选聚合，`_multiSources` 生效。
  Future<void> _applyMultiSources(List<String> ids) async {
    final available = widget.store.sources
        .where((site) => ids.contains(site.id))
        .map((site) => site.id)
        .toList();
    if (available.isEmpty) {
      setState(() {
        _multiSources = const [];
        _allSources = false;
      });
      await widget.store.setMultiSources(const []);
      return;
    }
    if (available.length == 1) {
      final leavingMulti = _multiSources.isNotEmpty;
      setState(() {
        _multiSources = const [];
        _allSources = false;
      });
      await widget.store.setMultiSources(const []);
      if (!mounted) return;
      // 0.8.10：从多选退到单选必须强制重载，否则"目标源恰好就是原来的单选源"时
      // `_changeSource` 会直接返回，列表还停在上一次的多源聚合结果。
      _changeSource(SourceSite.byId(available.first), force: leavingMulti);
      return;
    }
    setState(() {
      _multiSources = available;
      _allSources = false;
      // 多选分组不是「红果」，推荐流对它是无意义的；留着 `_showRecommendations`
      // 还会让紧接着的 `_load` 在开头直接 return，页面一样不刷新。
      _showRecommendations = false;
      _selectionMode = false;
      _selectedDramas.clear();
    });
    await widget.store.setMultiSources(available);
    if (!mounted) return;
    if (_scroll.hasClients) _scroll.jumpTo(0);
    _load(useCache: true);
    _loadCategories();
  }

  void _changeCategory(String category) {
    if (category == _recommendationCategory && _group.id == 'hongguo') {
      if (_showRecommendations) return;
      _pauseCatalog();
      setState(() {
        _showRecommendations = true;
        _selectionMode = false;
        _selectedDramas.clear();
        _search.clear();
        _searchVisible = false;
        _submittedQuery = '';
      });
      return;
    }
    if (_category == category && !_showRecommendations) return;
    _debounce?.cancel();
    setState(() {
      _showRecommendations = false;
      _selectionMode = false;
      _selectedDramas.clear();
      _categorySelections[_group.id] = category;
      if (_onlineSearch) {
        _search.clear();
        _submittedQuery = '';
      }
      _items = [];
      _hasMore = true;
      _error = null;
    });
    if (_scroll.hasClients) _scroll.jumpTo(0);
    _load(useCache: true);
  }

  void _swipeCategory(DragEndDetails details) {
    final velocity = details.primaryVelocity ?? 0;
    if (velocity.abs() < 240) return;
    final categories = _displayCategories;
    final index = categories.indexWhere(
      (entry) => entry.id == _displayCategory,
    );
    final next = index + (velocity < 0 ? 1 : -1);
    if (next >= 0 && next < categories.length) {
      _changeCategory(categories[next].id);
    }
  }

  void _toggleSearch() {
    if (AppLayout.isTelevision(context)) {
      _televisionSearch();
      return;
    }
    if (_showRecommendations) _changeCategory('');
    final hadQuery = _search.text.isNotEmpty;
    setState(() {
      _searchVisible = !_searchVisible;
      if (!_searchVisible) _search.clear();
    });
    if (!_searchVisible && hadQuery) _searchChanged('');
  }

  void _openRankings() {
    _pauseCatalog();
    Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => RankingsScreen(
          repository: widget.repository,
          store: widget.store,
          initialGroup: _group.id,
        ),
      ),
    );
  }

  Future<void> _manageSources() async {
    _pauseCatalog();
    await Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => SourcesScreen(
          repository: widget.repository,
          store: widget.store,
          initialSource: _source.id,
        ),
      ),
    );
    if (!mounted) return;
    await _loadCategories();
    if (mounted && (!_onlineSearch || _submittedQuery.isEmpty)) {
      await _load(useCache: true);
    }
  }

  Future<void> _chooseDisplayMode() => chooseDisplayMode(context, widget.store);

  /// 电视搜索的「热门搜索」词（0.8.3）。
  ///
  /// 取当前剧库里**真实存在**的片名，而不是写死的营销词——
  /// 无输入法的盒子上，用户点一下就能搜到眼前这部；写死的词很可能搜不到东西，
  /// 点了等于白点。
  List<String> get _televisionHotSearches {
    final seen = <String>{};
    final result = <String>[];
    for (final drama in _items) {
      final title = drama.title.trim();
      if (title.isEmpty || !seen.add(title)) continue;
      result.add(title);
      if (result.length >= 10) break;
    }
    return result;
  }

  Future<void> _televisionSearch() async {
    final query = await showDialog<String>(
      context: context,
      builder: (_) => TelevisionSearchDialog(
        initialValue: _search.text,
        title: _multiSources.isNotEmpty
            ? '搜索已选站源'
            : _onlineSearch
            ? '搜索${_group.name}短剧'
            : '筛选当前已加载短剧',
        recentSearches: widget.store.recentSearches,
        hotSearches: _televisionHotSearches,
        onCancel: () => unawaited(widget.repository.cancelSuggestions()),
        suggestions: _searchSuggestions ? widget.repository.suggestions : null,
      ),
    );
    if (query != null && mounted) {
      _submitSearch(query);
    }
  }

  /// 电视返回键（0.8.1，0.8.7 改为"再按一次退出"，0.8.9 修回首页焦点）。
  ///
  /// 逐层退让：选择模式 → 退出选择；不在首页 → 回首页（**并把左侧菜单的
  /// 焦点一并移回「首页」**）；搜索有词 → 清空搜索；都没有 → 第一次按提示、
  /// 两秒内再按一次才退出应用。
  ///
  /// 0.8.7 不再弹"是否退出"对话框：电视上返回键会先把对话框关掉，
  /// 下一次返回又被同一条逻辑重新弹出来，用户绕不出去。
  ///
  /// 0.8.9 修两个真机问题：
  /// 1. 在「发现」页按返回，偶尔直接弹"再按一次退出"。部分盒子一次按键会
  ///    同时产生键盘事件和系统 `popRoute`，两条都走到这里：第一条把页面退回
  ///    首页，第二条到达时 `_tab` 已经是首页，于是被当成"在首页按返回"。
  ///    现在把去重窗口放宽到 [_backRepeatWindow]（见下方 `_backGuard` 的注释），
  ///    同一次按键的第二条事件在入口就被丢掉，不会再往下逐层退让。
  /// 2. 界面退回首页了，但左侧菜单的光标还停在「最近观看」上。
  ///    现在退回首页时显式把焦点交给「首页」那一项（[_focusNavItem]）。
  Future<void> _televisionBack() async {
    if (!_backGuard.claim()) return;
    if (_selectionMode) {
      _cancelSelection();
      return;
    }
    // 0.8.9：发现页里打开的"推荐分类"是一层子页面，先退这一层，
    // 再退到首页，否则一次返回会连跳两级。
    //
    // 这里必须走 `_changeCategory('')` 而不是裸 `setState`：后者只把标记翻掉，
    // 列表里仍是推荐内容，而且不会重新拉取分类数据（`_changeCategory` 会清空
    // `_items`、重置分页、回滚滚动位置并 `_load`）。
    if (_showRecommendations) {
      _changeCategory('');
      return;
    }
    if (_tab != _feedIndex) {
      _changeTab(_feedIndex);
      _focusNavItem(_feedIndex);
      return;
    }
    if (_search.text.isNotEmpty || _pinyinQuery.isNotEmpty) {
      _clearSearch();
      return;
    }
    _armExit();
  }

  /// 左侧菜单每项的焦点节点（0.8.9）。
  ///
  /// 为什么需要：`IndexedStack` 只切换显示页，左侧菜单始终在树里，
  /// 焦点会**留在原来那一项**上。返回键退回首页时如果不显式移焦，
  /// 光标就停在「最近观看」上，和当前页面不一致。
  final Map<int, FocusNode> _navFocusNodes = {};

  FocusNode _navFocusNode(int tab) => _navFocusNodes.putIfAbsent(
    tab,
    () => FocusNode(debugLabel: 'tv-nav-focus-$tab'),
  );

  /// 把左侧菜单焦点移到指定项（下一帧，等页面切换完成）。
  void _focusNavItem(int tab) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _navFocusNode(tab).requestFocus();
    });
  }

  /// 电视返回键去重（0.8.9：窗口 180ms → 400ms）。
  ///
  /// 部分盒子把**一次**返回按键同时报成键盘事件和系统 `popRoute`，两条都会
  /// 走到 [_televisionBack]。原来 180ms 只够覆盖"两条都在处理前到达"的情况；
  /// 一旦第一条已经把页面退到首页、第二条稍后才到（实测间隔 100~200ms，
  /// 加上退页动画还会更久），去重就失效，第二条被当成"在首页又按了一次"，
  /// 于是直接弹出"再按一次退出"。
  ///
  /// 400ms 的依据：重复事件间隔实测在 200ms 以内，留一倍余量；同时不会吃掉
  /// 用户为了退出而**真的**再按一次（提示出现后人手再按通常 ≥500ms）。
  /// 这个 guard 只有电视返回键在用，不影响手机端。
  final _backGuard = BackGuard(window: const Duration(milliseconds: 400));
  DateTime? _exitArmedAt;
  Timer? _exitTimer;
  static const _exitWindow = Duration(seconds: 2);

  void _armExit() {
    final armed = _exitArmedAt;
    if (armed != null && DateTime.now().difference(armed) <= _exitWindow) {
      _exitTimer?.cancel();
      _exitArmedAt = null;
      unawaited(SystemNavigator.pop());
      return;
    }
    _exitArmedAt = DateTime.now();
    _exitTimer?.cancel();
    _exitTimer = Timer(_exitWindow, () => _exitArmedAt = null);
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(
        SnackBar(
          content: Text(
            '再按一次「返回」退出$appName',
            style: const TextStyle(fontSize: 15),
          ),
          duration: _exitWindow,
          behavior: SnackBarBehavior.floating,
          margin: const EdgeInsets.fromLTRB(24, 0, 24, 28),
        ),
      );
  }

  /// 遥控器返回键的键盘事件入口（部分盒子只发键盘事件，不发系统返回）。
  void _keyboardBack() => popWithBackGuard(context);

  @override
  void initState() {
    super.initState();
    // 监听前后台切换：从后台返回时刷新首页竖滑流一次（用户要求）。
    WidgetsBinding.instance.addObserver(this);
    _source = SourceSite.byId(widget.store.source);
    _allSources = widget.store.catalogView.allSources;
    _multiSources = widget.store.multiSources;
    if (_allSources &&
        _multiSources.isEmpty &&
        widget.store.sources.length > 1) {
      // 旧版「全部站源」在单选模型下没有对应状态，但我们已恢复多选，
      // 这里保持多选语义：若历史配置是 allSources，不强制改写，交给用户再次选择。
    }
    // 进软件优先打开「首页」（竖滑视频流）。
    _tab = widget.initialTab ?? _feedIndex;
    if (!widget.store.canDownload && _tab == _downloadIndex) {
      _tab = _feedIndex;
    }
    _downloadVisited = _tab == _downloadIndex;
    _browser = CatalogBrowser(widget.repository);
    _updater = LibraryUpdater(
      widget.repository,
      widget.store,
      onCatalogChanged: _catalogUpdated,
    )..addListener(_updateChanged);
    _updater.startWatching();
    // 剧库自适应自动更新：打开 App 后延迟 2 分钟，默认每 2 小时一轮，
    // 连续无新剧后降级为 6 小时；设置里可完全关闭。
    _autoUpdater = CatalogAutoUpdater(widget.repository, widget.store)
      ..addListener(_autoUpdateChanged)
      ..start();
    widget.repository.catalogUpdates.addListener(_metadataChanged);
    if (widget.store.sources.isNotEmpty) {
      _load(useCache: true);
      _loadCategories();
    } else {
      _loading = false;
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _updater.removeListener(_updateChanged);
    _updater.dispose();
    _autoUpdater.removeListener(_autoUpdateChanged);
    _autoUpdater.dispose();
    _feedRefresh.dispose();
    _cacheRefreshTimer?.cancel();
    _exitTimer?.cancel();
    widget.repository.catalogUpdates.removeListener(_metadataChanged);
    _generation++;
    _categoryGeneration++;
    unawaited(_browser.cancel());
    unawaited(widget.repository.cancelSuggestions());
    _debounce?.cancel();
    _search.dispose();
    _scroll.dispose();
    for (final node in _navFocusNodes.values) {
      node.dispose();
    }
    super.dispose();
  }

  void _metadataChanged() {
    final drama = widget.repository.catalogUpdates.latest;
    if (!mounted || drama == null || !widget.store.allowsSource(drama.source)) {
      return;
    }
    _browser.updateDrama(drama);
    setState(() {
      _items = [
        for (final item in _items)
          item.id == drama.id ? item.merge(drama) : item,
      ];
    });
    unawaited(saveUserChange(context, () => widget.store.refreshDrama(drama)));
  }

  Future<void> _load({
    bool more = false,
    bool useCache = false,
    bool force = false,
    bool cacheOnly = false,
  }) async {
    if (_showRecommendations) return;
    if (more && (_loading || _loadingMore || !_hasMore)) return;
    final generation = ++_generation;
    final group = _group;
    final query = _onlineSearch ? _search.text.trim() : '';
    // 纯字母关键词既可能是拼音，也可能是站源本身支持的英文标题。
    // 0.8.4 曾在这里直接 return，只过滤“浏览过的本地缓存”，导致未缓存剧目
    // 永远搜不到，同时也误伤真正的英文搜索。现在改为双路并行：
    // 1. 原关键词继续交给站源搜索；
    // 2. 本地缓存额外做完整拼音 / 首字母匹配，最终在 _visible 中合并去重。
    final pinyinQuery = isPinyinQuery(query) ? query : '';
    _pinyinQuery = pinyinQuery;
    _pinyinPool = pinyinQuery.isEmpty
        ? const []
        : _browser.cachedLibrary(group.sources.map((site) => site.id));
    setState(() {
      _error = null;
      if (query.isNotEmpty) _categorySelections[group.id] = '';
      _failedMore = false;
      if (more) {
        _loadingMore = true;
      } else {
        _loading = true;
        _loadingMore = false;
        if (query != _submittedQuery) _items = [];
      }
    });
    void accept(CatalogPage result) {
      if (!mounted || generation != _generation) return;
      setState(() {
        _items = result.items;
        _hasMore = result.hasMore;
        _submittedQuery = query;
        // 只要已经有内容（无论是磁盘缓存还是网络返回），就立刻结束加载态。
        // 之前写成 cached && !result.fresh 会让"缓存命中但待刷新"时
        // 仍然显示加载指示，用户感知就是"一直在转圈"。
        // 新鲜度由后台刷新负责，不再阻塞界面。
        _loading = false;
        _loadingMore = false;
        _error = result.warning.isEmpty ? null : result.warning;
      });
      unawaited(
        saveUserChange(context, () => widget.store.refreshDramas(result.items)),
      );
    }

    try {
      final result = await _browser.load(
        group,
        category: _category,
        query: query,
        more: more,
        useCache: useCache,
        force: force,
        cacheOnly: cacheOnly,
        onCached: (result) => accept(result),
      );
      accept(result);
    } catch (error) {
      if (!mounted || generation != _generation) return;
      setState(() {
        _loading = false;
        _loadingMore = false;
        _error = error.toString();
        _failedMore = more;
      });
    }
  }

  void _changeSource(
    SourceSite source, {
    bool allSources = false,
    bool force = false,
  }) {
    // 0.8.10：`force` 用于「从多选退到单选」——
    // 那条路径会先把 `_multiSources` 清空再调进来，于是"目标源 == 原来的单选源"
    // 会命中这里的提前返回，页面就继续停在多源聚合结果上不刷新
    // （用户反馈"去掉一个站源后页面不刷新为最新配置"）。
    if (!force &&
        _source.id == source.id &&
        _allSources == allSources &&
        _multiSources.isEmpty) {
      return;
    }
    _debounce?.cancel();
    _search.clear();
    setState(() {
      _showRecommendations = false;
      _selectionMode = false;
      _selectedDramas.clear();
      _updateNotice = false;
      _source = source;
      _allSources = allSources;
      _multiSources = const [];
      _items = [];
      _hasMore = true;
      _submittedQuery = '';
      _pinyinQuery = '';
      _pinyinPool = const [];
      _error = null;
    });
    unawaited(
      saveUserChange(context, () async {
        await widget.store.setMultiSources(const []);
        await widget.store.setCatalogSource(source.id, allSources: allSources);
      }),
    );
    if (_scroll.hasClients) {
      _scroll.jumpTo(0);
    }
    _load(useCache: true);
    _loadCategories();
  }

  /// 退出搜索态、回到常规首页（0.8.4）。
  ///
  /// 电视搜索结果页的「清除搜索」和返回键都走这里。**不走** `_searchChanged`
  /// 那 300ms 防抖：遥控器上按一下要等三分之一秒才有反应，观感就是"没按上"。
  void _clearSearch() {
    _debounce?.cancel();
    _selectionMode = false;
    _selectedDramas.clear();
    _search.clear();
    setState(() {
      _pinyinQuery = '';
      _pinyinPool = const [];
    });
    if (_onlineSearch) _load();
  }

  void _searchChanged(String query) {
    _debounce?.cancel();
    _selectionMode = false;
    _selectedDramas.clear();
    if (_onlineSearch && (_loading || _loadingMore)) {
      _generation++;
      unawaited(_browser.cancel());
      _loading = _loadingMore = false;
    }
    // 关键词被清空（用户手动删光 / 点了取消）就退出搜索态，
    // 否则 `_pinyinQuery` 会一直把列表钉在旧结果上。
    if (query.trim().isEmpty && _pinyinQuery.isNotEmpty) {
      _pinyinQuery = '';
      _pinyinPool = const [];
    }
    setState(() {});
    if (_onlineSearch && query.trim().isEmpty) {
      _debounce = Timer(const Duration(milliseconds: 300), () => _load());
    }
  }

  void _submitSearch(String query) {
    if (_showRecommendations) {
      _showRecommendations = false;
      _categorySelections[_group.id] = '';
    }
    _selectionMode = false;
    _selectedDramas.clear();
    _search.text = query.trim();
    _debounce?.cancel();
    if (_search.text.isNotEmpty) {
      unawaited(
        saveUserChange(
          context,
          () => widget.store.rememberSearch(_search.text),
        ),
      );
    }
    // 拼音/首字母（0.8.4）：`_load` 会识别纯字母关键词，同时执行站源搜索和
    // 本地拼音匹配，并在 `_visible` 中合并去重。
    if (_onlineSearch) {
      _load();
    } else if (isPinyinQuery(_search.text)) {
      // 不联网的站源本来就在 `_items` 里筛，这里只是把「搜索态」标出来，
      // 好让电视切到搜索结果页——否则用户看不出搜索到底生效了没有。
      setState(() => _pinyinQuery = _search.text.trim());
    } else {
      setState(() {
        _pinyinQuery = '';
        _pinyinPool = const [];
      });
    }
  }

  Future<void> _chooseCatalogView() async {
    final selected = await chooseCatalogView(context, widget.store.catalogView);
    if (selected != null && mounted) {
      await saveUserChange(
        context,
        () => widget.store.setCatalogView(selected),
      );
      if (mounted && _scroll.hasClients) _scroll.jumpTo(0);
    }
  }

  void _openDrama(Drama drama, {bool resume = false, bool download = false}) {
    _pauseCatalog();
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => DetailScreen(
          drama: drama,
          repository: widget.repository,
          store: widget.store,
          resumeOnOpen: resume,
          downloadOnOpen: download,
        ),
      ),
    );
  }

  /// 进入竖滑「刷剧」模式（0.8.3）。
  ///
  /// 电视首页是横向海报行，竖滑流改由这个独立路由承载：
  /// 返回键回到电视首页，竖滑流内部还有一层「退出播放」确认框。
  void _startFeed() {
    _pauseCatalog();
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => VerticalFeedScreen(
          repository: widget.repository,
          store: widget.store,
          sources: _group.sources,
          hongguoGenres: _feedHongguoGenres,
          playerFactory: widget.feedPlayerFactory,
        ),
      ),
    );
  }

  void _changeTab(int tab) => setState(() {
    _tab = tab;
    if (tab == _downloadIndex) _downloadVisited = true;
    _selectionMode = false;
    _selectedDramas.clear();
  });

  /// 前后台切换（0.2.26 新增；0.2.27 修正）。
  ///
  /// 0.2.27 修正（用户真机反馈第 1 条）：
  /// 用户明确："返回到桌面（应用仍在后台存活）不算退后台，不该触发刷新；
  /// 只有应用被**彻底退出进程后重新冷启动**才算重新启动、才正常刷新"。
  ///
  /// 原实现在 `resumed && _wentToBackground` 时 `_feedRefresh.value++`，
  /// 但真机上"从桌面切回应用"会经历 `inactive/paused → resumed`，
  /// 于是每次从桌面回来都会误刷新一次首页。
  ///
  /// 现在**彻底移除生命周期驱动的刷新**：
  /// - 从桌面切回（进程存活）→ 不刷新；
  /// - 应用被杀后冷启动 → 走 `main` 初始化，首页自然首次加载（属正常刷新）。
  /// - 手动刷新入口保留：**双击「首页」标签**（`_onTabDoubleTap`）。
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // 0.2.27：不再根据前后台状态刷新首页。
    // 保留该方法以免影响 `WidgetsBindingObserver` 的注册/注销；
    // 若日后需要"真正退后台才刷新"，可在此处判断 `detached`（进程结束）再处理。
  }

  /// 双击标签：目前只对「首页」生效——重新随机一批视频。
  void _onTabDoubleTap(int tab) {
    if (tab != _feedIndex) return;
    _changeTab(_feedIndex);
    _feedRefresh.value++;
  }

  /// 「首页」标签在所有标签中固定排第一位。
  static const _feedIndex = 0;

  /// 各功能标签在导航中的下标（「首页」占 0，其余依次后移）。
  int get _discoverIndex => 1;
  int get _followIndex => 2;
  int get _historyIndex => 3;
  int get _downloadIndex => 4;

  void _cancelSelection() => setState(() {
    _selectionMode = false;
    _selectedDramas.clear();
  });

  void _selectDrama(Drama drama) {
    if (!widget.store.canDownload ||
        !widget.repository.supportsDownloads ||
        !widget.store.allowsSource(drama.source)) {
      return;
    }
    if (!_selectedDramas.containsKey(drama.id) &&
        _selectedDramas.length >= BatchDownloads.maxDramas) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('一次最多选择 50 部短剧，请分批下载')));
      return;
    }
    setState(() {
      _selectionMode = true;
      if (_selectedDramas.remove(drama.id) == null) {
        _selectedDramas[drama.id] = drama;
      }
    });
  }

  void _downloadSelected() {
    if (!widget.store.canDownload || _selectedDramas.isEmpty) return;
    _pauseCatalog();
    Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => BatchDownloadScreen(
          repository: widget.repository,
          store: widget.store,
          dramas: _selectedDramas.values.toList(),
        ),
      ),
    );
  }

  void _dramaActions(Drama drama) => showDramaActions(
    context,
    drama: drama,
    store: widget.store,
    onContinue: () => _openDrama(drama, resume: true),
    onDownload: widget.repository.supportsDownloads && widget.store.canDownload
        ? () => _openDrama(drama, download: true)
        : null,
    onSelect: widget.repository.supportsDownloads && widget.store.canDownload
        ? () => _selectDrama(drama)
        : null,
  );

  Widget _catalogTile(
    Drama drama, {
    FocusNode? focusNode,
    VoidCallback? onFocus,
  }) {
    final following = widget.store.following(drama.id);
    final canSelect =
        widget.store.canDownload && widget.repository.supportsDownloads;
    return DramaTile(
      key: ValueKey(drama.id),
      drama: drama,
      repository: widget.repository,
      focusNode: focusNode,
      onFocus: onFocus,
      onTap: () => _selectionMode ? _selectDrama(drama) : _openDrama(drama),
      onLongPress: canSelect ? () => _selectDrama(drama) : null,
      onMore: () => _dramaActions(drama),
      actions: DramaActionButton(
        drama: drama,
        onPressed: () => _dramaActions(drama),
      ),
      selected: _selectionMode ? _selectedDramas.containsKey(drama.id) : null,
      badge: following == null
          ? null
          : '${following.status.label}${following.newEpisodes > 0 ? ' · 更新 ${following.newEpisodes} 集' : ''}',
    );
  }

  void _pauseCatalog() {
    _debounce?.cancel();
    _generation++;
    unawaited(_browser.cancel());
    unawaited(widget.repository.cancelSuggestions());
    setState(() {
      _loading = false;
      _loadingMore = false;
    });
  }

  bool get _supportsVipFilter =>
      _group.sources.any((source) => source.id == 'huangdou');
  bool get _hideVip => _supportsVipFilter && widget.store.hideVip;

  List<Drama> get _visible {
    final query = _search.text.trim().toLowerCase();

    bool allowed(Drama drama) {
      if (!widget.store.allowsSource(drama.source)) return false;
      if (_category.startsWith('local:') &&
          categoryName(drama.category) != _category.substring(6)) {
        return false;
      }
      if (_hideVip && drama.source == 'huangdou' && drama.vip) return false;
      return true;
    }

    // 拼音搜索：在线站源的原始搜索结果 + 本地缓存中的拼音命中结果合并去重。
    // 这样 `hjdnz` 能本地命中中文标题，`love` 这类纯英文关键词也不会被
    // 错当成拼音而丢掉服务端真实结果。
    if (_pinyinQuery.isNotEmpty) {
      final merged = <String, Drama>{};
      if (_onlineSearch) {
        for (final drama in _items) {
          if (allowed(drama)) merged['${drama.source}/${drama.id}'] = drama;
        }
      }
      final localPool = _onlineSearch ? _pinyinPool : _items;
      for (final drama in localPool) {
        if (!allowed(drama) || !matchesPinyin(drama.title, _pinyinQuery)) {
          continue;
        }
        merged.putIfAbsent('${drama.source}/${drama.id}', () => drama);
      }
      return sortCatalog(merged.values, widget.store.catalogView);
    }

    return sortCatalog(
      _items.where((drama) {
        if (!allowed(drama)) return false;
        if (query.isEmpty) return true;
        // 在线搜索结果已经由站源筛选；本地站源仍按标题/描述/标签过滤。
        if (_onlineSearch) return true;
        return matchesDramaQuery(drama, query);
      }),
      widget.store.catalogView,
    );
  }

  /// 电视左侧菜单（0.8.7）。
  ///
  /// 顶部栏去掉后，站源、搜索、设置都收进这里：遥控器只要按左键就能
  /// 在"看什么"和"去哪儿"之间移动，顶部的高度全留给内容。
  Widget _televisionNav() {
    final entries = <(IconData, String, int)>[
      (Icons.smart_display_rounded, '首页', _feedIndex),
      (Icons.explore_rounded, '发现', _discoverIndex),
      (Icons.bookmark_rounded, '追剧', _followIndex),
      (Icons.history_rounded, '最近观看', _historyIndex),
      if (widget.store.canDownload)
        (Icons.download_rounded, '下载', _downloadIndex),
    ];
    Widget item(
      IconData icon,
      String label, {
      required Key key,
      required VoidCallback onPressed,
      bool selected = false,
      bool autofocus = false,
      FocusNode? focusNode,
    }) => Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: RemoteButton(
        key: key,
        label: label,
        icon: icon,
        selected: selected,
        autofocus: autofocus,
        focusNode: focusNode,
        onPressed: onPressed,
      ),
    );
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(8, 14, 8, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final (index, entry) in entries.indexed)
            item(
              entry.$1,
              entry.$2,
              key: ValueKey('tv-nav-${entry.$3}'),
              selected: _tab == entry.$3,
              autofocus: index == 0,
              // 每项一个稳定的焦点节点：返回键退回首页时要把光标移回「首页」
              // （见 _televisionBack / _focusNavItem）。
              focusNode: _navFocusNode(entry.$3),
              onPressed: () {
                // 电视没有"双击标签"这个手势：已经在当前页时再按一次 = 刷新剧库。
                if (_tab == entry.$3) {
                  if (widget.store.sources.isNotEmpty) _refreshCatalog();
                  return;
                }
                _changeTab(entry.$3);
              },
            ),
          const Divider(height: 18),
          item(
            Icons.search_rounded,
            '搜索',
            key: const ValueKey('tv-nav-search'),
            onPressed: () => unawaited(_televisionSearch()),
          ),
          if (widget.store.sources.isNotEmpty)
            item(
              Icons.live_tv_rounded,
              // 0.8.11：侧栏固定写「站源」。
              //
              // 多选时 `_group.name` 是「剧场+AI+旧版」这种拼接串，
              // 侧栏宽度只有十几个字，直接顶出去（用户反馈"外面显示 X果+X果，
              // 超长不好展示"）。点进去就是站源设置页，名字不影响判断。
              '站源',
              key: const ValueKey('tv-nav-source'),
              onPressed: _openSourceSheet,
            ),
          item(
            Icons.settings_rounded,
            '设置',
            key: const ValueKey('tv-nav-settings'),
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute<void>(
                builder: (_) => SettingsScreen(
                  repository: widget.repository,
                  store: widget.store,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  List<Widget> _appBarActions(
    BuildContext context,
    BoxConstraints constraints,
  ) => [
    if (_selectionMode) ...[
      TextButton(
        key: const ValueKey('clear-catalog-selection'),
        onPressed: _selectedDramas.isEmpty
            ? null
            : () => setState(_selectedDramas.clear),
        child: const Text('清空'),
      ),
      TextButton(
        key: const ValueKey('cancel-catalog-selection'),
        onPressed: _cancelSelection,
        child: const Text('取消'),
      ),
    ] else ...[
      // 0.7.2（XQ-0106）：首页剧类筛选入口。仅"单独红果"时可用。
      // 0.8.3：电视首页是海报行，剧类筛选只对竖滑流有意义，电视隐藏。
      if (_tab == _feedIndex)
        IconButton(
          key: const ValueKey('feed-genre'),
          tooltip: _feedGenreEnabled
              ? '剧类 · $_feedGenreLabel'
              : '剧类筛选仅红果单独选择时可用',
          onPressed: _openFeedGenreSheet,
          color: _feedGenreEnabled && _feedGenre != 'all'
              ? Theme.of(context).colorScheme.primary
              : null,
          icon: const Icon(Icons.theater_comedy_outlined),
        ),
      if (_tab == _followIndex)
        IconButton(
          key: const ValueKey('follow-lan-sync'),
          tooltip: '追剧同步',
          onPressed: () => openLanSync(context),
          icon: const Icon(Icons.sync_rounded),
        ),
      if (_tab == _discoverIndex) ...[
        if (!_showRecommendations && constraints.maxWidth >= 360)
          IconButton(
            tooltip: '排序与筛选 · ${widget.store.catalogView.sort.label}',
            onPressed: _chooseCatalogView,
            color:
                widget.store.catalogView.sort != CatalogSort.source ||
                    widget.store.catalogView.release.isNotEmpty
                ? Theme.of(context).colorScheme.primary
                : null,
            icon: const Icon(Icons.sort_rounded),
          ),
        if (constraints.maxWidth >= 360)
          IconButton(
            key: const ValueKey('open-rankings'),
            tooltip: '榜单',
            onPressed: widget.store.sources.isEmpty
                ? null
                : _openRankings,
            icon: const Icon(Icons.leaderboard_outlined),
          ),
        // 多选下载（0.8.3）：电视上隐藏。
        //
        // 电视只适合"挑一部看"，批量下载 20 部短剧是手机/电脑的活；
        // 而且选择模式的确认按钮在电视上要跨整个内容区去够，容易误触。
        if (!_showRecommendations &&
            constraints.maxWidth >= 360 &&
            widget.store.canDownload &&
            widget.repository.supportsDownloads)
          IconButton(
            key: const ValueKey('select-catalog-dramas'),
            tooltip: '多选下载',
            onPressed: () => setState(() => _selectionMode = true),
            icon: const Icon(Icons.checklist_rounded),
          ),
        IconButton(
          key: const ValueKey('toggle-search'),
          tooltip: _searchVisible ? '收起搜索' : '搜索',
          icon: Icon(
            _searchVisible
                ? Icons.search_off_rounded
                : Icons.search_rounded,
          ),
          onPressed: _toggleSearch,
        ),
      ],
      if (_tab == _discoverIndex &&
          !_showRecommendations &&
          constraints.maxWidth >= 400)
        RefreshAction(
          key: const ValueKey('catalog-refresh'),
          loading:
              _loading ||
              _loadingMore ||
              _categoriesLoading ||
              _group.sources.any(
                (source) => _updater.busy(source.id),
              ),
          tooltip: '更新剧库',
          onPressed: widget.store.sources.isEmpty
              ? null
              : _refreshCatalog,
        ),
      PopupMenuButton<String>(
        tooltip: '更多',
        onSelected: (value) {
          if (value == 'update') {
            _refreshCatalog();
          } else if (value == 'sort') {
            _chooseCatalogView();
          } else if (value == 'rankings') {
            _openRankings();
          } else if (value == 'select') {
            setState(() => _selectionMode = true);
          } else if (value == 'sources') {
            _manageSources();
          } else if (value == 'settings') {
            Navigator.push(
              context,
              MaterialPageRoute<void>(
                builder: (_) => SettingsScreen(
                  repository: widget.repository,
                  store: widget.store,
                ),
              ),
            );
          } else if (value == 'users') {
            Navigator.push(
              context,
              MaterialPageRoute<void>(
                builder: (_) => ProfilesScreen(store: widget.store),
              ),
            );
          } else if (value == 'display') {
            _chooseDisplayMode();
          } else if (value == 'about') {
            showAboutDialog(
              context: context,
              applicationName: appName,
              applicationVersion: AppLayout.versionOf(context),
              applicationIcon: const Icon(
                Icons.play_circle_filled_rounded,
                size: 48,
                color: Color(0xFFFF765F),
              ),
              children: [
                const Text('独立运行，打开即可浏览和播放。观看记录与追剧收藏保存在当前设备。'),
                const SizedBox(height: 12),
                // 作者信息（0.2.30 新增，用户要求）：
                // 名字蓝色可点击，跳转各自 GitHub 页面，并标明身份。
                const AboutCredit(
                  name: 'Topkill',
                  role: '开源项目 guoapp3 原作者',
                  url: 'https://github.com/Topkill/guoapp3',
                ),
                const AboutCredit(
                  name: '软萌小正太',
                  role: '原改版与维护',
                  url: 'https://github.com/kuoluoss/zhenguojian',
                ),
                const AboutCredit(
                  name: 'ai0by',
                  role: 'TV 版适配与维护',
                  url: 'https://github.com/ai0by',
                ),
                const AboutCredit(
                  name: '项目仓库',
                  role: '真果鉴 TV GitHub',
                  url: 'https://github.com/ai0by/zhenguojianTV',
                ),
              ],
            );
          }
        },
        itemBuilder: (_) => [
          if (_tab == _discoverIndex &&
              !_showRecommendations &&
              constraints.maxWidth < 400)
            PopupMenuItem(
              value: 'update',
              enabled:
                  widget.store.sources.isNotEmpty &&
                  !_group.sources.any(
                    (source) => _updater.busy(source.id),
                  ),
              child: const Text('更新剧库'),
            ),
          if (_tab == _discoverIndex &&
              !_showRecommendations &&
              constraints.maxWidth < 360) ...[
            const PopupMenuItem(value: 'sort', child: Text('排序与筛选')),
            const PopupMenuItem(value: 'rankings', child: Text('榜单')),
            if (widget.store.canDownload &&
                widget.repository.supportsDownloads)
              const PopupMenuItem(
                value: 'select',
                child: Text('多选下载'),
              ),
          ],
          if (widget.repository.supportsSourceManagement)
            const PopupMenuItem(
              value: 'sources',
              child: Text('站源管理'),
            ),
          const PopupMenuItem(value: 'users', child: Text('用户管理')),
          const PopupMenuItem(
            value: 'settings',
            child: Text('设置与备份'),
          ),
          // 「界面模式」入口（0.8.0 恢复）。
          //
          // 0.2.26 曾隐藏此入口并强制手机版渲染；现恢复 TV 版，
          // 需要保留手动切换能力：部分电视盒子（定制 launcher、
          // 投影仪、模拟器）不会上报 leanback / television，
          // 自动识别失效时用户可在这里强制切到「电视 / 遥控器」。
          const PopupMenuItem(value: 'display', child: Text('界面模式')),
          const PopupMenuItem(
            value: 'about',
            child: Text('关于$appName'),
          ),
        ],
      ),
    ],
    const SizedBox(width: 8),
  ];

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (context, _) => LayoutBuilder(
      builder: (context, constraints) {
        final television = AppLayout.isTelevision(context);
        final desktop = constraints.maxWidth >= 840;
        final scaffold = Scaffold(
          // 0.8.7：电视不要顶部栏。站源/搜索/设置都挪进左侧菜单，
          // 顶部省下来的高度全给海报和剧集格子。
          appBar: television
              ? null
              : AppBar(
                  titleSpacing: 12,
                  title: _selectionMode
                      ? const Text(
                          '选择短剧',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        )
                      : _tab == _feedIndex || _tab == _discoverIndex
                      ? _sourceMenu()
                      : const Text(appName),
                  actions: _appBarActions(context, constraints),
                ),
          body: SafeArea(
            top: false,
            child: Row(
              children: [
                if (television) ...[
                  SizedBox(width: 132, child: _televisionNav()),
                  const VerticalDivider(width: 1),
                ] else if (desktop) ...[
                  NavigationRail(
                    selectedIndex: _tab,
                    onDestinationSelected: _changeTab,
                    labelType: NavigationRailLabelType.all,
                    groupAlignment: -.8,
                    destinations: [
                      NavigationRailDestination(
                        icon: Icon(Icons.smart_display_outlined),
                        selectedIcon: Icon(Icons.smart_display_rounded),
                        label: Text('首页'),
                      ),
                      NavigationRailDestination(
                        icon: Icon(Icons.explore_outlined),
                        selectedIcon: Icon(Icons.explore),
                        label: Text('发现'),
                      ),
                      NavigationRailDestination(
                        icon: Icon(Icons.bookmark_border_rounded),
                        selectedIcon: Icon(Icons.bookmark_rounded),
                        label: Text('追剧'),
                      ),
                      NavigationRailDestination(
                        icon: Icon(Icons.history_rounded),
                        label: Text('最近观看'),
                      ),
                      if (widget.store.canDownload)
                        NavigationRailDestination(
                          icon: Icon(Icons.download_outlined),
                          selectedIcon: Icon(Icons.download_rounded),
                          label: Text('下载'),
                        ),
                    ],
                  ),
                  const VerticalDivider(width: 1, thickness: 1),
                ],
                Expanded(
                  // 0.2.26 修正（用户反馈"每次回到首页都会重新刷新"）：
                  //
                  // 原先这里用 `_tab == _feedIndex ? A : _tab == B ? ...` 的三元表达式
                  // 直接构建，切换标签时**未选中的页面会被销毁**，切回来时重建，
                  // 于是首页（竖滑流）每次都重新加载、丢失播放进度与滚动位置。
                  //
                  // 改用 `IndexedStack`：所有页面同时存在树中，只显示当前一个。
                  // 这样首页（含播放器）在切标签时保持存活，不再重建。
                  //
                  // 刷新时机改为（用户明确要求）：
                  // - 手动双击「首页」标签（_onTabDoubleTap → _feedRefresh++）；
                  // - 退到后台再回来（前后台切换时首页会因内存回收自然重载，
                  //   同时 _feedRefresh 由生命周期兜底触发）。
                  child: IndexedStack(
                    index: _tab,
                    children: [
                      // 首页（0.8.3）：电视和手机是两个完全不同的东西。
                      //
                      // 手机：竖滑短视频流（刷到就播，上下滑换剧）。
                      // 电视：横向海报行（继续观看 / 热门 / 分类），
                      //       竖滑流降级为「开始刷剧」按钮推进去的独立页面。
                      //
                      // 为什么不在电视上继续用竖滑流：遥控器上下键只能"下一部"，
                      // 用户既看不到剧库里有什么，也回不到刚才看过的那部；
                      // 而且一进应用就自动播，电视上很吵。
                      television
                          ? TvHomeScreen(
                              repository: widget.repository,
                              items: _visible,
                              history: widget.store.history,
                              loading: _loading,
                              searchQuery: _pinyinQuery,
                              onClearSearch: _clearSearch,
                              emptyMessage: widget.store.sources.isEmpty
                                  ? '暂无可用站源，请联系管理员开放'
                                  : null,
                              onOpen: _openDrama,
                              onResume: (entry) =>
                                  _openDrama(entry.drama, resume: true),
                              onStartFeed: _startFeed,
                              onBrowse: () => _changeTab(_discoverIndex),
                              onSearch: () => unawaited(_televisionSearch()),
                              onRefresh: _refreshCatalog,
                              onContentReady: widget.onFeedReady,
                            )
                          : VerticalFeedScreen(
                              repository: widget.repository,
                              store: widget.store,
                              embedded: true,
                              sources: _group.sources,
                              hongguoGenres: _feedHongguoGenres,
                              playerFactory: widget.feedPlayerFactory,
                              refreshSignal: _feedRefresh,
                              onReady: widget.onFeedReady,
                              active: _tab == _feedIndex,
                            ),
                      // 发现页。
                      widget.store.sources.isEmpty
                          ? const StatusPanel(
                              title: '暂无可用站源',
                              message: '请联系管理员为当前用户开放站源。',
                            )
                          : _catalog(selectionInBody: desktop || television),
                      // 追剧页。
                      SavedLibrary(
                        key: const ValueKey('saved-tab-follow'),
                        repository: widget.repository,
                        store: widget.store,
                        history: false,
                        onOpen: _openDrama,
                        onContinue: (drama) => _openDrama(drama, resume: true),
                        onDownload:
                            widget.repository.supportsDownloads &&
                                widget.store.canDownload
                            ? (drama) => _openDrama(drama, download: true)
                            : null,
                      ),
                      // 最近观看。
                      SavedLibrary(
                        key: const ValueKey('saved-tab-history'),
                        repository: widget.repository,
                        store: widget.store,
                        history: true,
                        onOpen: _openDrama,
                        onContinue: (drama) => _openDrama(drama, resume: true),
                        onDownload:
                            widget.repository.supportsDownloads &&
                                widget.store.canDownload
                            ? (drama) => _openDrama(drama, download: true)
                            : null,
                      ),
                      // 下载。
                      if (widget.store.canDownload)
                        _downloadVisited
                            ? DownloadsScreen(
                                repository: widget.repository,
                                store: widget.store,
                                embedded: true,
                                active: _tab == _downloadIndex,
                              )
                            : const SizedBox.shrink(),
                    ],
                  ),
                ),
              ],
            ),
          ),
          bottomNavigationBar: desktop || television
              ? null
              : _selectionMode
              ? _selectionBar()
              : AppBottomNavigation(
                  selectedIndex: _tab,
                  onDestinationSelected: _changeTab,
                  onDestinationDoubleTap: _onTabDoubleTap,
                  destinations: [
                    NavigationDestination(
                      icon: Icon(Icons.smart_display_outlined),
                      selectedIcon: Icon(Icons.smart_display_rounded),
                      label: '首页',
                    ),
                    NavigationDestination(
                      icon: Icon(Icons.explore_outlined),
                      selectedIcon: Icon(Icons.explore),
                      label: '发现',
                    ),
                    NavigationDestination(
                      icon: Icon(Icons.bookmark_border_rounded),
                      selectedIcon: Icon(Icons.bookmark_rounded),
                      label: '追剧',
                    ),
                    NavigationDestination(
                      icon: Icon(Icons.history_rounded),
                      label: '最近观看',
                    ),
                    if (widget.store.canDownload)
                      NavigationDestination(
                        icon: Icon(Icons.download_outlined),
                        selectedIcon: Icon(Icons.download_rounded),
                        label: '下载',
                      ),
                  ],
                ),
        );
        if (!television && !_selectionMode) return scaffold;
        return PopScope(
          // 电视（0.8.1）：所有返回都先交给 `_televisionBack` 逐层退让。
          // 手机保持原行为：不在选择模式时直接交给系统退出。
          canPop: !_selectionMode && !television,
          onPopInvokedWithResult: (didPop, result) {
            if (didPop) return;
            unawaited(_televisionBack());
          },
          child: CallbackShortcuts(
            bindings: {
              const SingleActivator(LogicalKeyboardKey.escape):
                  _keyboardBack,
              const SingleActivator(LogicalKeyboardKey.goBack): _keyboardBack,
            },
            child: scaffold,
          ),
        );
      },
    ),
  );

  Widget _catalog({required bool selectionInBody}) {
    final items = _visible;
    final television = AppLayout.isTelevision(context);
    return Column(
      children: [
        if (_searchVisible && !television)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
            child: SearchInput(
              key: ValueKey('search-${_group.id}'),
              controller: _search,
              autofocus: true,
              hint: _searchHint,
              suggestions: _searchSuggestions
                  ? widget.repository.suggestions
                  : null,
              onChanged: _searchChanged,
              onCancel: () => unawaited(widget.repository.cancelSuggestions()),
              onSearch: _submitSearch,
            ),
          ),
        if (_searchVisible &&
            _search.text.trim().isEmpty &&
            widget.store.recentSearches.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Row(
              children: [
                Expanded(
                  child: SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Row(
                      children: [
                        for (final query in widget.store.recentSearches)
                          Padding(
                            padding: const EdgeInsets.only(right: 8),
                            child: ActionChip(
                              avatar: const Icon(
                                Icons.history_rounded,
                                size: 16,
                              ),
                              label: Text(query),
                              onPressed: () => _submitSearch(query),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
                IconButton(
                  tooltip: '清空最近搜索',
                  onPressed: () =>
                      saveUserChange(context, widget.store.clearRecentSearches),
                  icon: const Icon(Icons.delete_outline_rounded, size: 20),
                ),
              ],
            ),
          ),
        CatalogFilters(
          key: ValueKey('filters-${_group.id}'),
          categories: _displayCategories,
          category: _displayCategory,
          error: _categoriesError,
          onCategory: _changeCategory,
          onRetry: () => _loadCategories(force: true),
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              // 0.8.7：电视没有顶部栏了，榜单/排序/更新并到剧类行右侧。
              if (television) ...[
                RemoteButton(
                  key: const ValueKey('tv-catalog-rankings'),
                  label: '榜单',
                  icon: Icons.leaderboard_outlined,
                  onPressed: widget.store.sources.isEmpty
                      ? null
                      : _openRankings,
                ),
                const SizedBox(width: 6),
                RemoteButton(
                  key: const ValueKey('tv-catalog-sort'),
                  label: '排序',
                  icon: Icons.sort_rounded,
                  onPressed: _chooseCatalogView,
                ),
                const SizedBox(width: 6),
                RemoteButton(
                  key: const ValueKey('tv-catalog-refresh'),
                  label: '更新',
                  icon: Icons.refresh_rounded,
                  onPressed: widget.store.sources.isEmpty
                      ? null
                      : _refreshCatalog,
                ),
                const SizedBox(width: 6),
              ],
              if (_supportsVipFilter)
                IconButton(
                  tooltip: widget.store.hideVip ? 'VIP：隐藏' : 'VIP：显示',
                  onPressed: () => saveUserChange(
                    context,
                    () => widget.store.setHideVip(!widget.store.hideVip),
                  ),
                  icon: VipIcon(hidden: widget.store.hideVip),
                ),
            ],
          ),
        ),
        if (_showRecommendations)
          Expanded(
            child: GestureDetector(
              onHorizontalDragEnd: television ? null : _swipeCategory,
              child: RecommendationsScreen(
                repository: widget.repository,
                store: widget.store,
                embedded: true,
              ),
            ),
          )
        else ...[
          if (widget.repository.supportsSourceManagement &&
              (_updateNotice ||
                  _group.sources.any((source) => _updater.busy(source.id))))
            _updateStatus(),
          if (_loading && _items.isNotEmpty)
            const LinearProgressIndicator(minHeight: 2),
          if (_error != null && _items.isNotEmpty)
            Container(
              margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
              padding: const EdgeInsets.fromLTRB(12, 8, 6, 8),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.errorContainer,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      _error!,
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 12,
                        color: Theme.of(context).colorScheme.onErrorContainer,
                      ),
                    ),
                  ),
                  TextButton(
                    onPressed: _loading || _loadingMore
                        ? null
                        : () => _load(more: _failedMore, force: true),
                    child: const Text('重试'),
                  ),
                  if (widget.repository.supportsSourceManagement)
                    IconButton(
                      tooltip: '站源诊断',
                      onPressed: _manageSources,
                      icon: const Icon(Icons.network_check),
                    ),
                ],
              ),
            ),
          Expanded(
            child: GestureDetector(
              onHorizontalDragEnd: television ? null : _swipeCategory,
              child: _loading && _items.isEmpty
                  ? const DramaGridSkeleton()
                  : _items.isEmpty && _error != null
                  ? StatusPanel(
                      title: '暂时无法加载',
                      message: _error!,
                      onRetry: () => _load(force: true),
                      secondaryAction:
                          widget.repository.supportsSourceManagement
                          ? TextButton(
                              onPressed: _manageSources,
                              child: const Text('站源诊断'),
                            )
                          : null,
                      icon: Icons.wifi_off_rounded,
                    )
                  : items.isEmpty
                  ? StatusPanel(
                      title: '没有找到匹配的短剧',
                      message: _hideVip
                          ? '可以换个搜索词，或显示 VIP 内容。'
                          : widget.store.sources.length > 1
                          ? '可以换个搜索词或切换站源。'
                          : '可以换个搜索词，或刷新后重试。',
                      onRetry:
                          _hasMore &&
                              !_loadingMore &&
                              (!_onlineSearch ||
                                  _search.text.isEmpty ||
                                  _group.sources.any(
                                    (source) => source.pagedSearch,
                                  ))
                          ? () => _load(more: true)
                          : null,
                      action: '加载更多',
                    )
                  : LayoutBuilder(
                      builder: (context, constraints) {
                        if (television) {
                          return _televisionGrid(
                            items,
                            constraints.maxWidth,
                            key:
                                'catalog-${_group.id}-$_category-$_submittedQuery',
                            controller: _scroll,
                            footer: Padding(
                              padding: const EdgeInsets.fromLTRB(18, 0, 18, 24),
                              child: Center(
                                child: _loadingMore
                                    ? const LoadingHint(
                                        message: '正在加载更多剧集…',
                                        compact: true,
                                      )
                                    : _hasMore
                                    ? RemoteButton(
                                        label: '加载更多',
                                        icon: Icons.expand_more,
                                        onPressed: () => _load(more: true),
                                      )
                                    : const Text('已经看到这里的全部剧集'),
                              ),
                            ),
                          );
                        }
                        final padding = constraints.maxWidth < 600
                            ? 16.0
                            : 24.0;
                        return RefreshIndicator(
                          onRefresh: _refreshCatalog,
                          child: CustomScrollView(
                            controller: _scroll,
                            physics: const AlwaysScrollableScrollPhysics(),
                            slivers: [
                              SliverPadding(
                                padding: EdgeInsets.fromLTRB(
                                  padding,
                                  0,
                                  padding,
                                  16,
                                ),
                                sliver: SliverGrid(
                                  gridDelegate: dramaGridDelegate(
                                    context,
                                    constraints.maxWidth - 2 * padding,
                                  ),
                                  delegate: SliverChildBuilderDelegate(
                                    (_, index) => _catalogTile(items[index]),
                                    childCount: items.length,
                                  ),
                                ),
                              ),
                              SliverToBoxAdapter(
                                child: Padding(
                                  padding: const EdgeInsets.only(bottom: 24),
                                  child: Center(
                                    child: _loadingMore
                                        ? const LoadingHint(
                                            message: '正在加载更多剧集…',
                                            compact: true,
                                          )
                                        : _hasMore
                                        ? OutlinedButton.icon(
                                            onPressed: () => _load(more: true),
                                            icon: const Icon(
                                              Icons.expand_more_rounded,
                                            ),
                                            label: const Text('加载更多'),
                                          )
                                        : Text(
                                            '已经看到这里的全部剧集',
                                            style: TextStyle(
                                              color: Theme.of(context)
                                                  .colorScheme
                                                  .onSurfaceVariant,
                                              fontSize: 12,
                                            ),
                                          ),
                                  ),
                                ),
                              ),
                            ],
                          ),
                        );
                      },
                    ),
            ),
          ),
          if (_selectionMode && selectionInBody)
            _selectionBar(safeBottom: false),
        ],
      ],
    );
  }

  Widget _selectionBar({bool safeBottom = true}) {
    final theme = Theme.of(context);
    final count = _selectedDramas.length;
    return Material(
      color: theme.colorScheme.surface,
      child: Container(
        width: double.infinity,
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(color: theme.colorScheme.outlineVariant),
          ),
        ),
        child: SafeArea(
          top: false,
          bottom: safeBottom,
          minimum: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: LayoutBuilder(
            builder: (context, constraints) {
              final summary = Semantics(
                liveRegion: true,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      count == 0 ? '点选要下载的短剧' : '已选 $count 部',
                      style: theme.textTheme.titleSmall?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      count == 0
                          ? '最多 ${BatchDownloads.maxDramas} 部'
                          : '下一步选择分集和画质',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              );
              final next = FilledButton(
                key: const ValueKey('download-selected-dramas'),
                onPressed: count == 0 ? null : _downloadSelected,
                style: FilledButton.styleFrom(
                  minimumSize: const Size(96, 48),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 20,
                    vertical: 12,
                  ),
                ),
                child: const Text('下一步'),
              );
              if (constraints.maxWidth < 320 ||
                  MediaQuery.textScalerOf(context).scale(14) > 21) {
                return Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [summary, const SizedBox(height: 12), next],
                );
              }
              return Row(
                children: [
                  Expanded(child: summary),
                  const SizedBox(width: 16),
                  next,
                ],
              );
            },
          ),
        ),
      ),
    );
  }

  Widget _updateStatus() {
    final sources = _group.sources;
    final busy = sources.any((source) => _updater.busy(source.id));
    final messages = <String>[];
    var failed = false;
    for (final source in sources) {
      final status = _updater.status(source.id);
      final error = [
        _updater.error(source.id),
        status?.error ?? '',
        status?.storageError ?? '',
      ].where((value) => value.isNotEmpty).toSet().join('；');
      if (error.isNotEmpty) {
        failed = true;
        messages.add('${source.name}：$error');
      } else if (status != null) {
        messages.add(
          '${source.name}：${status.stage.isEmpty ? '准备更新' : status.stage}'
          '${status.total > 0 ? ' ${status.completed}/${status.total}' : ''}'
          '${!status.running && status.added > 0 ? ' · 新增 ${status.added} 部' : ''}',
        );
      } else if (busy) {
        messages.add('${source.name}：正在启动');
      }
    }
    final colors = Theme.of(context).colorScheme;
    return Material(
      color: failed ? colors.errorContainer : colors.surfaceContainerHighest,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
        child: Row(
          children: [
            Expanded(
              child: Text(
                messages.isEmpty ? '准备更新剧库' : messages.join('；'),
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  fontSize: 12,
                  color: failed ? colors.onErrorContainer : colors.onSurface,
                ),
              ),
            ),
            if (busy)
              TextButton(
                onPressed: () => _updater.stop(sources),
                child: const Text('停止'),
              ),
            // 0.8.10：失败后直接给一个重试入口。
            // 原来只能去设置里点「检查链接」才能重来，用户感受就是"卡死了"。
            if (failed && !busy)
              TextButton(
                onPressed: () => _updater.update(sources),
                child: const Text('重试'),
              ),
            IconButton(
              tooltip: '查看更新详情',
              onPressed: _manageSources,
              icon: const Icon(Icons.info_outline_rounded, size: 20),
            ),
            if (!busy)
              IconButton(
                tooltip: '收起更新提示',
                onPressed: () => setState(() => _updateNotice = false),
                icon: const Icon(Icons.close_rounded, size: 20),
              ),
          ],
        ),
      ),
    );
  }

  Widget _televisionGrid(
    List<Drama> items,
    double width, {
    required String key,
    ScrollController? controller,
    Widget? footer,
  }) {
    final columns = ((width - 36) / 120).floor().clamp(1, 10);
    final tileWidth = (width - 36 - (columns - 1) * 14) / columns;
    return RemoteGrid(
      key: ValueKey('tv-grid-$key'),
      itemKeys: items.map((item) => item.id).toList(),
      columns: columns,
      // Use the actual RemoteGrid cell width. The old tileWidth - 14/+14
      // formula under-allocated a 2:3 poster and could overflow on 720p TV layouts.
      itemExtent: DramaTile.extentFor(context, tileWidth),
      controller: controller,
      footer: footer,
      padding: const EdgeInsets.fromLTRB(18, 2, 18, 18),
      itemBuilder: (_, index, node, onFocus) =>
          _catalogTile(items[index], focusNode: node, onFocus: onFocus),
    );
  }
}

/// 站源选择弹窗（0.6.11 新增）。
///
/// 用户反馈原下拉菜单"点一下就修改、多选要多点很多次"，
/// 故改为弹窗内自由勾选、**按「确定」才生效**。
///
/// 内部只维护 `Set<String>`（分组 id），按「确定」时
/// `Navigator.pop(selected)` 把结果交回调用方，由调用方展开成站点 id。
class _SourcePickerSheet extends StatefulWidget {
  const _SourcePickerSheet({
    required this.groups,
    required this.initial,
    required this.allSources,
    this.television = false,
  });

  final List<SourceGroup> groups;
  final Set<String> initial;
  final List<SourceSite> allSources;

  /// 电视模式：加大字号/点击区，并给每一行挂 RemoteTarget 焦点。
  ///
  /// 由调用方显式传入而不是只读 `AppLayout`，这样在纯 widget 测试里
  /// 也能直接构造电视版弹窗。
  final bool television;

  @override
  State<_SourcePickerSheet> createState() => _SourcePickerSheetState();
}

class _SourcePickerSheetState extends State<_SourcePickerSheet> {
  late final Set<String> _selected = {...widget.initial};

  bool get _tv => widget.television || AppLayout.isTelevision(context);

  /// 遥控器初始落点：优先停在**已勾选**的第一个分组上，
  /// 这样一打开就知道"现在用的是哪个源"，而不是从列表头开始瞎摸。
  int get _autoFocusIndex {
    final index = widget.groups.indexWhere(
      (group) => _selected.contains(group.id),
    );
    return index < 0 ? 0 : index;
  }

  void _toggle(String groupId) {
    setState(() {
      if (!_selected.remove(groupId)) _selected.add(groupId);
    });
  }

  void _selectAll() {
    setState(() {
      _selected
        ..clear()
        ..addAll(widget.groups.map((group) => group.id));
    });
  }

  void _clearAll() => setState(_selected.clear);

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final tv = _tv;
    final selectedCount = _selected.length;
    final groupCount = widget.groups.length;
    return Container(
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: BorderRadius.circular(tv ? 24 : 18),
        border: tv
            ? Border.all(
                color: colors.outlineVariant.withValues(alpha: .8),
                width: 1,
              )
            : null,
        boxShadow: tv
            ? [
                BoxShadow(
                  color: Colors.black.withValues(alpha: .45),
                  blurRadius: 32,
                  spreadRadius: 4,
                ),
              ]
            : null,
      ),
      padding: EdgeInsets.fromLTRB(
        tv ? 18 : 16,
        14,
        tv ? 18 : 16,
        tv ? 14 : 12,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  '选择站源',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: tv ? 19 : 17,
                    fontWeight: FontWeight.w800,
                  ),
                ),
              ),
              // 已选计数：多选时最容易"忘了自己勾了几个"。
              Container(
                padding: EdgeInsets.symmetric(
                  horizontal: tv ? 10 : 8,
                  vertical: tv ? 4 : 2,
                ),
                decoration: BoxDecoration(
                  color: colors.primary.withValues(alpha: .14),
                  borderRadius: BorderRadius.circular(9),
                ),
                child: Text(
                  '已选 $selectedCount/$groupCount',
                  style: TextStyle(
                    fontSize: tv ? 13 : 12,
                    fontWeight: FontWeight.w700,
                    color: colors.primary,
                  ),
                ),
              ),
              SizedBox(width: tv ? 16 : 6),
              _quickAction(
                key: const ValueKey('source-sheet-all'),
                label: '全选',
                onPressed: _selectAll,
              ),
              SizedBox(width: tv ? 8 : 2),
              _quickAction(
                key: const ValueKey('source-sheet-clear'),
                label: '清空',
                onPressed: _clearAll,
              ),
            ],
          ),
          SizedBox(height: tv ? 6 : 2),
          Text(
            tv
                ? '共 ${widget.allSources.length} 个站源，可同时勾选多个聚合浏览'
                : '可同时勾选多个站源聚合浏览',
            style: TextStyle(
              fontSize: 12,
              color: colors.onSurfaceVariant,
            ),
          ),
          SizedBox(height: tv ? 16 : 8),
          ConstrainedBox(
            constraints: BoxConstraints(
              maxHeight: tv
                  ? (MediaQuery.sizeOf(context).height < 600
                        ? (MediaQuery.sizeOf(context).height - 300).clamp(
                            160.0,
                            260.0,
                          )
                        : (MediaQuery.sizeOf(context).height * 0.52).clamp(
                            200.0,
                            460.0,
                          ))
                  : MediaQuery.sizeOf(context).height * 0.45,
            ),
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (var index = 0; index < widget.groups.length; index++)
                    _groupTile(widget.groups[index], index, tv),
                ],
              ),
            ),
          ),
          SizedBox(height: tv ? 16 : 8),
          Row(
            children: [
              Expanded(
                child: _sheetButton(
                  key: const ValueKey('source-sheet-cancel'),
                  label: '取消',
                  tv: tv,
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ),
              SizedBox(width: tv ? 16 : 12),
              Expanded(
                child: _sheetButton(
                  key: const ValueKey('source-sheet-confirm'),
                  label: '确定',
                  tv: tv,
                  filled: true,
                  onPressed: () => Navigator.of(context).pop({..._selected}),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  /// 单行分组：勾选框 + 名称 + 入口列表。
  ///
  /// 用 RemoteTarget 而不是 CheckboxListTile：后者在电视上只有一个很淡的
  /// 焦点色块，遥控器停在上面几乎看不出来；而且它的点击区只有文字那一行。
  Widget _groupTile(SourceGroup group, int index, bool tv) {
    final colors = Theme.of(context).colorScheme;
    final checked = _selected.contains(group.id);
    final entries = group.sources.map((site) => site.entryName).join(' · ');
    return Padding(
      padding: EdgeInsets.only(bottom: tv ? 8 : 6),
      child: RemoteTarget(
        key: ValueKey('source-sheet-${group.id}'),
        onPressed: () => _toggle(group.id),
        selected: checked,
        autofocus: tv && index == _autoFocusIndex,
        label:
            '${group.name}，${group.sources.length} 个入口，'
            '${checked ? '已选' : '未选'}',
        child: Padding(
          padding: EdgeInsets.symmetric(
            horizontal: tv ? 10 : 8,
            vertical: 8,
          ),
          child: Row(
            children: [
              _checkBox(checked, tv),
              SizedBox(width: tv ? 14 : 10),
              Expanded(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      group.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 15,
                        fontWeight: FontWeight.w700,
                        color: checked ? colors.primary : colors.onSurface,
                      ),
                    ),
                    if (group.sources.length > 1) ...[
                      const SizedBox(height: 3),
                      Text(
                        '${group.sources.length} 个入口 · $entries',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: tv ? 12 : 11.5,
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _checkBox(bool checked, bool tv) {
    final colors = Theme.of(context).colorScheme;
    final size = tv ? 24.0 : 22.0;
    return AnimatedContainer(
      duration: const Duration(milliseconds: 120),
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: checked ? colors.primary : Colors.transparent,
        borderRadius: BorderRadius.circular(tv ? 9 : 6),
        border: Border.all(
          color: checked ? colors.primary : colors.outline,
          width: 2,
        ),
      ),
      child: checked
          ? Icon(Icons.check_rounded, size: size - 8, color: colors.onPrimary)
          : null,
    );
  }

  /// 全选 / 清空：TV 上同样要能聚焦，所以 TV 用 RemoteTarget，
  /// 手机沿用 TextButton（触屏不需要焦点环，也省一点宽度）。
  Widget _quickAction({
    required Key key,
    required String label,
    required VoidCallback onPressed,
  }) {
    final tv = _tv;
    if (!tv) {
      return TextButton(key: key, onPressed: onPressed, child: Text(label));
    }
    final colors = Theme.of(context).colorScheme;
    return RemoteTarget(
      key: key,
      onPressed: onPressed,
      label: label,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Text(
          label,
          style: TextStyle(
            fontSize: 14,
            fontWeight: FontWeight.w700,
            color: colors.primary,
          ),
        ),
      ),
    );
  }

  Widget _sheetButton({
    required Key key,
    required String label,
    required bool tv,
    required VoidCallback onPressed,
    bool filled = false,
  }) {
    if (!tv) {
      final child = Text(label);
      return filled
          ? FilledButton(key: key, onPressed: onPressed, child: child)
          : OutlinedButton(key: key, onPressed: onPressed, child: child);
    }
    final colors = Theme.of(context).colorScheme;
    return RemoteTarget(
      key: key,
      onPressed: onPressed,
      label: label,
      child: Container(
        height: 42,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: filled ? colors.primary : colors.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
          border: filled
              ? null
              : Border.all(color: colors.outlineVariant, width: 1),
        ),
        child: Text(
          label,
          style: TextStyle(
            fontSize: 15,
            fontWeight: FontWeight.w700,
            color: filled ? colors.onPrimary : colors.onSurface,
          ),
        ),
      ),
    );
  }
}
