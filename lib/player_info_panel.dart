import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'models.dart';

/// 小屏（竖屏）播放页下方的信息面板（0.8.13）。
///
/// 借鉴 B 站竖屏播放页的分工：
/// - **全屏只留播放器**（本面板不参与）；
/// - 非全屏时视频按自身比例固定在上方，下方是可滚动的信息区。
///
/// 为什么需要：竖屏里 16:9 的视频按 `BoxFit.contain` 铺满会留出大片黑边，
/// 而原来的控件（剧名 + 进度 + 一排按钮）全压在画面上，空出来的地方
/// 什么都没有 —— 用户反馈"太空了"。现在把剧名、操作、简介、选集
/// 都搬到画面下方，黑边变成有效信息区。
///
/// 面板只负责**展示与回调**，不持有播放状态：所有动作都由播放页传入，
/// 这样电视端（不显示本面板）与手机端共用同一套业务逻辑。
class PlayerInfoPanel extends StatefulWidget {
  const PlayerInfoPanel({
    super.key,
    required this.title,
    required this.meta,
    required this.summary,
    required this.episodes,
    required this.currentIndex,
    required this.actions,
    required this.onSelectEpisode,
    this.onOpenDetail,
    this.onOpenAllEpisodes,
  });

  /// 剧名。
  final String title;

  /// 元信息片段（如「共 42 集」「已完结」「热度 12.3 万」），自动用 · 连接。
  final List<String> meta;

  /// 简介原文，可能为空。
  final String summary;

  /// 全部剧集（用于选集网格）。
  final List<Episode> episodes;

  /// 当前播放的集索引。
  final int currentIndex;

  /// 操作按钮（追剧 / 连播 / 选集 / 倍速 / 清晰度 / 弹幕 …）。
  final List<PlayerInfoAction> actions;

  /// 点选集网格里的某一集。
  final ValueChanged<int> onSelectEpisode;

  /// 点标题进详情页；为空时不显示箭头。
  final VoidCallback? onOpenDetail;

  /// 「全部 N 集」按钮（打开完整的选集弹层）。
  final VoidCallback? onOpenAllEpisodes;

  @override
  State<PlayerInfoPanel> createState() => _PlayerInfoPanelState();
}

class _PlayerInfoPanelState extends State<PlayerInfoPanel> {
  /// 简介是否已展开。
  bool _summaryOpen = false;

  /// 选集网格是否已展开（默认只显示当前集附近的一屏）。
  bool _episodesOpen = false;

  /// 折叠状态下选集网格显示多少格。
  static const int _collapsedCells = 15;

  /// 超过这个长度才给「展开」按钮。
  static const int _summaryFoldLength = 90;

  static const Color _surface = Color(0xFF191A20);
  static const Color _accent = Color(0xFFFF765F);

  bool get _summaryFoldable => widget.summary.trim().length > _summaryFoldLength;

  /// 折叠时只显示"当前集所在的那一屏"，避免几十集一次全铺出来。
  List<int> get _visibleEpisodes {
    final total = widget.episodes.length;
    if (_episodesOpen || total <= _collapsedCells) {
      return List<int>.generate(total, (index) => index);
    }
    var current = widget.currentIndex;
    if (current < 0) current = 0;
    if (current > total - 1) current = total - 1;
    var start = (current ~/ _collapsedCells) * _collapsedCells;
    final lastStart = total - _collapsedCells;
    if (start > lastStart) start = lastStart;
    final count = math.min(_collapsedCells, total - start);
    return List<int>.generate(count, (offset) => start + offset);
  }

  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: _surface,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 28),
        children: [
          _header(),
          if (widget.meta.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              widget.meta.join(' · '),
              key: const ValueKey('player-info-meta'),
              style: const TextStyle(
                color: Colors.white38,
                fontSize: 12,
                height: 1.4,
              ),
            ),
          ],
          if (widget.actions.isNotEmpty) ...[
            const SizedBox(height: 14),
            _actionRow(),
          ],
          if (widget.summary.trim().isNotEmpty) ...[
            const SizedBox(height: 16),
            _divider(),
            const SizedBox(height: 12),
            const Text(
              '简介',
              style: TextStyle(
                color: Colors.white,
                fontSize: 13,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              widget.summary.trim(),
              key: const ValueKey('player-info-summary'),
              maxLines: _summaryOpen ? null : 3,
              overflow: _summaryOpen ? null : TextOverflow.ellipsis,
              style: const TextStyle(
                color: Colors.white70,
                fontSize: 13,
                height: 1.55,
              ),
            ),
            if (_summaryFoldable)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton(
                  key: const ValueKey('player-info-summary-toggle'),
                  onPressed: () => setState(() => _summaryOpen = !_summaryOpen),
                  style: TextButton.styleFrom(
                    foregroundColor: _accent,
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    minimumSize: const Size(0, 32),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: Text(_summaryOpen ? '收起' : '展开全部'),
                ),
              ),
          ],
          if (widget.episodes.isNotEmpty) ...[
            const SizedBox(height: 12),
            _divider(),
            const SizedBox(height: 12),
            _episodeHeader(),
            const SizedBox(height: 10),
            _episodeGrid(),
          ],
        ],
      ),
    );
  }

  Widget _divider() => const Divider(height: 1, color: Color(0xFF26272F));

  Widget _header() {
    final row = Row(
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Expanded(
          child: Text(
            widget.title,
            key: const ValueKey('player-info-title'),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              color: Colors.white,
              fontSize: 17,
              fontWeight: FontWeight.w700,
              height: 1.3,
            ),
          ),
        ),
        if (widget.onOpenDetail != null)
          IconButton(
            key: const ValueKey('player-info-detail'),
            tooltip: '查看详情',
            onPressed: widget.onOpenDetail,
            color: Colors.white54,
            iconSize: 22,
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.chevron_right_rounded),
          ),
      ],
    );
    if (widget.onOpenDetail == null) return row;
    return Semantics(
      button: true,
      label: '${widget.title}，查看详情',
      child: InkWell(
        onTap: widget.onOpenDetail,
        borderRadius: BorderRadius.circular(6),
        child: row,
      ),
    );
  }

  Widget _actionRow() => Wrap(
    spacing: 8,
    runSpacing: 8,
    children: [
      for (final action in widget.actions) _actionChip(action),
    ],
  );

  Widget _actionChip(PlayerInfoAction action) {
    final color = action.active ? _accent : Colors.white70;
    return Material(
      color: action.active
          ? _accent.withValues(alpha: .16)
          : Colors.white.withValues(alpha: .06),
      borderRadius: BorderRadius.circular(999),
      child: InkWell(
        key: action.key,
        onTap: action.onPressed,
        borderRadius: BorderRadius.circular(999),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(action.icon, size: 17, color: color),
              const SizedBox(width: 5),
              Text(
                action.label,
                style: TextStyle(
                  color: color,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _episodeHeader() => Row(
    children: [
      const Text(
        '选集',
        style: TextStyle(
          color: Colors.white,
          fontSize: 13,
          fontWeight: FontWeight.w700,
        ),
      ),
      const SizedBox(width: 8),
      Expanded(
        child: Text(
          '共 ${widget.episodes.length} 集',
          style: const TextStyle(color: Colors.white38, fontSize: 12),
        ),
      ),
      if (widget.onOpenAllEpisodes != null)
        TextButton(
          key: const ValueKey('player-info-all-episodes'),
          onPressed: widget.onOpenAllEpisodes,
          style: TextButton.styleFrom(
            foregroundColor: Colors.white70,
            padding: const EdgeInsets.symmetric(horizontal: 6),
            minimumSize: const Size(0, 32),
            tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          ),
          child: const Text('全部', style: TextStyle(fontSize: 12)),
        ),
      if (widget.episodes.length > _collapsedCells)
        TextButton(
          key: const ValueKey('player-info-episodes-toggle'),
          onPressed: () => setState(() => _episodesOpen = !_episodesOpen),
          style: TextButton.styleFrom(
            foregroundColor: _accent,
            padding: const EdgeInsets.symmetric(horizontal: 6),
            minimumSize: const Size(0, 32),
            tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          ),
          child: Text(
            _episodesOpen ? '收起' : '展开',
            style: const TextStyle(fontSize: 12),
          ),
        ),
    ],
  );

  Widget _episodeGrid() => LayoutBuilder(
    builder: (context, constraints) {
      // 手机上固定 5 列：短剧集数多，5 列一屏能看 15 格，不用一直滑。
      final columns = constraints.maxWidth >= 520 ? 6 : 5;
      return GridView.builder(
        shrinkWrap: true,
        // 外层已经是 ListView，这里不能再自己滚。
        physics: const NeverScrollableScrollPhysics(),
        padding: EdgeInsets.zero,
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: columns,
          mainAxisSpacing: 8,
          crossAxisSpacing: 8,
          childAspectRatio: 1.6,
        ),
        itemCount: _visibleEpisodes.length,
        itemBuilder: (context, position) {
          final index = _visibleEpisodes[position];
          final selected = index == widget.currentIndex;
          return _episodeCell(index, selected);
        },
      );
    },
  );

  Widget _episodeCell(int index, bool selected) {
    final episode = widget.episodes[index];
    return Material(
      color: selected
          ? _accent.withValues(alpha: .18)
          : Colors.white.withValues(alpha: .05),
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        key: ValueKey('player-info-episode-$index'),
        borderRadius: BorderRadius.circular(8),
        onTap: () => widget.onSelectEpisode(index),
        child: Container(
          alignment: Alignment.center,
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(8),
            border: Border.all(
              color: selected ? _accent : Colors.transparent,
              width: 1.2,
            ),
          ),
          child: Text(
            '${episode.number}',
            style: TextStyle(
              color: selected ? _accent : Colors.white70,
              fontSize: 13,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            ),
          ),
        ),
      ),
    );
  }
}

/// 信息面板上的一个操作按钮。
class PlayerInfoAction {
  const PlayerInfoAction({
    required this.icon,
    required this.label,
    required this.onPressed,
    this.active = false,
    this.key,
  });

  final IconData icon;
  final String label;
  final VoidCallback onPressed;

  /// 是否处于"已开启"状态（如已追剧、连播本剧），用品牌橙高亮。
  final bool active;
  final Key? key;
}
