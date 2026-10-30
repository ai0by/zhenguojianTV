package com.duanju.duanju_app

import io.flutter.embedding.android.FlutterActivity
import android.app.PictureInPictureParams
import android.app.UiModeManager
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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            window.isNavigationBarContrastEnforced = false
            window.isStatusBarContrastEnforced = false
        }
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
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                        // Android 12+ 用系统自动进小窗，比 onUserLeaveHint 稳：
                        // 分屏、手势返回桌面等场景也能正确触发。
                        applyPipParams(ratio, pipArmed)
                    }
                    result.success(true)
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
     * 进入画中画。返回是否真的切进去了。
     *
     * `setAspectRatio` 要求宽高比落在 1:2.39 ~ 2.39:1 之间，越界会抛
     * IllegalArgumentException；这里统一收敛一次，竖版短剧按 9:16 处理。
     */
    private fun enterPip(ratio: Double): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return false
        if (!pipAvailable() || isFinishing) return false
        return try {
            pipAspect = ratio.coerceIn(0.42, 2.39)
            val builder = PictureInPictureParams.Builder()
                .setAspectRatio(Rational((pipAspect * 1000).toInt(), 1000))
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                builder.setAutoEnterEnabled(true)
            }
            enterPictureInPictureMode(builder.build())
        } catch (e: Exception) {
            false
        }
    }

    private fun applyPipParams(ratio: Double, armed: Boolean) {
        if (!pipAvailable() || Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return
        try {
            val safe = ratio.coerceIn(0.42, 2.39)
            val builder = PictureInPictureParams.Builder()
                .setAspectRatio(Rational((safe * 1000).toInt(), 1000))
                .setAutoEnterEnabled(armed)
            setPictureInPictureParams(builder.build())
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
            val safe = pipAspect.coerceIn(0.42, 2.39)
            enterPictureInPictureMode(
                PictureInPictureParams.Builder()
                    .setAspectRatio(Rational((safe * 1000).toInt(), 1000))
                    .build()
            )
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


}
