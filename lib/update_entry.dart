import 'package:flutter/material.dart';

import 'updater.dart';

/// 「关于真果鉴TV」里的检查更新入口（0.8.18）。
///
/// 用户明确要求：**只有点这里才检查**，不做启动自动检查、不弹任何提醒。
/// 所以这个组件不注册任何定时器、不在 initState 里发请求 —— 只有按下
/// 「检查更新」按钮才会走网络。
///
/// 状态流转：
/// 未检查 → 检查中 → 已是最新 / 发现新版本 → 下载中 → 唤起安装器
///                  ↘ 失败（可重试）
class UpdateEntry extends StatefulWidget {
  const UpdateEntry({super.key, required this.currentVersion});

  final String currentVersion;

  @override
  State<UpdateEntry> createState() => _UpdateEntryState();
}

enum _Stage { idle, checking, latest, available, downloading, installing, failed }

class _UpdateEntryState extends State<UpdateEntry> {
  _Stage _stage = _Stage.idle;
  UpdateCheckResult? _result;
  String _message = '';
  double _progress = 0;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Expanded(child: _statusText(theme)),
            const SizedBox(width: 8),
            _actionButton(),
          ],
        ),
        if (_stage == _Stage.downloading) ...[
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(value: _progress <= 0 ? null : _progress),
          ),
        ],
        if (_stage == _Stage.failed && _message.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            _message,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.error,
            ),
          ),
        ],
      ],
    );
  }

  Widget _statusText(ThemeData theme) {
    final style = theme.textTheme.bodyMedium;
    switch (_stage) {
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
        return Text('正在下载… ${(_progress * 100).toStringAsFixed(0)}%', style: style);
      case _Stage.installing:
        return Text('已唤起系统安装器', style: style);
      case _Stage.failed:
        return Text('检查更新', style: style);
    }
  }

  Widget _actionButton() {
    switch (_stage) {
      case _Stage.checking:
      case _Stage.downloading:
        return const SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        );
      case _Stage.available:
        return FilledButton.tonal(
          onPressed: _download,
          child: const Text('下载并安装'),
        );
      case _Stage.installing:
        return TextButton(onPressed: _check, child: const Text('重新检查'));
      case _Stage.idle:
      case _Stage.latest:
      case _Stage.failed:
        return TextButton.icon(
          onPressed: _check,
          icon: const Icon(Icons.refresh_rounded, size: 18),
          label: Text(_stage == _Stage.failed ? '重试' : '检查更新'),
        );
    }
  }

  Future<void> _check() async {
    if (_stage == _Stage.checking || _stage == _Stage.downloading) return;
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

  Future<void> _download() async {
    final result = _result;
    if (result == null || _stage != _Stage.available) return;
    setState(() {
      _stage = _Stage.downloading;
      _progress = 0;
      _message = '';
    });
    try {
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
      final file = await Updater.download(
        release: result.release,
        asset: asset,
        mirrors: result.mirrors,
        onProgress: (received, total) {
          if (!mounted || total <= 0) return;
          final value = (received / total).clamp(0.0, 1.0).toDouble();
          // 每 1% 才重建一次：一个 30MB 的包会回调上千次，无条件 setState 会卡。
          if ((value - _progress).abs() < 0.01 && value < 1) return;
          setState(() => _progress = value);
        },
      );
      if (!mounted) return;
      if (!await Updater.canInstall()) {
        await Updater.openInstallSettings();
        if (!mounted) return;
        setState(() {
          _stage = _Stage.failed;
          _message = '请在系统设置里允许「安装未知应用」，然后回来再点一次「下载并安装」';
        });
        return;
      }
      final opened = await Updater.install(file.path);
      if (!mounted) return;
      setState(() {
        _stage = opened ? _Stage.installing : _Stage.failed;
        if (!opened) _message = '无法唤起安装器，请手动安装：${file.path}';
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
        _message = '下载失败，请稍后再试';
      });
    }
  }
}
