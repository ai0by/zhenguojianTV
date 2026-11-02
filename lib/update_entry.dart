import 'dart:async';

import 'package:flutter/material.dart';

import 'update_downloader.dart';
import 'updater.dart';

/// 「关于真果鉴TV」里的检查更新入口（0.8.18，0.8.20 改后台下载）。
///
/// 用户明确要求：**只有点这里才检查**，不做启动自动检查、不弹任何提醒。
/// 所以这个组件不注册任何定时器、不在 initState 里发请求 —— 只有按下
/// 「检查更新」按钮才会走网络。
///
/// 状态流转：
/// 未检查 → 检查中 → 已是最新 / 发现新版本 → 下载中 → 待安装 → 唤起安装器
///                  ↘ 失败（可重试）
///
/// 0.8.20：**下载不再由这个组件持有**。真正的下载挂在 `UpdateDownloader`
/// 单例上，这里只订阅它的状态。好处是关掉对话框（State 被 dispose）下载
/// 照样跑完，重新打开就能接着看进度、或者直接点「安装」。
class UpdateEntry extends StatefulWidget {
  const UpdateEntry({super.key, required this.currentVersion});

  final String currentVersion;

  @override
  State<UpdateEntry> createState() => _UpdateEntryState();
}

enum _Stage {
  idle,
  checking,
  latest,
  available,
  downloading,
  readyToInstall,
  installing,
  failed,
}

class _UpdateEntryState extends State<UpdateEntry> {
  final UpdateDownloader _downloader = UpdateDownloader.instance;

  _Stage _stage = _Stage.idle;
  UpdateCheckResult? _result;
  String _message = '';

  @override
  void initState() {
    super.initState();
    _downloader.addListener(_onDownloadChanged);
  }

  @override
  void dispose() {
    _downloader.removeListener(_onDownloadChanged);
    super.dispose();
  }

  /// 下载进度全来自全局下载器；这里只负责重建。
  ///
  /// 重新打开对话框时 initState 一订阅就拿到当前进度，不需要任何恢复逻辑。
  void _onDownloadChanged() {
    if (mounted) setState(() {});
  }

  /// 真正要显示的阶段：下载器的状态优先级高于本页自己的检查结果。
  _Stage get _displayStage {
    switch (_downloader.stage) {
      case UpdateDownloadStage.probing:
      case UpdateDownloadStage.downloading:
      case UpdateDownloadStage.verifying:
        return _Stage.downloading;
      case UpdateDownloadStage.ready:
        return _Stage.readyToInstall;
      case UpdateDownloadStage.failed:
        return _Stage.failed;
      case UpdateDownloadStage.idle:
        return _stage;
    }
  }

  String get _displayMessage =>
      _downloader.stage == UpdateDownloadStage.failed
      ? _downloader.error
      : _message;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final stage = _displayStage;
    final message = _displayMessage;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Expanded(child: _statusText(theme, stage)),
            const SizedBox(width: 8),
            _actionButton(stage),
          ],
        ),
        if (stage == _Stage.downloading) ...[
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: _downloader.progress <= 0
                  ? null
                  : _downloader.progress,
            ),
          ),
          Align(
            alignment: Alignment.centerRight,
            child: TextButton(
              onPressed: _downloader.cancel,
              child: const Text('取消下载'),
            ),
          ),
        ],
        if (message.isNotEmpty &&
            (stage == _Stage.failed || stage == _Stage.readyToInstall)) ...[
          const SizedBox(height: 6),
          Text(
            message,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.error,
            ),
          ),
        ],
      ],
    );
  }

  Widget _statusText(ThemeData theme, _Stage stage) {
    final style = theme.textTheme.bodyMedium;
    switch (stage) {
      case _Stage.idle:
        return Text('当前版本 ${widget.currentVersion}', style: style);
      case _Stage.checking:
        return Text('正在检查更新…', style: style);
      case _Stage.latest:
        return Text('已是最新版本 ${widget.currentVersion}', style: style);
      case _Stage.available:
        final release = _result?.release;
        return Text(
          '发现新版本 ${release?.version ?? ''}',
          style: style?.copyWith(
            color: theme.colorScheme.primary,
            fontWeight: FontWeight.w600,
          ),
        );
      case _Stage.downloading:
        return Text(_downloadText(), style: style);
      case _Stage.readyToInstall:
        final version = _downloader.version;
        return Text(
          version.isEmpty ? '安装包已下载完成' : '新版本 $version 已下载完成',
          style: style?.copyWith(
            color: theme.colorScheme.primary,
            fontWeight: FontWeight.w600,
          ),
        );
      case _Stage.installing:
        return Text('已唤起系统安装器', style: style);
      case _Stage.failed:
        return Text(
          _downloader.stage == UpdateDownloadStage.failed ? '下载失败' : '检查更新',
          style: style,
        );
    }
  }

  /// 把"测速中 / 下载中 / 校验中 + 速度 + 当前节点"拼成一行。
  ///
  /// 用户抱怨过"下载速度过于慢"，所以把速度和新节点直接摆出来 —— 看得见
  /// 在换节点、看得见速度有没有起来，比一个不动的转圈强。
  String _downloadText() {
    final downloader = _downloader;
    if (downloader.stage == UpdateDownloadStage.probing) {
      return '正在挑选最快的下载节点…';
    }
    if (downloader.stage == UpdateDownloadStage.verifying) {
      return '正在校验安装包…';
    }
    final parts = <String>[
      '正在下载… ${(downloader.progress * 100).toStringAsFixed(0)}%',
    ];
    if (downloader.bytesPerSecond > 0) {
      parts.add(_speedText(downloader.bytesPerSecond));
    }
    final host = _hostOf(downloader.mirror);
    if (host.isNotEmpty) parts.add(host);
    return parts.join(' · ');
  }

  static String _speedText(int bytesPerSecond) {
    if (bytesPerSecond >= 1 << 20) {
      return '${(bytesPerSecond / (1 << 20)).toStringAsFixed(1)} MB/s';
    }
    return '${(bytesPerSecond / 1024).round()} KB/s';
  }

  static String _hostOf(String url) {
    if (url.isEmpty) return '';
    return Uri.tryParse(url)?.host ?? '';
  }

  Widget _actionButton(_Stage stage) {
    switch (stage) {
      case _Stage.checking:
      case _Stage.downloading:
        return const SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        );
      case _Stage.available:
        return FilledButton.tonal(
          onPressed: _startDownload,
          child: const Text('下载并安装'),
        );
      case _Stage.readyToInstall:
        return FilledButton(onPressed: _install, child: const Text('安装'));
      case _Stage.installing:
        return TextButton(onPressed: _check, child: const Text('重新检查'));
      case _Stage.failed:
        // 检查失败就重试检查，下载失败就重试下载 —— 别让用户多点一步。
        if (_result != null &&
            _downloader.stage == UpdateDownloadStage.failed) {
          return FilledButton.tonal(
            onPressed: _startDownload,
            child: const Text('重试下载'),
          );
        }
        return TextButton.icon(
          onPressed: _check,
          icon: const Icon(Icons.refresh_rounded, size: 18),
          label: const Text('重试'),
        );
      case _Stage.idle:
      case _Stage.latest:
        return TextButton.icon(
          onPressed: _check,
          icon: const Icon(Icons.refresh_rounded, size: 18),
          label: const Text('检查更新'),
        );
    }
  }

  Future<void> _check() async {
    final current = _displayStage;
    if (current == _Stage.checking || current == _Stage.downloading) return;
    // 上一次的下载结论（失败 / 已下完）不该盖住这次检查的结果。
    if (_downloader.stage != UpdateDownloadStage.idle) _downloader.reset();
    setState(() {
      _stage = _Stage.checking;
      _message = '';
      _result = null;
    });
    try {
      final result = await Updater.check(currentVersion: widget.currentVersion);
      if (!mounted) return;
      setState(() {
        if (result == null) {
          _stage = _Stage.latest;
        } else {
          _result = result;
          _stage = _Stage.available;
        }
      });
    } on UpdateException catch (error) {
      if (!mounted) return;
      setState(() {
        _stage = _Stage.failed;
        _message = error.message;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _stage = _Stage.failed;
        _message = '检查更新失败，请稍后再试';
      });
    }
  }

  Future<void> _startDownload() async {
    final result = _result;
    if (result == null) return;
    final environment = await Updater.environment();
    final asset = Updater.pickAsset(
      result.release,
      versionCode: environment.versionCode,
      abi: environment.abi,
    );
    if (asset == null) {
      if (!mounted) return;
      setState(() {
        _stage = _Stage.failed;
        _message = '这个版本没有适合本机架构的安装包';
      });
      return;
    }
    // 故意不 await：下载挂在全局下载器上，这个对话框随时可以关掉。
    unawaited(
      _downloader.start(
        release: result.release,
        asset: asset,
        mirrors: result.mirrors,
      ),
    );
  }

  Future<void> _install() async {
    final downloaded = _downloader.file;
    if (downloaded == null) return;
    if (!await Updater.canInstall()) {
      await Updater.openInstallSettings();
      if (!mounted) return;
      // 下载器仍然停在 ready（文件还在，允许安装后可以直接点「安装」），
      // 所以这里只留提示，不把阶段改成失败。
      setState(() {
        _message = '请在系统设置里允许「安装未知应用」，然后回来再点一次「安装」';
      });
      return;
    }
    final opened = await Updater.install(downloaded.path);
    // 装成功就把下载器复位，否则 _displayStage 会一直停在"待安装"。
    if (opened) _downloader.reset();
    if (!mounted) return;
    setState(() {
      _stage = opened ? _Stage.installing : _Stage.failed;
      _message = opened ? '' : '无法唤起安装器，请手动安装：${downloaded.path}';
    });
  }
}
