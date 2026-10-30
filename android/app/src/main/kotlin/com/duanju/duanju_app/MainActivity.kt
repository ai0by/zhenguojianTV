package com.duanju.duanju_app

import io.flutter.embedding.android.FlutterActivity
import android.annotation.TargetApi
import android.app.PendingIntent
import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.app.UiModeManager
import android.content.BroadcastReceiver
import android.content.Intent
import android.content.IntentFilter
import android.graphics.drawable.Icon
import android.media.AudioManager
import android.os.Build
import android.provider.Settings
import android.util.Rational
import android.view.WindowManager
import android.os.Bundle
import android.content.Context
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import androidx.core.content.FileProvider
import java.io.File

class MainActivity : FlutterActivity() {
    /**
     * 画中画（0.8.17）。
     *
     * `pipArmed` 由 Dart 侧在「播放中且停在播放页」时置 true：
     * 只有这时按 Home 才自动进小窗，其他页面退出应用不该弹小窗。
     * `pipAspect` 是视频宽高比（宽 / 高），竖版短剧约 0.5625。
     */
    private var pipArmed = false
    private var pipAspect = 9.0 / 16.0
    private var pipChannel: MethodChannel? = null

    /**
     * 小窗里当前是否在播放（0.8.18）。
     *
     * 小窗上的动作图标要跟真实播放态一致：播放中显示"暂停"、暂停时显示"播放"。
     * Dart 侧在播放态变化时调 `updatePictureInPicturePlayback` 同步过来，
     * 原生这边只是照它渲染图标，不自己猜。
     */
    private var pipPlaying = true
    private var pipReceiverRegistered = false

    /** 小窗动作点击后回传 Dart（0.8.18）：真正的播放/暂停由播放器决定。 */
    private val pipReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action == ACTION_PIP_TOGGLE) {
                pipChannel?.invokeMethod("pictureInPictureTogglePlayback", null)
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            window.isNavigationBarContrastEnforced = false
            window.isStatusBarContrastEnforced = false
        }
        registerPipReceiver()
    }

    override fun onDestroy() {
        // 动态注册的接收器必须成对注销，否则 Activity 销毁后还挂着会泄漏。
        if (pipReceiverRegistered) {
            pipReceiverRegistered = false
            try {
                unregisterReceiver(pipReceiver)
            } catch (e: Exception) {
                // 已被系统注销，忽略。
            }
        }
        super.onDestroy()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val channel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "duanju/device")
        pipChannel = channel
        channel
            .setMethodCallHandler { call, result ->
                if (call.method == "deviceInfo") {
                    val mode = getSystemService(Context.UI_MODE_SERVICE) as UiModeManager
                    val television = mode.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION ||
                        packageManager.hasSystemFeature(PackageManager.FEATURE_LEANBACK)
                    val version = packageManager.getPackageInfo(packageName, 0).versionName
                    result.success(mapOf("television" to television, "version" to version))
                } else if (call.method == "systemProxy") {
                    val connection = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
                    val proxy = connection.defaultProxy
                    val host = proxy?.host.orEmpty()
                    val address = if (host.isNotEmpty() && (proxy?.port ?: 0) > 0) {
                        "http://${if (host.contains(':')) "[$host]" else host}:${proxy!!.port}"
                    } else ""
                    result.success(mapOf("http" to address, "https" to address,
                        "bypass" to (proxy?.exclusionList?.toList() ?: emptyList<String>()),
                        "pac" to (proxy != null && proxy.pacFileUrl != Uri.EMPTY)))
                } else if (call.method == "networkType") {
                    result.success(networkType())
                } else if (call.method == "systemVolume") {
                    result.success(systemVolume())
                } else if (call.method == "setSystemVolume") {
                    val index = call.argument<Int>("index") ?: -1
                    result.success(setSystemVolume(index))
                } else if (call.method == "screenBrightness") {
                    result.success(screenBrightness())
                } else if (call.method == "setScreenBrightness") {
                    val value = call.argument<Double>("value") ?: -1.0
                    result.success(setScreenBrightness(value))
                } else if (call.method == "resetScreenBrightness") {
                    result.success(resetScreenBrightness())
                } else if (call.method == "pictureInPictureAvailable") {
                    result.success(pipAvailable())
                } else if (call.method == "enterPictureInPicture") {
                    val ratio = call.argument<Double>("aspectRatio") ?: pipAspect
                    result.success(enterPip(ratio))
                } else if (call.method == "setPictureInPictureArmed") {
                    pipArmed = call.argument<Boolean>("armed") ?: false
                    val ratio = call.argument<Double>("aspectRatio") ?: pipAspect
                    pipAspect = ratio
                    // 0.8.18：不再只在 S+ 生效 —— 小窗上的动作（播放/暂停、
                    // 返回应用）从 Android 8.0 起就要带上，否则进小窗只剩一个
                    // 光秃秃的画面，暂停不了。
                    applyPipParams(ratio, pipArmed)
                    result.success(true)
                } else if (call.method == "updatePictureInPicturePlayback") {
                    pipPlaying = call.argument<Boolean>("playing") ?: true
                    val ratio = call.argument<Double>("aspectRatio") ?: pipAspect
                    pipAspect = ratio
                    applyPipParams(ratio, pipArmed)
                    result.success(true)
                } else if (call.method == "updateEnvironment") {
                    result.success(updateEnvironment())
                } else if (call.method == "canInstallApk") {
                    result.success(canInstallApk())
                } else if (call.method == "openInstallSettings") {
                    result.success(openInstallSettings())
                } else if (call.method == "installApk") {
                    val path = call.argument<String>("path") ?: ""
                    result.success(installApk(path))
                } else {
                    result.notImplemented()
                }
            }

    }

    /** 设备是否支持画中画（Android 8.0+ 且系统声明了该特性）。 */
    private fun pipAvailable(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return false
        return packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)
    }

    /**
     * 注册小窗动作的接收器（0.8.18）。
     *
     * 动作点击走的是 `PendingIntent.getBroadcast`，所以必须有个接收器接住；
     * 用动态注册而不是清单静态注册 —— 这个接收器只服务于正在跑的 Activity，
     * 静态注册反而会让应用在后台被外部广播唤起（还得多写 exported=false）。
     * Android 13+ 必须显式声明「不导出」，否则注册会抛异常。
     */
    private fun registerPipReceiver() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        if (pipReceiverRegistered) return
        val filter = IntentFilter(ACTION_PIP_TOGGLE)
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                registerReceiver(pipReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
            } else {
                registerReceiver(pipReceiver, filter)
            }
            pipReceiverRegistered = true
        } catch (e: Exception) {
            // 注册失败只是小窗里点不动暂停，不影响其他功能。
        }
    }

    /**
     * 小窗上的动作按钮（0.8.18）。
     *
     * 两个动作：
     * 1. 播放 / 暂停 —— 图标按 `pipPlaying` 切换，点击只回传 Dart，由播放器
     *    真正执行（原生不碰播放器状态，避免两边状态打架）；
     * 2. 返回应用 —— 小窗本身就支持"点一下展开"，但动作菜单里给一个显式入口
     *    更直观（部分 ROM 的展开按钮做得不明显）。
     */
    @TargetApi(Build.VERSION_CODES.O)
    private fun pipActions(): List<RemoteAction> {
        val actions = ArrayList<RemoteAction>(2)
        val toggleLabel = if (pipPlaying) "暂停" else "播放"
        val toggleIntent = PendingIntent.getBroadcast(
            this,
            REQUEST_PIP_TOGGLE,
            Intent(ACTION_PIP_TOGGLE).setPackage(packageName),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val toggleIcon = Icon.createWithResource(
            this,
            if (pipPlaying) android.R.drawable.ic_media_pause else android.R.drawable.ic_media_play,
        )
        actions.add(RemoteAction(toggleIcon, toggleLabel, toggleLabel, toggleIntent))

        val openIntent = PendingIntent.getActivity(
            this,
            REQUEST_PIP_OPEN,
            Intent(this, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val openIcon = Icon.createWithResource(this, android.R.drawable.ic_menu_view)
        actions.add(RemoteAction(openIcon, "返回应用", "返回应用", openIntent))
        return actions
    }

    /**
     * 组装一次完整的画中画参数。
     *
     * `setAspectRatio` 要求宽高比落在 1:2.39 ~ 2.39:1 之间，越界会抛
     * IllegalArgumentException；这里统一收敛一次，竖版短剧按 9:16 处理。
     */
    @TargetApi(Build.VERSION_CODES.O)
    private fun pipParams(ratio: Double, armed: Boolean): PictureInPictureParams {
        val safe = ratio.coerceIn(0.42, 2.39)
        val builder = PictureInPictureParams.Builder()
            .setAspectRatio(Rational((safe * 1000).toInt(), 1000))
            .setActions(pipActions())
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            // Android 12+ 用系统自动进小窗，比 onUserLeaveHint 稳：
            // 分屏、手势返回桌面等场景也能正确触发。
            builder.setAutoEnterEnabled(armed)
        }
        return builder.build()
    }

    /**
     * 进入画中画。返回是否真的切进去了。
     */
    private fun enterPip(ratio: Double): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return false
        if (!pipAvailable() || isFinishing) return false
        return try {
            pipAspect = ratio.coerceIn(0.42, 2.39)
            enterPictureInPictureMode(pipParams(pipAspect, true))
        } catch (e: Exception) {
            false
        }
    }

    /** 刷新已生效的画中画参数（播放态变了、进出小窗都要调一次）。 */
    private fun applyPipParams(ratio: Double, armed: Boolean) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        if (!pipAvailable()) return
        try {
            val safe = ratio.coerceIn(0.42, 2.39)
            setPictureInPictureParams(pipParams(safe, armed))
        } catch (e: Exception) {
            // 个别 ROM 不支持自动进入，忽略即可：手动按钮仍然可用。
        }
    }

    /**
     * Android 8.0~11 的自动进小窗入口：用户按 Home / 切到别的应用时触发。
     *
     * 只有 Dart 侧「已武装」（正在播放且停在播放页）才进小窗，
     * 否则从首页退出应用也弹个小窗出来，反而打扰。
     */
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        if (!pipArmed || !pipAvailable() || isFinishing) return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) return
        try {
            enterPictureInPictureMode(pipParams(pipAspect, true))
        } catch (e: Exception) {
            // 忽略：不支持就保持普通后台。
        }
    }

    /** 小窗进出状态回传给 Dart，用来收起控件、停掉自动隐藏计时。 */
    override fun onPictureInPictureModeChanged(
        isInPictureInPictureMode: Boolean,
        newConfig: Configuration,
    ) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        }
        pipChannel?.invokeMethod("pictureInPictureChanged", isInPictureInPictureMode)
    }

    /**
     * 系统媒体音量（0.7.5）。
     *
     * 播放器手势要调的是**系统媒体音量**，不是应用内音量；
     * 返回 {current, max}，由 Dart 侧按比例换算百分比。
     */
    private fun systemVolume(): Map<String, Int> {
        return try {
            val audio = getSystemService(Context.AUDIO_SERVICE) as AudioManager
            val max = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1)
            val current = audio.getStreamVolume(AudioManager.STREAM_MUSIC)
            mapOf("current" to current, "max" to max)
        } catch (e: Exception) {
            mapOf("current" to 0, "max" to 1)
        }
    }

    /** 设置系统媒体音量档位（0.7.5）。越界自动收敛，不弹系统音量条。 */
    private fun setSystemVolume(index: Int): Boolean {
        return try {
            val audio = getSystemService(Context.AUDIO_SERVICE) as AudioManager
            val max = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC)
            val target = index.coerceIn(0, max)
            audio.setStreamVolume(AudioManager.STREAM_MUSIC, target, 0)
            true
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 读取系统当前亮度比例（0~1，0.7.8）。
     *
     * 只读系统设置，**不需要任何权限**；用作手势的起点。
     */
    private fun screenBrightness(): Double {
        return try {
            val value = Settings.System.getInt(
                contentResolver,
                Settings.System.SCREEN_BRIGHTNESS,
            )
            (value / 255.0).coerceIn(0.0, 1.0)
        } catch (e: Exception) {
            0.5
        }
    }

    /**
     * 设置**本 App 窗口**亮度（0~1，0.7.8）。
     *
     * 用窗口亮度覆盖系统亮度：系统调得再暗，App 内也能拉亮，
     * 且不需要「修改系统设置」特殊权限；退出时会恢复跟随系统。
     */
    private fun setScreenBrightness(ratio: Double): Boolean {
        return try {
            val value = ratio.coerceIn(0.0, 1.0).toFloat()
            runOnUiThread {
                val attrs = window.attributes
                attrs.screenBrightness = value
                window.attributes = attrs
            }
            true
        } catch (e: Exception) {
            false
        }
    }

    /** 恢复窗口亮度为"跟随系统"（0.7.8）。 */
    private fun resetScreenBrightness(): Boolean {
        return try {
            runOnUiThread {
                val attrs = window.attributes
                attrs.screenBrightness =
                    WindowManager.LayoutParams.BRIGHTNESS_OVERRIDE_NONE
                window.attributes = attrs
            }
            true
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 当前网络的粗略类型（0.7.3）。
     *
     * 只用于"移动网络下是否自动播放"：Wi-Fi/有线返回 wifi，蜂窝返回 mobile，
     * 无法判断（含 VPN、无网络）返回 unknown，由 Dart 侧按"允许自动播放"处理。
     */
    private fun networkType(): String {
        return try {
            val manager = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            val network = manager.activeNetwork ?: return "none"
            val caps = manager.getNetworkCapabilities(network) ?: return "none"
            when {
                caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "mobile"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
                else -> "unknown"
            }
        } catch (e: Exception) {
            "unknown"
        }
    }

    /**
     * 远程升级用到的设备信息（0.8.18）：versionCode + 首选 ABI。
     *
     * versionCode 决定**能不能推通用包**：通用包带 +4000 偏移（4096 vs 2096），
     * 已经装了通用包的设备如果推一个单 ABI 包过去，系统会判成版本降级拒装。
     */
    private fun updateEnvironment(): Map<String, Any> {
        val info = packageManager.getPackageInfo(packageName, 0)
        val code = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.longVersionCode
        } else {
            @Suppress("DEPRECATION")
            info.versionCode.toLong()
        }
        val abi = Build.SUPPORTED_ABIS.firstOrNull() ?: ""
        return mapOf("versionCode" to code, "abi" to abi)
    }

    /** 是否已允许「安装未知应用」（Android 8.0 以下无需授权）。 */
    private fun canInstallApk(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return true
        return try {
            packageManager.canRequestPackageInstalls()
        } catch (e: Exception) {
            false
        }
    }

    /** 跳到本应用的「安装未知应用」授权页。 */
    private fun openInstallSettings(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return false
        return try {
            startActivity(
                Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES)
                    .setData(Uri.parse("package:$packageName"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            )
            true
        } catch (e: Exception) {
            false
        }
    }

    /**
     * 用系统安装器打开下载好的 APK（0.8.18）。
     *
     * 必须走 FileProvider 的 `content://`：Android 7.0 起直接传 `file://`
     * 会抛 `FileUriExposedException`。授权用一次性读权限，不给整个目录。
     */
    private fun installApk(path: String): Boolean {
        if (path.isEmpty()) return false
        val file = File(path)
        if (!file.exists()) return false
        return try {
            val uri = FileProvider.getUriForFile(this, "$packageName.fileprovider", file)
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            startActivity(intent)
            true
        } catch (e: Exception) {
            false
        }
    }

    private companion object {
        /** 小窗「播放/暂停」动作的广播 action（0.8.18）。 */
        const val ACTION_PIP_TOGGLE = "com.duanju.duanju_app.PIP_TOGGLE"

        /** 两个 PendingIntent 的 requestCode，互不相同即可。 */
        const val REQUEST_PIP_TOGGLE = 1001
        const val REQUEST_PIP_OPEN = 1002
    }
}
