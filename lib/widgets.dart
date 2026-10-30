import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'core_bridge.dart';
import 'app_layout.dart';
import 'models.dart';
import 'remote_widgets.dart';

Future<void> saveUserChange(
  BuildContext context,
  Future<void> Function() action,
) async {
  try {
    await action();
  } catch (_) {
    if (context.mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('未能保存，请检查存储空间和权限后重试。')));
    }
  }
}

class RefreshAction extends StatefulWidget {
  const RefreshAction({
    super.key,
    required this.loading,
    required this.tooltip,
    required this.onPressed,
  });
  final bool loading;
  final String tooltip;
  final VoidCallback? onPressed;

  @override
  State<RefreshAction> createState() => _RefreshActionState();
}

class _RefreshActionState extends State<RefreshAction>
    with SingleTickerProviderStateMixin {
  late final AnimationController _rotation;

  @override
  void initState() {
    super.initState();
    _rotation = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 1),
    );
    if (widget.loading) _rotation.repeat();
  }

  @override
  void didUpdateWidget(covariant RefreshAction oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.loading == oldWidget.loading) return;
    if (widget.loading) {
      _rotation.repeat();
    } else {
      _rotation.reset();
    }
  }

  @override
  void dispose() {
    _rotation.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    value: widget.loading ? '正在更新' : null,
    child: IconButton(
      tooltip: widget.loading ? '正在更新' : widget.tooltip,
      onPressed: widget.loading ? null : widget.onPressed,
      disabledColor: widget.loading
          ? Theme.of(context).colorScheme.primary
          : null,
      icon: RotationTransition(
        turns: _rotation,
        child: const Icon(Icons.refresh_rounded),
      ),
    ),
  );
}

class DramaCover extends StatelessWidget {
  const DramaCover({
    super.key,
    required this.drama,
    required this.repository,
    this.radius = 14,
  });
  final Drama drama;
  final AppRepository repository;
  final double radius;
  static const imagesDisabled = bool.fromEnvironment('DISABLE_REMOTE_IMAGES');

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final placeholder = Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [colors.surfaceContainerHighest, colors.surfaceContainer],
        ),
      ),
      child: Center(
        child: Icon(
          Icons.movie_creation_outlined,
          size: 40,
          color: colors.onSurfaceVariant,
        ),
      ),
    );
    return ClipRRect(
      borderRadius: BorderRadius.circular(radius),
      child: Stack(
        fit: StackFit.expand,
        children: [
          placeholder,
          if (!imagesDisabled && drama.id.isNotEmpty)
            CachedCoverImage(
              key: ValueKey('${drama.id}\u0000${drama.cover}'),
              drama: drama,
              repository: repository,
              placeholder: placeholder,
            ),
          Positioned.fill(
            child: IgnorePointer(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.center,
                    end: Alignment.bottomCenter,
                    colors: [
                      Colors.transparent,
                      Colors.black.withValues(alpha: .78),
                    ],
                  ),
                ),
              ),
            ),
          ),
          if (drama.episodes > 0)
            Positioned(
              left: 9,
              bottom: 9,
              child: Text(
                '共 ${drama.episodes} 集',
                style: const TextStyle(color: Colors.white, fontSize: 12),
              ),
            ),
          if (drama.vip)
            Positioned(
              left: 8,
              top: 8,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
                decoration: BoxDecoration(
                  color: const Color(0xFFF6C86B),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: const Text(
                  'VIP',
                  style: TextStyle(
                    color: Color(0xFF40300D),
                    fontSize: 11,
                    fontWeight: FontWeight.w800,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class CachedCoverImage extends StatefulWidget {
  const CachedCoverImage({
    super.key,
    required this.drama,
    required this.repository,
    required this.placeholder,
  });
  final Drama drama;
  final AppRepository repository;
  final Widget placeholder;
  @override
  State<CachedCoverImage> createState() => _CachedCoverImageState();
}

class _CachedCoverImageState extends State<CachedCoverImage> {
  late Future<String> _file;
  int _coverRevision = 0;
  bool _coverFailed = false;
  bool _retryOnFailure = false;
  bool _retryQueued = false;
  String? _failedPath;

  @override
  void initState() {
    super.initState();
    _coverRevision = widget.repository.catalogUpdates.coverRevision(
      widget.drama.id,
    );
    widget.repository.catalogUpdates.addListener(_metadataChanged);
    _file = _requestCover();
  }

  @override
  void didUpdateWidget(covariant CachedCoverImage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.repository != widget.repository) {
      oldWidget.repository.catalogUpdates.removeListener(_metadataChanged);
      widget.repository.catalogUpdates.addListener(_metadataChanged);
    }
    if (oldWidget.repository != widget.repository ||
        oldWidget.drama.id != widget.drama.id ||
        oldWidget.drama.cover != widget.drama.cover ||
        oldWidget.drama.source != widget.drama.source) {
      _coverFailed = _retryOnFailure = false;
      _failedPath = null;
      _coverRevision = widget.repository.catalogUpdates.coverRevision(
        widget.drama.id,
      );
      _file = _requestCover();
    }
  }

  /// 请求封面。站源未返回海报时会抛错，这里统一转成失败的 Future，
  /// 交给 FutureBuilder 走重试界面，避免未捕获异常。
  Future<String> _requestCover({bool force = false}) {
    final drama = force
        ? widget.repository.catalogUpdates.current(widget.drama)
        : widget.drama;
    late final Future<String> request;
    try {
      request = widget.repository.cover(drama, force: force);
    } catch (_) {
      return Future<String>.error(AppFailure('海报暂不可用'));
    }
    // 在交给 FutureBuilder 之前先挂一个空处理器，避免 Future 先完成时报错
    // 被判定为未处理异常；FutureBuilder 仍可正常读到同一个错误。
    unawaited(request.then<void>((_) {}, onError: (Object _) {}));
    return request;
  }

  void _metadataChanged() {
    final revision = widget.repository.catalogUpdates.coverRevision(
      widget.drama.id,
    );
    if (revision == _coverRevision) return;
    _coverRevision = revision;
    _retryOnFailure = true;
    if (_coverFailed) _queueRetry();
  }

  void _queueRetry() {
    if (_retryQueued || !_retryOnFailure) return;
    _retryQueued = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _retryQueued = false;
      if (!mounted || !_retryOnFailure) return;
      _retryOnFailure = false;
      _retry(_failedPath);
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  @override
  void dispose() {
    widget.repository.catalogUpdates.removeListener(_metadataChanged);
    super.dispose();
  }

  Future<void> _retry(String? path) async {
    if (path != null) {
      await ResizeImage.resizeIfNeeded(
        440,
        null,
        FileImage(File(path)),
      ).evict();
    }
    // 先安全地取得 Future 再更新状态：站源未返回海报时会同步抛错，
    // 若直接写进 setState 会成为未捕获异常。
    final next = _requestCover(force: true);
    if (!mounted) return;
    setState(() {
      _coverFailed = _retryOnFailure = false;
      _failedPath = null;
      _file = next;
    });
  }

  Widget _failed(String? path) {
    _coverFailed = true;
    _failedPath = path;
    if (_retryOnFailure) _queueRetry();
    return Center(
      child: IconButton(
        tooltip: '重试海报',
        onPressed: () => _retry(path),
        icon: Icon(
          Icons.refresh_rounded,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => FutureBuilder<String>(
    future: _file,
    builder: (context, snapshot) {
      if (snapshot.connectionState != ConnectionState.done) {
        return widget.placeholder;
      }
      if (snapshot.hasError || !snapshot.hasData) {
        return _failed(null);
      }
      return Image.file(
        File(snapshot.data!),
        fit: BoxFit.cover,
        cacheWidth: 440,
        excludeFromSemantics: true,
        errorBuilder: (_, error, stack) => _failed(snapshot.data),
      );
    },
  );
}

class DramaTile extends StatelessWidget {
  const DramaTile({
    super.key,
    required this.drama,
    required this.repository,
    required this.onTap,
    this.subtitle,
    this.focusNode,
    this.onFocus,
    this.actions,
    this.badge,
    this.selected,
    this.onMore,
    this.onLongPress,
  });
  final Drama drama;
  final AppRepository repository;
  final VoidCallback onTap;
  final String? subtitle;
  final FocusNode? focusNode;
  final VoidCallback? onFocus;
  final Widget? actions;
  final String? badge;
  final bool? selected;
  final VoidCallback? onMore;
  final VoidCallback? onLongPress;

  static double titleHeight(BuildContext context) =>
      MediaQuery.textScalerOf(context).scale(14) * 2.6;

  static double subtitleHeight(BuildContext context) =>
      MediaQuery.textScalerOf(context).scale(12) * 1.3;

  static double extentFor(BuildContext context, double width) {
    // 0.8.9：焦点装饰不再占用额外空间（`RemoteTarget.chrome` 为 0），
    // 格子高度 = 封面 2:3 + 间距 + 两行文字，不再预留焦点环的 8px。
    final chrome = AppLayout.isTelevision(context) ? RemoteTarget.chrome : 0.0;
    return ((width - chrome) * 1.5 +
            13 +
            titleHeight(context) +
            subtitleHeight(context) +
            chrome)
        .ceilToDouble();
  }

  @override
  Widget build(BuildContext context) {
    final television = AppLayout.isTelevision(context);
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        AspectRatio(
          aspectRatio: 2 / 3,
          child: Stack(
            fit: StackFit.expand,
            children: [
              DramaCover(drama: drama, repository: repository),
              if (badge != null && badge!.isNotEmpty)
                Positioned(
                  left: 6,
                  right: 6,
                  bottom: 6,
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      color: Colors.black.withValues(alpha: .72),
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 4,
                      ),
                      child: Text(
                        badge!,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          color: Colors.white,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  ),
                ),
              if (actions != null && selected == null)
                Positioned(top: 2, right: 2, child: actions!),
              if (selected != null)
                Positioned(
                  top: 6,
                  right: 6,
                  child: CircleAvatar(
                    radius: 16,
                    backgroundColor: selected!
                        ? Theme.of(context).colorScheme.primary
                        : Colors.black.withValues(alpha: .64),
                    child: Icon(
                      selected! ? Icons.check_rounded : Icons.circle_outlined,
                      size: 22,
                      color: selected!
                          ? Theme.of(context).colorScheme.onPrimary
                          : Colors.white,
                    ),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: 9),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 2),
          child: SizedBox(
            height: titleHeight(context),
            child: Text(
              drama.title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                fontWeight: FontWeight.w600,
                height: 1.3,
                fontSize: 14,
              ),
            ),
          ),
        ),
        const SizedBox(height: 4),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 2),
          child: SizedBox(
            height: subtitleHeight(context),
            child: Text(
              subtitle ?? drama.category,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 12,
                height: 1.3,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
        ),
      ],
    );
    if (television) {
      return CallbackShortcuts(
        bindings: {
          const SingleActivator(LogicalKeyboardKey.contextMenu): ?onMore,
        },
        child: RemoteTarget(
          focusNode: focusNode,
          onFocus: onFocus,
          onPressed: onTap,
          selected: selected ?? false,
          // 0.8.9：海报用四角标记（不画整圈边框），焦点样式完全落在
          // 卡片自身范围内，不会被网格视口裁掉。
          focusStyle: RemoteFocusStyle.corners,
          label:
              '${drama.title}${drama.episodes > 0 ? '，${drama.episodes}集' : ''}'
              '${badge == null ? '' : '，$badge'}',
          child: content,
        ),
      );
    }
    return Semantics(
      button: true,
      selected: selected,
      label: '${drama.title}${drama.episodes > 0 ? '，${drama.episodes}集' : ''}',
      child: InkWell(
        onTap: onTap,
        onLongPress: onLongPress ?? onMore,
        onSecondaryTap: onMore,
        borderRadius: BorderRadius.circular(14),
        child: content,
      ),
    );
  }
}

class StatusPanel extends StatelessWidget {
  const StatusPanel({
    super.key,
    required this.title,
    this.message = '',
    this.onRetry,
    this.icon = Icons.movie_filter_outlined,
    this.action = '重试',
    this.secondaryAction,
  });
  final String title;
  final String message;
  final VoidCallback? onRetry;
  final IconData icon;
  final String action;
  final Widget? secondaryAction;
  @override
  Widget build(BuildContext context) => Center(
    child: SingleChildScrollView(
      padding: const EdgeInsets.all(28),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 56, color: Theme.of(context).colorScheme.primary),
          const SizedBox(height: 20),
          Text(
            title,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.titleLarge,
          ),
          if (message.isNotEmpty) ...[
            const SizedBox(height: 12),
            Text(
              message,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ],
          if (onRetry != null) ...[
            const SizedBox(height: 24),
            FilledButton.icon(
              autofocus: AppLayout.isTelevision(context),
              onPressed: onRetry,
              icon: const Icon(Icons.refresh_rounded),
              label: Text(action),
            ),
          ],
          if (secondaryAction != null) ...[
            const SizedBox(height: 8),
            secondaryAction!,
          ],
        ],
      ),
    ),
  );
}

SliverGridDelegate dramaGridDelegate(BuildContext context, double width) {
  final columns = width < 600 ? 3 : (width / 180).floor().clamp(4, 9);
  final spacing = width < 600 ? 10.0 : 18.0;
  final tileWidth = (width - (columns - 1) * spacing) / columns;
  return SliverGridDelegateWithFixedCrossAxisCount(
    crossAxisCount: columns,
    mainAxisSpacing: 22,
    crossAxisSpacing: spacing,
    mainAxisExtent: DramaTile.extentFor(context, tileWidth),
  );
}

/// 剧集网格骨架屏。
///
/// 用途：首次加载（尤其是站源较慢时）先渲染与真实卡片同尺寸的灰块，
/// 让用户在等待网络期间看到"界面已就绪、内容马上到"，
/// 而不是一个孤零零的转圈，减少等待焦虑。
///
/// 说明：只做视觉占位，不含任何交互；数据到达后被真实列表替换。
class DramaGridSkeleton extends StatefulWidget {
  const DramaGridSkeleton({super.key, this.count = 9});

  /// 占位卡片数量。默认 9 张，恰好铺满多数手机首屏。
  final int count;

  @override
  State<DramaGridSkeleton> createState() => _DramaGridSkeletonState();
}

class _DramaGridSkeletonState extends State<DramaGridSkeleton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1200),
  )..repeat(reverse: true);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    // 用主题色派生灰阶，深浅色模式都能看清。
    final base = colorScheme.onSurface.withValues(alpha: .06);
    final highlight = colorScheme.onSurface.withValues(alpha: .12);
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, _) {
        final color = Color.lerp(base, highlight, _controller.value) ?? base;
        return LayoutBuilder(
          builder: (context, constraints) {
            final padding = constraints.maxWidth < 600 ? 16.0 : 24.0;
            final width = constraints.maxWidth - 2 * padding;
            return GridView.builder(
              padding: EdgeInsets.fromLTRB(padding, 0, padding, 16),
              physics: const NeverScrollableScrollPhysics(),
              gridDelegate: dramaGridDelegate(context, width),
              itemCount: widget.count,
              itemBuilder: (context, index) => _SkeletonTile(color: color),
            );
          },
        );
      },
    );
  }
}

class _SkeletonTile extends StatelessWidget {
  const _SkeletonTile({required this.color});

  final Color color;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(10),
            child: ColoredBox(color: color, child: const SizedBox.expand()),
          ),
        ),
        const SizedBox(height: 8),
        Container(height: 12, width: double.infinity, color: color),
        const SizedBox(height: 6),
        FractionallySizedBox(
          widthFactor: .6,
          alignment: Alignment.centerLeft,
          child: Container(height: 10, color: color),
        ),
      ],
    );
  }
}

String formatPosition(double seconds) {
  final value = seconds.isFinite ? seconds.toInt().clamp(0, 999999) : 0;
  return '${value ~/ 60}:${(value % 60).toString().padLeft(2, '0')}';
}

/// 播放页的加载动效（0.8.16 第三批）。
///
/// 与 [LoadingHint] 的分工：列表 / 首屏那种"不知道在等什么"的场景需要
/// 文字说明；**播放页不需要** —— 用户已经点进来看这一集了，他在等视频
/// 这件事是明摆着的。抖音 / 红果的加载态就是画面正中一个细环在转，
/// 没有一个字。多一行「正在加载视频…」只是噪音，还会把"卡住了"这件事
/// 显得更重。
///
/// [tv] 传 true 时整体放大，10 英尺外也看得清。
class PlaybackLoading extends StatelessWidget {
  const PlaybackLoading({super.key, this.tv = false});

  /// 是否电视端（放大动效）。
  final bool tv;

  @override
  Widget build(BuildContext context) {
    final size = tv ? 64.0 : 42.0;
    // 画面上一个字都不要，但**读屏要能念出来**：无障碍标签不算文案，
    // TalkBack / 读屏用户同样需要知道"在等视频"。
    return Semantics(
      label: '正在加载视频',
      container: true,
      child: Center(
        child: SizedBox(
          width: size,
          height: size,
          child: CircularProgressIndicator(
            strokeWidth: tv ? 4.5 : 3.2,
            color: Colors.white70,
            // 淡淡的底圈：弱光环境下也能看出"这里有个环在转"，
            // 而不是一截孤零零的弧线在飘。
            backgroundColor: Colors.white12,
          ),
        ),
      ),
    );
  }
}

/// 统一的加载提示：转圈 + 一句说明「正在做什么」。
///
/// 用途：用户明确要求所有等待都要有文字说明，避免"不知道在等什么"。
/// 各页面只需传入一句中文文案即可，样式集中在这里维护，
/// 避免每个页面各写一套造成重复代码。
class LoadingHint extends StatelessWidget {
  const LoadingHint({
    super.key,
    required this.message,
    this.dark = false,
    this.compact = false,
  });

  /// 正在做什么，例如「正在加载首页内容…」。
  final String message;

  /// 是否用于深色背景（播放器、首页竖滑流），为 true 时文字用浅色。
  final bool dark;

  /// 紧凑模式：只显示一行较小的转圈+文字，适合嵌在列表/卡片内部。
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final television = AppLayout.isTelevision(context);
    final color = dark
        ? Colors.white70
        : Theme.of(context).colorScheme.onSurfaceVariant;
    final indicator = SizedBox(
      width: compact ? 16 : (television ? 44 : 34),
      height: compact ? 16 : (television ? 44 : 34),
      child: CircularProgressIndicator(
        strokeWidth: compact ? 2 : 3,
        color: dark ? Colors.white70 : null,
      ),
    );
    final label = Text(
      message,
      textAlign: TextAlign.center,
      style: TextStyle(
        color: color,
        // 0.8.1：电视上提示文字放大，10 英尺外也能读。
        fontSize: compact ? 12 : (television ? 18 : 14),
      ),
    );
    if (compact) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          indicator,
          const SizedBox(width: 10),
          Flexible(child: label),
        ],
      );
    }
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          indicator,
          const SizedBox(height: 18),
          label,
        ],
      ),
    );
  }
}

/// 圆形内嵌的紧凑加载提示（用于按钮/小区域内的等待反馈）。
class InlineLoadingHint extends StatelessWidget {
  const InlineLoadingHint({super.key, this.message});

  final String? message;

  @override
  Widget build(BuildContext context) {
    if (message == null || message!.isEmpty) {
      return const SizedBox(
        width: 18,
        height: 18,
        child: CircularProgressIndicator(strokeWidth: 2),
      );
    }
    return LoadingHint(message: message!, compact: true);
  }
}
