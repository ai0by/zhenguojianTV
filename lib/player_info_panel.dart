import 'package:flutter/material.dart';

import 'models.dart';

/// 小屏（竖屏）播放页下方的信息面板（0.8.13，0.8.14 改版）。
///
/// 借鉴红果短剧竖屏播放页的分工：
/// - **全屏只留播放器**（本面板不参与）；
/// - 非全屏时视频按自身比例固定在上方，下方是信息区。
///
/// 0.8.14 改版的原因：0.8.13 把**选集网格直接铺在滚动区里**，短剧动辄几十集，
/// 用户要一路往下滑才看得到选集，而且网格把简介挤在中间、滚动时到处乱跑。
/// 现在按红果的做法拆成三段：
/// 1. 剧名 / 元信息 / 一行图标操作（追剧、连播、倍速、清晰度、弹幕）；
/// 2. 简介（默认 3 行，可展开）；
/// 3. **固定在面板底部的选集条** —— 点它从底部弹出选集面板（`_openEpisodes`）。
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

  /// 全部剧集（只用来算集数，不再内嵌网格）。
  final List<Episode> episodes;

  /// 当前播放的集索引。
  final int currentIndex;

  /// 操作按钮（追剧 / 连播 / 倍速 / 清晰度 / 弹幕 …）。
  final List<PlayerInfoAction> actions;

  /// 点某一集（保留给外部扩展，当前底部条走 [onOpenAllEpisodes]）。
  final ValueChanged<int> onSelectEpisode;

  /// 点标题进详情页；为空时不显示箭头。
  final VoidCallback? onOpenDetail;

  /// 点底部选集条（打开完整的选集弹层）。
  final VoidCallback? onOpenAllEpisodes;

  @override
  State<PlayerInfoPanel> createState() => _PlayerInfoPanelState();
}

class _PlayerInfoPanelState extends State<PlayerInfoPanel> {
  /// 简介是否已展开。
  bool _summaryOpen = false;

  /// 超过这个长度才给「展开」按钮。
  static const int _summaryFoldLength = 90;

  static const Color _surface = Color(0xFF191A20);
  static const Color _barSurface = Color(0xFF20212A);
  static const Color _accent = Color(0xFFFF765F);

  bool get _summaryFoldable => widget.summary.trim().length > _summaryFoldLength;

  /// 选集条上显示的当前集号（1 起）。
  int get _currentEpisodeNumber {
    final total = widget.episodes.length;
    if (total == 0) return 0;
    final index = widget.currentIndex;
    if (index < 0 || index >= total) return 1;
    return index + 1;
  }

  @override
  Widget build(BuildContext context) {
    return Material(
      color: _surface,
      child: Column(
        children: [
          Expanded(
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
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
                  const SizedBox(height: 12),
                  _actionRow(),
                ],
                if (widget.summary.trim().isNotEmpty) ...[
                  const SizedBox(height: 14),
                  _divider(),
                  const SizedBox(height: 12),
                  _summaryBlock(),
                ],
              ],
            ),
          ),
          if (widget.episodes.isNotEmpty && widget.onOpenAllEpisodes != null)
            _episodeBar(),
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

  /// 一行等宽的图标操作（红果风格：图标在上、文字在下）。
  ///
  /// 为什么不用药丸 chip：短剧操作项固定 4~5 个，等宽排布位置恒定，
  /// 切换「连播本剧 / 连播下一部」这种长短不一的文案时不会整体位移。
  Widget _actionRow() => Row(
    children: [
      for (final action in widget.actions)
        Expanded(child: _actionTile(action)),
    ],
  );

  Widget _actionTile(PlayerInfoAction action) {
    final color = action.active ? _accent : Colors.white70;
    return InkWell(
      key: action.key,
      onTap: action.onPressed,
      borderRadius: BorderRadius.circular(10),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 2),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(action.icon, size: 22, color: color),
            const SizedBox(height: 5),
            Text(
              action.label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: color,
                fontSize: 11,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _summaryBlock() => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
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
  );

  /// 固定在面板底部的选集条（0.8.14）。
  ///
  /// 点它打开选集弹层 —— 这就是「底部点击展开选集」。条上直接给出
  /// 「第 N 集 / 共 M 集」，不用点开也能知道进度位置。
  Widget _episodeBar() {
    final total = widget.episodes.length;
    return Material(
      color: _barSurface,
      // 条贴在屏幕最底部，要给手势条留出空间；背景色包在外面，
      // 让安全区的空白也是同一个底色。
      child: SafeArea(
        top: false,
        child: InkWell(
          // key 沿用 0.8.13 的 `feed-episodes`：它就是「选集入口」。
          key: const ValueKey('feed-episodes'),
          onTap: widget.onOpenAllEpisodes,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 13, 12, 13),
            child: Row(
              children: [
                const Icon(Icons.grid_view_rounded, size: 18, color: _accent),
                const SizedBox(width: 8),
                const Text(
                  '选集',
                  style: TextStyle(
                    color: Colors.white,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    '第 $_currentEpisodeNumber 集 / 共 $total 集',
                    key: const ValueKey('player-info-episode-progress'),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: Colors.white54, fontSize: 12),
                  ),
                ),
                const Text(
                  '展开',
                  style: TextStyle(
                    color: _accent,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Icon(
                  Icons.keyboard_arrow_up_rounded,
                  size: 20,
                  color: _accent,
                ),
              ],
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
