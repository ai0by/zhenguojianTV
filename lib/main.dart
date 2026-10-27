import 'dart:io';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'core_bridge.dart';
import 'app_layout.dart';
import 'app_splash.dart';
import 'app_theme.dart';
import 'back_guard.dart';
import 'home_screen.dart';
import 'local_store.dart';
import 'playback_cache.dart';
import 'profiles_screen.dart';
import 'media_library.dart';
import 'package_smoke.dart';
import 'lan_controller.dart';
import 'vertical_feed_screen.dart';

Future<void> main(List<String> arguments) async {
  WidgetsFlutterBinding.ensureInitialized();
  if (Platform.isAndroid) {
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    SystemChrome.setSystemUIOverlayStyle(AppTheme.systemBars(Brightness.dark));
  }
  if (Platform.isWindows) {
    await windowManager.ensureInitialized();
  }
  MediaKit.ensureInitialized();
  if (Platform.isWindows && arguments.firstOrNull == '--package-smoke') {
    await runPackageSmoke(arguments);
    return;
  }
  // 0.8.11：开屏图先解进图片缓存，再起界面。
  //
  // 不预加载的话，Flutter 首帧画出来时 `Image.asset` 还没解码完，用户看到的是
  // "一层纯色 → 整张图突然出现"的硬切，就是反馈里的"开屏闪了一下"。
  // 这段等待期间原生启动窗口显示的是同色底（`splash_background`），
  // 所以观感上只是开屏图稍微晚一点点出现，不会跳。
  final device = await AppDevice.detect();
  await precacheSplashImage(device.television);
  runApp(const AppBootstrap());
}

class AppBootstrap extends StatefulWidget {
  const AppBootstrap({super.key});
  @override
  State<AppBootstrap> createState() => _AppBootstrapState();
}

class _AppBootstrapState extends State<AppBootstrap>
    with WidgetsBindingObserver {
  final repository = NativeRepository();
  final navigator = GlobalKey<NavigatorState>();

  /// 首页是否已拿到第一批可播放视频（控制开屏页淡出）。
  final _feedReady = ValueNotifier<bool>(false);

  LocalStore? store;
  Object? error;
  AppDevice device = const AppDevice();

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _feedReady.dispose();
    LanController.current?.dispose();
    LanController.current = null;
    MediaLibrary.current?.dispose();
    MediaLibrary.current = null;
    store?.dispose();
    super.dispose();
  }

  @override
  void initState() {
    super.initState();
    // 新建实例从干净的窗口开始，避免上一次的 180ms 去重窗口吞掉首次返回。
    entryBackGuard.reset();
    WidgetsBinding.instance.addObserver(this);
    _initialize();
  }

  /// 系统返回（`popRoute`）。与遥控器返回键的键盘事件去重：
  /// 部分盒子一次按键会同时发出两条，不去重就会"按一次退两级"。
  @override
  Future<bool> didPopRoute() async => !entryBackGuard.claim();

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!Platform.isIOS) return;
    final library = MediaLibrary.current;
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      if (library != null) {
        library.suspended = true;
        unawaited(library.cancel());
      }
      unawaited(
        NativeRepository(
          background: true,
        ).controlDownloads('pauseAll').catchError((Object _) {}),
      );
    } else if (state == AppLifecycleState.resumed) {
      if (library != null) library.suspended = false;
    }
  }

  Future<void> _initialize() async {
    setState(() {
      error = null;
    });
    try {
      final preferences = await SharedPreferences.getInstance();
      device = await AppDevice.detect();
      await repository.initialize();
      if (mounted) {
        setState(() {
          store = LocalStore(preferences);
          repository.access = store;
          MediaLibrary.attach(repository, store!);
          // 0.8.12：把用户设置的预缓存上限推给原生侧。
          // 原生不持久化这个值（设置是 Dart 侧的事），启动时对齐一次即可。
          unawaited(
            repository
                .setMediaCacheLimit(store!.playbackCachePreferences.limitMB << 20)
                .catchError((Object _) => const PlaybackCacheUsage()),
          );
          LanController.current?.dispose();
          final link = LanController(
            repository,
            store!,
            kind: device.television
                ? 'tv'
                : Platform.isWindows
                ? 'computer'
                : 'phone',
          );
          LanController.current = link;
          link.openPlayback = (request) async {
            final epoch = request.profileEpoch;
            if (request.cancelled ||
                !mounted ||
                store!.locked ||
                !link.receiving ||
                store!.profileEpoch != epoch ||
                !store!.allowsSource(request.detail.drama.source)) {
              throw StateError('当前用户不能接收播放');
            }
            await link.playbackHost?.stop();
            if (request.cancelled ||
                !mounted ||
                store!.locked ||
                store!.profileEpoch != epoch ||
                !link.receiving) {
              throw StateError('播放接收已取消');
            }
            final navigation = navigator.currentState;
            if (navigation == null) throw StateError('接收设备界面尚未就绪');
            unawaited(
              navigation.pushAndRemoveUntil<void>(
                MaterialPageRoute<void>(
                  // 0.6.4：局域网推送接收播放改用**新播放器**（竖滑播放器）。
                  //
                  // 迁移原因：老播放器 `player_screen.dart` 即将删除（XF-0003），
                  // 它最后的独有能力就是这条接收链路。
                  // 用 `singleDrama` 走"单剧模式"：上滑下一集、下滑上一集，
                  // 到边界只提示，与老播放器"这是一部剧"的语义一致；
                  // `initialEpisodeIndex` / `initialPosition` 承接发送端的续播点。
                  builder: (_) => VerticalFeedScreen(
                    repository: repository,
                    store: store!,
                    singleDrama: request.detail.drama,
                    initialEpisodeIndex: request.index,
                    initialPosition: request.position,
                    handoff: request,
                  ),
                ),
                (route) => route.isFirst,
              ),
            );
          };
        });
      }
    } catch (failure) {
      if (mounted) {
        setState(() {
          error = failure;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) => DuanjuApp(
    repository: repository,
    store: store,
    bootstrapError: error?.toString(),
    onRetry: _initialize,
    television: device.television,
    version: device.version,
    navigatorKey: navigator,
    feedReady: _feedReady,
  );
}

class DuanjuApp extends StatelessWidget {
  const DuanjuApp({
    super.key,
    required this.repository,
    this.store,
    this.bootstrapError,
    this.onRetry,
    this.television = false,
    this.version = appVersion,
    this.navigatorKey,
    this.feedPlayerFactory,
    this.feedReady,
    this.initialTab,
  });
  final AppRepository repository;
  final LocalStore? store;
  final String? bootstrapError;
  final VoidCallback? onRetry;
  final bool television;
  final String version;
  final GlobalKey<NavigatorState>? navigatorKey;

  /// 测试用：注入首页假播放器。
  final Player Function()? feedPlayerFactory;

  /// 测试/深链用：直接打开指定主标签；正常启动为空，仍从首页进入。
  final int? initialTab;

  /// 首页是否已就绪（控制开屏页）。为空时不开开屏页（如测试）。
  final ValueNotifier<bool>? feedReady;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: Listenable.merge([store]),
    builder: (_, _) => _application(),
  );

  Widget _application() => MaterialApp(
    navigatorKey: navigatorKey,
    title: appName,
    debugShowCheckedModeBanner: false,
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN')],
    localizationsDelegates: GlobalMaterialLocalizations.delegates,
    theme: AppTheme.light,
    darkTheme: AppTheme.dark,
    themeMode: AppTheme.mode(store?.themeMode ?? 'system'),
    builder: (context, child) {
      final mode = store?.displayMode ?? 'auto';
      final tv = mode == 'television' || mode == 'auto' && television;
      final theme = Theme.of(context);
      return AnnotatedRegion<SystemUiOverlayStyle>(
        value: AppTheme.systemBars(theme.brightness),
        child: ColoredBox(
          color: theme.scaffoldBackgroundColor,
          child: AppLayout(
            television: tv,
            version: version,
            child: Theme(
              data: tv ? televisionTheme(theme) : theme,
              child: Shortcuts(
                shortcuts: const {
                  SingleActivator(
                    LogicalKeyboardKey.select,
                    includeRepeats: false,
                  ): ActivateIntent(),
                  SingleActivator(
                    LogicalKeyboardKey.gameButtonA,
                    includeRepeats: false,
                  ): ActivateIntent(),
                },
                child: CallbackShortcuts(
                  bindings: {
                    const SingleActivator(LogicalKeyboardKey.goBack): () {
                      if (!entryBackGuard.claim()) return;
                      navigatorKey?.currentState?.maybePop();
                    },
                  },
                  child: FocusTraversalGroup(child: child!),
                ),
              ),
            ),
          ),
        ),
      );
    },
    home: store != null
        ? store!.locked
              ? ProfilesScreen(store: store!, locked: true)
              : _withSplash(
                  HomeScreen(
                    key: ValueKey(
                      'profile-${store!.profile.id}-${store!.profileEpoch}',
                    ),
                    repository: repository,
                    store: store!,
                    initialTab: initialTab,
                    feedPlayerFactory: feedPlayerFactory,
                    onFeedReady: feedReady == null
                        ? null
                        : () => feedReady!.value = true,
                  ),
                )
        : Scaffold(
            body: Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.play_circle_fill_rounded,
                      color: Color(0xFFFF765F),
                      size: 72,
                    ),
                    const SizedBox(height: 24),
                    Text(
                      bootstrapError ?? '正在打开$appName',
                      textAlign: TextAlign.center,
                      style: const TextStyle(fontSize: 18),
                    ),
                    const SizedBox(height: 24),
                    if (bootstrapError == null)
                      const CircularProgressIndicator()
                    else
                      FilledButton(
                        onPressed: onRetry,
                        child: const Text('重新打开'),
                      ),
                  ],
                ),
              ),
            ),
          ),
  );

  /// 用开屏页盖住主界面，等首页有视频可播后再淡出。
  ///
  /// 测试或未注入就绪信号时直接返回主界面（不显示开屏页）。
  Widget _withSplash(Widget child) {
    final ready = feedReady;
    if (ready == null) return child;
    return AppSplash(ready: ready, child: child);
  }
}
