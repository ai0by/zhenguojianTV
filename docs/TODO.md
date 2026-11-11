# 待办（TODO）

本文件记录**已经明确要做、但还没排期实现**的功能和遗留问题。每条都写清「为什么现在不做」「建议怎么做」「怎么算做完」，方便后续直接接手，不用重新调研。

---

## Windows 端画中画（PiP）

**状态**：已实现（2026-11-11，0.8.28；下列方案对比保留作决策记录）

### 实际落地（2026-11-11）

用户要求「真正的画中画、可随意拖动改大小、快捷键显示隐藏」，因此在方案 A 的基础上做了增强：

- `lib/windows_pip.dart`：`WindowsPip` 切窗口形态（480×270 起步、置顶、记住进入前的 bounds / 最大化状态），退出原样还原；`minimumSize` 从 980×620 降到 240×135，**保留 `WS_THICKFRAME` 所以四条边 / 四个角都能拖着改大小**，改过的尺寸下次沿用；`WindowsPipOverlay` 提供拖动条与置顶 / 退出按钮。
- `windows/runner/flutter_window.cpp`：Win32 `RegisterHotKey` 注册全局 `Ctrl+Shift+P`，`WM_HOTKEY` 经 `duanju/windows_pip` MethodChannel 回传 Dart。窗口隐藏 / 失焦时全局热键照样能收到；注册失败不阻断启动，播放页还有小窗按钮。
- 隐藏小窗时只 `hide()` 窗口，**不暂停播放**（声音继续）；再按一次叫回来。`Esc` 退出小窗并还原窗口。
- `lib/home_screen.dart`：PiP 时隐藏 AppBar / NavigationRail、强制切到竖滑播放页；退出自动回到原页面。
- 播放页小窗按钮（`feed-pip`）在 Windows 上改为调 `WindowsPip.enter()`，Android 行为不变。

**为什么仍是「主窗口形态切换」而不是独立 HWND**：Windows 没有系统级 PiP；方案 B/C 要新起 Flutter 引擎或改写 runner 的视频渲染，而 media_kit 的 Player 不能跨引擎共享，等于两个播放器、内存翻倍。浮窗形态在当前架构下是唯一不牺牲播放连续性的做法，用户可见行为（拖动、缩放、置顶、全局热键显隐、隐藏时继续播）已全部满足。若后续确实需要「小窗与主窗同时可用」，再评估方案 C。

### 背景

Android 侧早就有系统画中画：`MainActivity` 用 `PictureInPictureParams.Builder.setActions(List<RemoteAction>)` 挂播放/暂停按钮，配合 `PendingIntent.getBroadcast` + 动态 `BroadcastReceiver` 把点击回传到 Dart。**Windows 端完全没有**，用户在电脑上想边看边干别的，只能手动把窗口拖小、再手动切回来。

### 为什么不能照搬 Android

Windows 没有"系统级 PiP"这种东西 —— 没有 `enterPictureInPictureMode()` 那种由系统托管的小窗。能用的只有 Win32 窗口能力：置顶、无边框、改尺寸、跳过任务栏。所以 Windows 的"画中画"本质是**把主窗口切换成一个小尺寸、无边框、置顶的浮窗形态**。

### 候选方案

| 方案 | 做法 | 优点 | 代价 |
|---|---|---|---|
| **A. 主窗口缩成小窗**（推荐先做） | `window_manager` 的 `setAlwaysOnTop(true)` + `setSize(480×270)` + 记下原尺寸/位置 → 退出时还原；小窗时播放器页面铺满窗口，隐藏标题栏与其余 UI | 复用现有 `WindowsDesktopFrame`，纯 Dart 改动，不碰 C++ | 小窗期间主界面不可用（要退出小窗才能浏览剧库） |
| B. 第二个 Flutter 窗口 | 用多窗口插件新开一个引擎承载播放器 | 主窗与小窗并存 | 要跨引擎共享播放状态，`media_kit` 的 `Player` 实例不能跨引擎；内存翻倍 |
| C. 原生子窗口 + libmpv | 写 C++ 插件，把 `libmpv` 渲染到独立 HWND | 最接近真 PiP，主窗完全可用 | 要改 `windows/runner` 与 `native/bridge`，工作量大、易踩 DLL 生命周期坑 |

**建议先做 A**：80% 的使用场景（边看边回消息/查资料）用小窗置顶就够了，且不需要动原生代码。等确实有人抱怨"小窗时不能同时浏览剧库"，再评估 B/C。

### 建议实现要点

- 状态入口放播放页控件层（和 Android 的小窗按钮同一个位置、同一个图标），只在 `defaultTargetPlatform == windows` 显示；
- 小窗尺寸建议 480×270（16:9），最短边不低于 240，允许用户拖边缩放；
- 必须记住进入小窗前的窗口尺寸与位置，退出时原样还原（含是否最大化）；
- 置顶开关默认开，但要在小窗上加一个"取消置顶"的小按钮 —— 有时候用户只是想让窗口小一点；
- 小窗状态下 `Esc` 退出小窗（回到原窗口），而不是退出应用；
- 小窗期间不要让 `WindowsDesktopFrame` 的标题栏露出来（本来就无边框），但**要保留一个能拖动的区域**，否则窗口拖不动。

### 验收标准

1. 播放页点小窗 → 窗口变成 480×270、无边框、置顶，画面继续播、不重新缓冲；
2. 退出小窗 → 窗口尺寸/位置/最大化状态与进入前完全一致，播放进度不丢；
3. 小窗可拖动、可缩放、可关闭置顶；
4. 手机 / 电视 / 测试环境完全不受影响（`flutter test` 里不出现新分支）；
5. `Build Windows` 与两条回归 CI 全绿。

### 落点

- `lib/windows_chrome.dart` —— 窗口形态切换（小窗 / 普通）；
- 播放页控件层（与 Android 小窗按钮同一处）—— 入口按钮；
- 可能需要 `lib/local_store.dart` 记住小窗尺寸偏好（新增键必须同步 `local_snapshot.dart` 三处）。

---

## 其它已知遗留

- ~~**Windows 包启动验收 `--package-smoke` 卡在位置流 20s 超时**~~ —— **2026-11-11 已修好**。根因不是时序：`PlayerConfiguration(vo:'null')` + 纯视频无音轨 + headless runner ⇒ mpv 没有输出时钟，`time-pos` 恒 0，老判据「位置推进到 400ms」在该环境**不可能成立**。现改为 `duration` / `playing` / `seek` 三条不依赖时钟的硬断言，`Build Windows` 已真绿。详见 `docs/BUILD.md`。
- **红果播放取流的「每轮注册新身份」可以清理**：2026-11-11 实测固定同一身份连取 4 集 4/4 成功，证明 0.8.27 的「一个身份只能取流一次」是误判（真因是取流接口改认播放器专用签名，已修）。当前保留只为容错，代价是每集多一次 `device_register` 往返。清理时 `provider_hongguo_playback_routes_test.go` 里断言「每轮新身份 / 全失败再注册一次」的用例要一起改。
- **境外 runner 抓不到花果 / 网果 / 无果 / 发果**：恒 403，只能国内机器串行深抓后手工提交快照。详见 `memory/catalog.md`。
