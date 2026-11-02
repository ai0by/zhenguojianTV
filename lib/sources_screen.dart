import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'core_bridge.dart';
import 'app_layout.dart';
import 'local_store.dart';
import 'models.dart';
import 'remote_widgets.dart';
import 'source_status.dart';

String sourceTimestamp(DateTime? value) {
  if (value == null) return '尚无记录';
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.month}/${local.day} ${two(local.hour)}:${two(local.minute)}';
}

class SourcesScreen extends StatefulWidget {
  const SourcesScreen({
    super.key,
    required this.repository,
    required this.store,
    this.initialSource,
    this.drama,
  });

  final AppRepository repository;
  final LocalStore store;
  final String? initialSource;
  final Drama? drama;

  @override
  State<SourcesScreen> createState() => _SourcesScreenState();
}

class _SourcesScreenState extends State<SourcesScreen> {
  final _statuses = <String, SourceStatus>{};
  final _errors = <String, String>{};
  final _pending = <String>{};
  final _revisions = <String, int>{};
  final _expandedHealth = <String>{};
  Timer? _timer;
  bool _polling = false;
  int _ticks = 0;

  /// 是否正在"自动续批"更新（0.2.27）。
  ///
  /// 站源更新是分批进行的，一键更新时需要逐站源跑完所有批次。
  /// 该标记用于按钮禁用与进度文案（"正在自动继续更新…"）。
  bool _autoUpdating = false;

  /// 远端剧集快照的索引摘要（0.8.20）。
  ///
  /// 站源管理页现在承担"手动同步远端剧集"的入口，所以要把远端那份数据的
  /// 生成时间摆出来 —— 用户点按钮之前就该知道远端有多新、本机是不是已经吃过。
  CatalogSnapshotInfo? _snapshot;
  bool _snapshotBusy = false;
  String _snapshotError = '';

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    unawaited(_loadSnapshot());
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      _ticks++;
      if (_statuses.values.any((status) => status.retryAt != null)) {
        setState(() {});
      }
      if (_ticks % 2 == 0 &&
          (_statuses.values.any((status) => status.running) ||
              _ticks % 10 == 0)) {
        unawaited(_refresh());
      }
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_polling) return;
    _polling = true;
    final epoch = widget.store.profileEpoch;
    try {
      await Future.wait([
        for (final source in widget.store.sources)
          (() async {
            final revision = _revisions[source.id] ?? 0;
            try {
              final status = await widget.repository.sourceStatus(source.id);
              if (!mounted ||
                  epoch != widget.store.profileEpoch ||
                  revision != (_revisions[source.id] ?? 0)) {
                return;
              }
              setState(() {
                _statuses[source.id] = status;
                _errors.remove(source.id);
              });
            } catch (error) {
              if (mounted &&
                  epoch == widget.store.profileEpoch &&
                  revision == (_revisions[source.id] ?? 0)) {
                setState(() => _errors[source.id] = error.toString());
              }
            }
          })(),
      ]);
    } finally {
      _polling = false;
    }
  }

  Future<void> _run(SourceSite source, String operation) async {
    if (_pending.contains(source.id)) return;
    final epoch = widget.store.profileEpoch;
    setState(() {
      _pending.add(source.id);
      _errors.remove(source.id);
      _revisions[source.id] = (_revisions[source.id] ?? 0) + 1;
      if (operation == 'check' || operation == 'checkCatalog') {
        _expandedHealth.add(source.id);
      }
    });
    try {
      final status = operation == 'cancel'
          ? await widget.repository.cancelSourceJob(source.id)
          : await widget.repository.startSourceJob(
              source.id,
              operation,
              drama: source.id == widget.drama?.source ? widget.drama : null,
            );
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _statuses[source.id] = status);
      }
    } catch (error) {
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _errors[source.id] = error.toString());
      }
    } finally {
      if (mounted) setState(() => _pending.remove(source.id));
    }
  }

  /// 读一次远端快照索引（0.8.20）。
  ///
  /// 只拉几 KB 的 `index.json`，不下载任何站源文件，所以进页面就读一次没问题。
  /// 失败不弹窗：远端不通时按钮照样能点（点了会给出明确的失败提示）。
  Future<void> _loadSnapshot() async {
    if (!widget.repository.supportsSourceManagement) return;
    final epoch = widget.store.profileEpoch;
    try {
      final info = await widget.repository.catalogSnapshotInfo();
      if (!mounted || epoch != widget.store.profileEpoch) return;
      setState(() {
        _snapshot = info;
        _snapshotError = '';
      });
    } catch (error) {
      if (!mounted || epoch != widget.store.profileEpoch) return;
      setState(() => _snapshotError = '$error');
    }
  }

  /// 手动同步远端剧集快照（0.8.20）。
  ///
  /// 取代了 0.8.19 在首页 initState 和自动更新里"顺手拉一次"的行为：用户明确
  /// 要求改成手动。同步完通知一次首页整库重读缓存，站源卡片上的条数也跟着刷新。
  Future<void> _syncSnapshot() async {
    if (_snapshotBusy) return;
    final epoch = widget.store.profileEpoch;
    final sources = [for (final site in widget.store.sources) site.id];
    setState(() {
      _snapshotBusy = true;
      _snapshotError = '';
    });
    try {
      final changed = await widget.repository.syncCatalogSnapshot(
        sources: sources,
      );
      if (!mounted || epoch != widget.store.profileEpoch) return;
      if (changed) widget.repository.catalogUpdates.markSnapshotSynced();
      await _loadSnapshot();
      if (!mounted || epoch != widget.store.profileEpoch) return;
      await _refresh();
      if (!mounted) return;
      final info = _snapshot;
      final message = changed
          ? '已同步远端剧集：${info?.total ?? 0} 部'
          : '远端剧集没有新版本';
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
    } catch (error) {
      if (!mounted || epoch != widget.store.profileEpoch) return;
      setState(() => _snapshotError = '$error');
    } finally {
      if (mounted) setState(() => _snapshotBusy = false);
    }
  }

  /// 一键更新全部站源（0.2.27：改为**自动续批**）。
  ///
  /// 用户真机反馈第 3 条："分批更新应该自动继续而不是手动继续"。
  ///
  /// 实现方式（串行，用户确认）：
  /// - 逐个站源依次发起 `update` 作业（原生层对站源任务本身有串行保护）；
  /// - 等该站源当前作业结束后，若原生仍报告"还有更多未完成"（`hasMore`）
  ///   或进度未走完，则**自动再发一轮**，直到该站源更新完成为止；
  /// - 全部站源处理完后退出。
  ///
  /// 同时用 `_autoUpdateSources` 标记"正在自动续批"，用于按钮禁用与文案。
  Future<void> _updateAll() async {
    if (_autoUpdating) return;
    final sources = widget.store.sources.toList();
    if (sources.isEmpty) return;
    setState(() => _autoUpdating = true);
    try {
      for (final source in sources) {
        await _runToCompletion(source);
      }
    } finally {
      if (mounted) setState(() => _autoUpdating = false);
    }
  }

  /// 对单个站源反复发起 `update`，直到原生报告不再有未完成内容。
  Future<void> _runToCompletion(SourceSite source) async {
    // 最多轮数兜底，避免极端情况下无限循环（每轮原生最多 10 分钟）。
    const maxRounds = 60;
    for (var round = 0; round < maxRounds; round++) {
      if (!mounted) return;
      final epoch = widget.store.profileEpoch;
      await _run(source, 'update');
      // 等待该站源作业真正结束（_run 只负责发起，作业在后台跑）。
      final finished = await _waitSourceIdle(source, epoch);
      if (!finished) return; // 用户离开/切换用户：停止续批
      if (epoch != widget.store.profileEpoch) return;
      final status = _statuses[source.id];
      if (status == null) return;
      // 原生报告还有更多历史分页未加载 → 继续下一轮。
      if (status.hasMore) continue;
      return;
    }
  }

  /// 轮询等待某站源作业结束。
  ///
  /// 返回 false 表示组件已销毁或用户已切换，调用方应终止续批。
  Future<bool> _waitSourceIdle(SourceSite source, int epoch) async {
    // 先轮询直到"运行中"（避免作业刚发起还没进入 running 就被判定为空闲）。
    var seenRunning = false;
    for (var i = 0; i < 1200; i++) {
      if (!mounted || epoch != widget.store.profileEpoch) return false;
      await _refresh();
      if (!mounted || epoch != widget.store.profileEpoch) return false;
      final status = _statuses[source.id];
      final running = status?.running == true || _pending.contains(source.id);
      if (running) {
        seenRunning = true;
      } else if (seenRunning) {
        return true;
      } else if (i > 5) {
        // 6 秒内都没进入 running，认为本轮已直接结束。
        return true;
      }
      await Future<void>.delayed(const Duration(milliseconds: 1000));
    }
    return true;
  }

  /// 当前正在更新的站源数量，用于按钮上的进度提示。
  int get _runningCount => widget.store.sources
      .where(
        (source) =>
            _pending.contains(source.id) ||
            _statuses[source.id]?.running == true,
      )
      .length;

  Future<void> _copy(SourceSite source, SourceStatus status) async {
    final text = StringBuffer('${source.name}\n');
    text.writeln(
      '缓存 ${status.count} 部，更新 ${sourceTimestamp(status.updatedAt)}',
    );
    final health = status.health;
    if (health != null) {
      text.writeln('${health.label} · ${sourceTimestamp(health.checkedAt)}');
      if (health.sample.isNotEmpty) text.writeln('检测剧集：${health.sample}');
      for (final step in health.steps) {
        text.writeln('${step.name}：${step.message}');
        text.writeln(
          '${step.host} HTTP ${step.httpStatus} · ${step.elapsedMs} ms',
        );
        if (step.cfRay.isNotEmpty) text.writeln('CF Ray: ${step.cfRay}');
      }
    }
    if (status.error.isNotEmpty) text.writeln(status.error);
    if (status.storageError.isNotEmpty) text.writeln(status.storageError);
    await Clipboard.setData(ClipboardData(text: text.toString()));
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('诊断信息已复制')));
    }
  }

  void _toggleHealth(String sourceId) {
    setState(() {
      if (!_expandedHealth.remove(sourceId)) _expandedHealth.add(sourceId);
    });
  }

  /// 电视端的操作按钮：带焦点环、放大、外发光，遥控器停上去一眼可见。
  ///
  /// 手机上沿用 Material 按钮（触屏更贴合系统风格，也让既有测试继续有效）。
  Widget _tvAction({
    required Key key,
    required String label,
    required IconData icon,
    required VoidCallback? onPressed,
    bool filled = false,
    bool autofocus = false,
  }) {
    final colors = Theme.of(context).colorScheme;
    final enabled = onPressed != null;
    final foreground = enabled
        ? (filled ? colors.onPrimary : colors.onSurface)
        : colors.onSurfaceVariant.withValues(alpha: .5);
    return RemoteTarget(
      key: key,
      onPressed: onPressed,
      autofocus: autofocus,
      label: label,
      child: Container(
        height: 48,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        decoration: BoxDecoration(
          color: filled && enabled
              ? colors.primary
              : colors.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
          border: filled
              ? null
              : Border.all(color: colors.outlineVariant, width: 1),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 20, color: foreground),
            const SizedBox(width: 8),
            Text(
              label,
              style: TextStyle(
                fontSize: 16,
                fontWeight: FontWeight.w600,
                color: foreground,
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// TV 上「更多操作」：用居中对话框代替 PopupMenuButton。
  Future<void> _openMoreActions(SourceSite source, SourceStatus? status) async {
    final options = <(String, String, bool)>[
      ('more', '继续加载一页', status?.hasMore ?? true),
      ('metadata', '补齐资料', true),
      if (source.id == 'huangdou')
        (
          'vipMetadata',
          '补齐 VIP 资料（${status?.unknownVip ?? 0} 部）',
          (status?.unknownVip ?? 0) > 0,
        ),
      ('checkCatalog', '仅检测目录', true),
    ];
    final picked = await showDialog<String>(
      context: context,
      barrierColor: Colors.black.withValues(alpha: .66),
      builder: (dialogContext) => Dialog(
        backgroundColor: Colors.transparent,
        elevation: 0,
        insetPadding: const EdgeInsets.symmetric(horizontal: 48, vertical: 32),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 560),
          child: RemoteOptionSheet(
            title: '${source.name} · 更多操作',
            options: options,
            keyPrefix: 'more-option',
          ),
        ),
      ),
    );
    if (picked == null || !mounted) return;
    await _run(source, picked);
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (context, _) {
      final television = AppLayout.isTelevision(context);
      final sources = widget.store.sources.toList()
        ..sort((a, b) {
          final aFirst = a.id == widget.initialSource ? 0 : 1;
          final bFirst = b.id == widget.initialSource ? 0 : 1;
          return aFirst.compareTo(bFirst);
        });
      return Scaffold(
        appBar: AppBar(
          automaticallyImplyLeading: !television,
          title: const Text('站源管理'),
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 960),
            child: RefreshIndicator(
              onRefresh: _refresh,
              child: ListView(
                padding: const EdgeInsets.all(16),
                children: [
                  const Padding(
                    padding: EdgeInsets.only(bottom: 16),
                    child: Text(
                      '各站源可分别更新和检测。更新会查找新剧、继续加载一页历史内容，并分批补齐资料；离开此页后任务继续。'
                      '「同步远程数据源」会拉取仓库里由服务器统一抓好的剧库（不再自动同步）。',
                    ),
                  ),
                  // 一键全部更新 + 手动同步远端剧集（0.8.20）。
                  if (sources.isNotEmpty &&
                      widget.repository.supportsSourceManagement)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          // 两个按钮都可能很长，窄屏 / 大字号下用 Wrap 换行，
                          // 不要塞进 Row —— 一行放不下会直接溢出。
                          Wrap(
                            spacing: 12,
                            runSpacing: 10,
                            crossAxisAlignment: WrapCrossAlignment.center,
                            children: [
                              // TV 上用带焦点环的 RemoteTarget，手机沿用 Material 按钮。
                              if (television)
                                _tvAction(
                                  key: const ValueKey('update-all-sources'),
                                  label: '全部更新',
                                  icon: Icons.sync_rounded,
                                  filled: true,
                                  onPressed: _runningCount > 0 || _autoUpdating
                                      ? null
                                      : _updateAll,
                                )
                              else
                                FilledButton.icon(
                                  key: const ValueKey('update-all-sources'),
                                  onPressed:
                                      _runningCount > 0 || _autoUpdating
                                      ? null
                                      : _updateAll,
                                  icon: const Icon(Icons.sync_rounded),
                                  label: const Text('全部更新'),
                                ),
                              if (television)
                                _tvAction(
                                  key: const ValueKey('sync-catalog-snapshot'),
                                  label: '同步远程数据源',
                                  icon: Icons.cloud_download_outlined,
                                  onPressed: _snapshotBusy ? null : _syncSnapshot,
                                )
                              else
                                OutlinedButton.icon(
                                  key: const ValueKey('sync-catalog-snapshot'),
                                  onPressed: _snapshotBusy ? null : _syncSnapshot,
                                  icon: _snapshotBusy
                                      ? const SizedBox(
                                          width: 18,
                                          height: 18,
                                          child: CircularProgressIndicator(
                                            strokeWidth: 2,
                                          ),
                                        )
                                      : const Icon(Icons.cloud_download_outlined),
                                  label: Text(
                                    _snapshotBusy ? '同步中…' : '同步远程数据源',
                                  ),
                                ),
                            ],
                          ),
                          const SizedBox(height: 10),
                          Text(
                            _autoUpdating
                                ? '正在自动继续更新（分批进行，请勿离开）…'
                                : _runningCount > 0
                                ? '正在更新 $_runningCount / ${sources.length} 个站源…'
                                : '共 ${sources.length} 个站源，可一键依次更新',
                            style: television
                                ? const TextStyle(fontSize: 15)
                                : Theme.of(context).textTheme.bodySmall,
                          ),
                          const SizedBox(height: 4),
                          _snapshotLine(television),
                        ],
                      ),
                    ),
                  if (sources.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(24),
                      child: Text('当前用户没有可用站源'),
                    ),
                  for (final group in SourceGroup.fromSources(sources)) ...[
                    // 多入口站源（黄果 = 剧场 / AI / 旧版）先给一行分组标题，
                    // 再平铺各自的卡片 —— 详见 [_groupHeader] 的注释。
                    if (group.sources.length > 1)
                      _groupHeader(group, television),
                    for (final source in group.sources)
                      _sourceCard(source, television: television),
                  ],
                ],
              ),
            ),
          ),
        ),
      );
    },
  );

  /// 多入口站源的分组标题（0.8.20）。
  ///
  /// 历史问题（用户反馈"黄果的站源管理那里有大片留白"）：
  /// 黄果是「剧场 / AI / 旧版」三个入口，原来渲染成
  /// `Card > ExpansionTile > Card` 的三层嵌套 —— 外层和里层用的都是
  /// `surfaceContainerLow`，展开后整块连成一片纯色，三张卡片的边界根本看不出来，
  /// 观感就是"一大片空白"；折叠时又只剩一行标题，用户以为这三个源没加载出来。
  ///
  /// 现在改成**一行分组标题 + 三张和其他站源完全一致的卡片**：没有嵌套卡片、
  /// 没有折叠态，视觉和行为都不再有特例。
  Widget _groupHeader(SourceGroup group, bool television) {
    final colors = Theme.of(context).colorScheme;
    final total = group.sources.fold<int>(
      0,
      (sum, source) => sum + (_statuses[source.id]?.count ?? 0),
    );
    return Padding(
      padding: const EdgeInsets.only(top: 4, bottom: 10),
      child: Row(
        children: [
          Icon(
            Icons.hub_outlined,
            size: television ? 24 : 20,
            color: colors.primary,
          ),
          SizedBox(width: television ? 10 : 8),
          Text(
            group.name,
            style: TextStyle(
              fontSize: television ? 20 : 16,
              fontWeight: FontWeight.w800,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              '${group.sources.length} 个入口 · $total 部',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: television ? 15 : 12,
                color: colors.onSurfaceVariant,
              ),
            ),
          ),
        ],
      ),
    );
  }

  /// 远端剧集快照那一行说明（0.8.20）。
  ///
  /// 用户要求"要给出提示，远端站源数据同步时间"，所以这里必须始终有一行字：
  /// 读到了就写远端生成时间 + 站源数 + 总条数 + 本机是否已同步，
  /// 读不到就明确写"尚未读取 / 读取失败"，而不是留一片空白。
  Widget _snapshotLine(bool television) {
    final colors = Theme.of(context).colorScheme;
    final info = _snapshot;
    final String text;
    if (_snapshotBusy) {
      text = '正在同步远端剧集，请稍候…';
    } else if (_snapshotError.isNotEmpty) {
      text = '远端剧集读取失败：$_snapshotError';
    } else if (info == null) {
      text = '远端站源数据：尚未读取';
    } else if (info.generatedAt == null) {
      text = '远端站源数据：还没有可用的快照';
    } else {
      final state = info.upToDate ? '本机已同步' : '本机有新版本可同步';
      text =
          '远端站源数据：${sourceTimestamp(info.generatedAt)} · '
          '${info.sourceCount} 个站源 / ${info.total} 部 · $state';
    }
    final failed = _snapshotError.isNotEmpty;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(
          Icons.cloud_outlined,
          size: television ? 20 : 15,
          color: failed ? colors.error : colors.onSurfaceVariant,
        ),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            text,
            style: TextStyle(
              fontSize: television ? 15 : 12,
              color: failed ? colors.error : colors.onSurfaceVariant,
            ),
          ),
        ),
      ],
    );
  }

  Widget _sourceCard(SourceSite source, {required bool television}) {
    final status = _statuses[source.id];
    final pending = _pending.contains(source.id);
    final busy = pending || status?.running == true;
    final seconds = status?.retrySeconds ?? 0;
    final enabled =
        !busy && seconds == 0 && widget.repository.supportsSourceManagement;
    final error = _errors[source.id] ?? status?.error ?? '';
    final health = status?.health;
    final healthExpanded = _expandedHealth.contains(source.id);
    final colors = Theme.of(context).colorScheme;
    return Card(
      key: ValueKey('source-${source.id}'),
      margin: const EdgeInsets.only(bottom: 16),
      child: Padding(
        padding: EdgeInsets.all(television ? 20 : 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  Icons.dns_outlined,
                  size: television ? 28 : 24,
                  color: colors.primary,
                ),
                SizedBox(width: television ? 14 : 12),
                Expanded(
                  child: Text(
                    source.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                      fontSize: television ? 24 : null,
                    ),
                  ),
                ),
                Text(
                  '${status?.count ?? 0} 部',
                  style: TextStyle(
                    fontSize: television ? 18 : null,
                    color: colors.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              '最近更新：${sourceTimestamp(status?.updatedAt)}',
              style: TextStyle(fontSize: television ? 16 : null),
            ),
            if (status != null && status.count > 0)
              Text(
                '已加载至第 ${status.page} 页${status.hasMore ? ' · 可继续加载' : ' · 当前分页已加载完'}',
                style: TextStyle(fontSize: television ? 16 : null),
              ),
            const SizedBox(height: 12),
            if (television)
              Wrap(
                spacing: 12,
                runSpacing: 10,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  _tvAction(
                    key: ValueKey('update-${source.id}'),
                    label: '更新',
                    icon: Icons.sync_rounded,
                    filled: true,
                    onPressed: enabled ? () => _run(source, 'update') : null,
                  ),
                  _tvAction(
                    key: ValueKey('check-${source.id}'),
                    label: '检测连接与播放',
                    icon: Icons.network_check,
                    onPressed: enabled ? () => _run(source, 'check') : null,
                  ),
                  if (status?.running == true)
                    _tvAction(
                      key: ValueKey('stop-${source.id}'),
                      label: '停止',
                      icon: Icons.stop_circle_outlined,
                      onPressed: pending ? null : () => _run(source, 'cancel'),
                    ),
                  // TV 上 PopupMenuButton 的弹出菜单很难用遥控器操作
                  // （无焦点环、贴边），改为打开一个可聚焦的居中对话框。
                  _tvAction(
                    key: ValueKey('more-${source.id}'),
                    label: '更多',
                    icon: Icons.more_horiz_rounded,
                    onPressed: enabled
                        ? () => _openMoreActions(source, status)
                        : null,
                  ),
                ],
              )
            else
              Wrap(
                spacing: 10,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  FilledButton.icon(
                    key: ValueKey('update-${source.id}'),
                    onPressed: enabled ? () => _run(source, 'update') : null,
                    icon: const Icon(Icons.sync_rounded),
                    label: const Text('更新'),
                  ),
                  OutlinedButton.icon(
                    key: ValueKey('check-${source.id}'),
                    onPressed: enabled ? () => _run(source, 'check') : null,
                    icon: const Icon(Icons.network_check),
                    label: const Text('检测连接与播放'),
                  ),
                  if (status?.running == true)
                    TextButton(
                      onPressed: pending ? null : () => _run(source, 'cancel'),
                      child: const Text('停止'),
                    ),
                  PopupMenuButton<String>(
                    tooltip: '${source.name}更多操作',
                    enabled: enabled,
                    onSelected: (operation) => _run(source, operation),
                    itemBuilder: (_) => [
                      PopupMenuItem(
                        value: 'more',
                        enabled: status?.hasMore ?? true,
                        child: const Text('继续加载一页'),
                      ),
                      const PopupMenuItem(
                        value: 'metadata',
                        child: Text('补齐资料'),
                      ),
                      if (source.id == 'huangdou')
                        PopupMenuItem(
                          value: 'vipMetadata',
                          enabled: (status?.unknownVip ?? 0) > 0,
                          child: Text(
                            '补齐 VIP 资料（${status?.unknownVip ?? 0} 部）',
                          ),
                        ),
                      const PopupMenuItem(
                        value: 'checkCatalog',
                        child: Text('仅检测目录'),
                      ),
                    ],
                  ),
                ],
              ),
            if (busy) ...[
              const SizedBox(height: 12),
              LinearProgressIndicator(
                value: status != null && status.total > 0
                    ? (status.completed / status.total).clamp(0, 1)
                    : null,
              ),
              const SizedBox(height: 8),
              Text(
                '${status?.stage ?? '准备中'}${(status?.total ?? 0) > 0 ? ' · ${status!.completed}/${status.total}' : ''}',
              ),
            ] else if (status != null && status.stage.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(
                '${status.stage}${status.added > 0 ? ' · 新增 ${status.added} 部' : ''}',
              ),
            ],
            if (seconds > 0)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  '请在 $seconds 秒后重试',
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: SelectableText(
                  error,
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (status != null && status.storageError.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      status.storageError,
                      style: TextStyle(color: colors.error),
                    ),
                    if (television)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: _tvAction(
                          key: ValueKey('save-${source.id}'),
                          label: '重试保存',
                          icon: Icons.save_outlined,
                          onPressed:
                              !busy &&
                                  widget.repository.supportsSourceManagement
                              ? () => _run(source, 'retrySave')
                              : null,
                        ),
                      )
                    else
                      TextButton.icon(
                        key: ValueKey('save-${source.id}'),
                        onPressed:
                            !busy && widget.repository.supportsSourceManagement
                            ? () => _run(source, 'retrySave')
                            : null,
                        icon: const Icon(Icons.save_outlined),
                        label: const Text('重试保存'),
                      ),
                  ],
                ),
              ),
            if (health != null) ...[
              const Divider(height: 28),
              if (television)
                Row(
                  children: [
                    Expanded(
                      child: RemoteTarget(
                        key: ValueKey('health-toggle-${source.id}'),
                        onPressed: () => _toggleHealth(source.id),
                        label:
                            '检测详情，${health.label}，${healthExpanded ? '已展开' : '已收起'}',
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 16,
                            vertical: 12,
                          ),
                          decoration: BoxDecoration(
                            color: colors.surfaceContainerHighest,
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(
                              color: colors.outlineVariant,
                              width: 1,
                            ),
                          ),
                          child: Row(
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      health.label,
                                      style: const TextStyle(
                                        fontSize: 17,
                                        fontWeight: FontWeight.w600,
                                      ),
                                    ),
                                    Text(
                                      sourceTimestamp(health.checkedAt),
                                      style: TextStyle(
                                        fontSize: 13,
                                        color: colors.onSurfaceVariant,
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                              const SizedBox(width: 8),
                              Icon(
                                healthExpanded
                                    ? Icons.expand_less_rounded
                                    : Icons.expand_more_rounded,
                                size: 26,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    _tvAction(
                      key: ValueKey('copy-${source.id}'),
                      label: '复制',
                      icon: Icons.copy_rounded,
                      onPressed: () => _copy(source, status!),
                    ),
                  ],
                )
              else
                Row(
                  children: [
                    Expanded(
                      child: Semantics(
                        expanded: healthExpanded,
                        child: Tooltip(
                          message: healthExpanded ? '收起检测详情' : '展开检测详情',
                          child: TextButton(
                            key: ValueKey('health-toggle-${source.id}'),
                            onPressed: () => _toggleHealth(source.id),
                            style: TextButton.styleFrom(
                              foregroundColor: colors.onSurface,
                              padding: const EdgeInsets.symmetric(
                                horizontal: 4,
                                vertical: 8,
                              ),
                            ),
                            child: Row(
                              children: [
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(health.label),
                                      Text(
                                        sourceTimestamp(health.checkedAt),
                                        style: Theme.of(
                                          context,
                                        ).textTheme.bodySmall,
                                      ),
                                    ],
                                  ),
                                ),
                                const SizedBox(width: 8),
                                Icon(
                                  healthExpanded
                                      ? Icons.expand_less_rounded
                                      : Icons.expand_more_rounded,
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                    IconButton(
                      tooltip: '复制诊断信息',
                      onPressed: () => _copy(source, status!),
                      icon: const Icon(Icons.copy_rounded),
                    ),
                  ],
                ),
              if (healthExpanded) ...[
                if (health.sample.isNotEmpty) Text('检测剧集：${health.sample}'),
                for (final step in health.steps)
                  Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Icon(
                          step.state == 'ok'
                              ? Icons.check_circle_outline
                              : Icons.error_outline,
                          size: 20,
                          color: step.state == 'ok'
                              ? colors.primary
                              : colors.error,
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text('${step.name}：${step.message}'),
                              if (step.host.isNotEmpty || step.httpStatus > 0)
                                Text(
                                  '${step.host}${step.httpStatus > 0 ? ' · HTTP ${step.httpStatus}' : ''} · ${step.elapsedMs} ms',
                                  style: Theme.of(context).textTheme.bodySmall,
                                ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ] else ...[
              const SizedBox(height: 12),
              const Text('尚未检测连接'),
            ],
          ],
        ),
      ),
    );
  }
}

class SourceDiagnosticsButton extends StatelessWidget {
  const SourceDiagnosticsButton({
    super.key,
    required this.repository,
    required this.store,
    required this.drama,
  });
  final AppRepository repository;
  final LocalStore store;
  final Drama drama;

  @override
  Widget build(BuildContext context) => TextButton.icon(
    onPressed: () => Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => SourcesScreen(
          repository: repository,
          store: store,
          initialSource: drama.source,
          drama: drama,
        ),
      ),
    ),
    icon: const Icon(Icons.network_check),
    label: const Text('站源诊断'),
  );
}
