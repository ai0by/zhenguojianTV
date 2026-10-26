import 'dart:async';

import 'package:flutter/material.dart';

import 'app_layout.dart';
import 'core_bridge.dart';
import 'local_store.dart';
import 'playback_cache.dart';
import 'widgets.dart';

/// 播放预缓存设置（0.8.12）。
///
/// 三个诉求对应三块：
///   1. 「预缓存集数」：播这一集时往后缓存几集，默认 2；
///   2. 「缓存上限」：磁盘占用天花板，超了按 LRU 自动回收；
///   3. 「当前占用 / 清理」：随时能看到吃了多少空间、一键清空。
class PlaybackCacheScreen extends StatefulWidget {
  const PlaybackCacheScreen({
    super.key,
    required this.repository,
    required this.store,
  });

  final AppRepository repository;
  final LocalStore store;

  @override
  State<PlaybackCacheScreen> createState() => _PlaybackCacheScreenState();
}

class _PlaybackCacheScreenState extends State<PlaybackCacheScreen> {
  PlaybackCacheUsage? _usage;
  bool _busy = false;
  String _message = '';

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
  }

  Future<void> _refresh() async {
    try {
      final usage = await widget.repository.mediaCacheUsage();
      if (mounted) setState(() => _usage = usage);
    } catch (error) {
      if (mounted) setState(() => _message = error.toString());
    }
  }

  Future<void> _setEpisodes(int value) => saveUserChange(
    context,
    () => widget.store.setPlaybackCachePreferences(
      widget.store.playbackCachePreferences.copyWith(episodes: value),
    ),
  );

  Future<void> _setLimit(int value) async {
    await saveUserChange(
      context,
      () => widget.store.setPlaybackCachePreferences(
        widget.store.playbackCachePreferences.copyWith(limitMB: value),
      ),
    );
    if (!mounted) return;
    try {
      final usage = await widget.repository.setMediaCacheLimit(value << 20);
      if (mounted) setState(() => _usage = usage);
    } catch (error) {
      if (mounted) setState(() => _message = error.toString());
    }
  }

  Future<void> _clear() async {
    setState(() {
      _busy = true;
      _message = '';
    });
    try {
      final usage = await widget.repository.clearMediaCache();
      if (!mounted) return;
      setState(() => _usage = usage);
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已清理预缓存')));
    } catch (error) {
      if (mounted) setState(() => _message = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (context, _) {
      final preferences = widget.store.playbackCachePreferences;
      final usage = _usage;
      return Scaffold(
        appBar: AppBar(
          automaticallyImplyLeading: !AppLayout.isTelevision(context),
          title: const Text('播放预缓存'),
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 680),
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                ListTile(
                  leading: const Icon(Icons.speed_rounded),
                  title: const Text('预缓存集数'),
                  subtitle: const Text(
                    '播这一集时，提前把后面几集下载到本机。'
                    '切集不用再等加载；手机和电视都生效。',
                  ),
                  trailing: DropdownButton<int>(
                    value: preferences.episodes,
                    underline: const SizedBox.shrink(),
                    items: [
                      for (final value in PlaybackCachePreferences.episodeOptions)
                        DropdownMenuItem(
                          value: value,
                          child: Text(value == 0 ? '关闭' : '$value 集'),
                        ),
                    ],
                    onChanged: (value) {
                      if (value != null) unawaited(_setEpisodes(value));
                    },
                  ),
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.sd_storage_outlined),
                  title: const Text('缓存上限'),
                  subtitle: const Text(
                    '超过上限后自动淘汰最久没看过的内容，不会一直变大。',
                  ),
                  trailing: DropdownButton<int>(
                    value: preferences.limitMB,
                    underline: const SizedBox.shrink(),
                    items: [
                      for (final value in PlaybackCachePreferences.limitOptions)
                        DropdownMenuItem(
                          value: value,
                          child: Text(
                            PlaybackCachePreferences(limitMB: value).limitLabel,
                          ),
                        ),
                    ],
                    onChanged: (value) {
                      if (value != null) unawaited(_setLimit(value));
                    },
                  ),
                ),
                const Divider(height: 1),
                ListTile(
                  key: const ValueKey('precache-usage'),
                  leading: const Icon(Icons.donut_large_rounded),
                  title: const Text('当前占用'),
                  subtitle: Text(
                    usage == null
                        ? '读取中…'
                        : '${usage.bytesLabel} · ${usage.files} 个分片'
                              '${usage.limitBytes > 0 ? ' · 上限 ${usage.limitLabel}' : ''}',
                  ),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      IconButton(
                        tooltip: '刷新',
                        onPressed: _busy ? null : () => unawaited(_refresh()),
                        icon: const Icon(Icons.refresh_rounded),
                      ),
                      TextButton(
                        key: const ValueKey('precache-clear'),
                        onPressed: _busy ? null : () => unawaited(_clear()),
                        child: const Text('清理'),
                      ),
                    ],
                  ),
                ),
                if (_message.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                    child: Text(
                      _message,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                const Padding(
                  padding: EdgeInsets.all(16),
                  child: Text(
                    '预缓存只保存分片，不占下载目录；删掉也不影响已下载的剧集。'
                    '移动网络下遵循「移动网络下自动播放」开关：关闭时不会自动播，'
                    '也就不会预缓存。',
                  ),
                ),
              ],
            ),
          ),
        ),
      );
    },
  );
}
