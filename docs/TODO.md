# 待办（TODO）

本文件记录**已经明确要做、但还没排期实现**的功能和遗留问题。每条都写清「为什么现在不做」「建议怎么做」「怎么算做完」，方便后续直接接手，不用重新调研。

---

## Windows 端画中画（PiP）

**状态**：未开始（2026-11-10 立项，用户要求先记待办）

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

- **Windows 包启动验收 `--package-smoke` 卡在位置流 20s 超时**：前 3 项（Native Core / ffprobe / remux）通过，用 ctypes 直调包内 `libmpv-2.dll` 播同一文件正常推进，判定为老验收脚本 `vo:'null'` + `open()` 前订阅位置流的写法问题，不是包的问题。详见 `docs/BUILD.md`。
- **境外 runner 抓不到花果 / 网果 / 无果 / 发果**：恒 403，只能国内机器串行深抓后手工提交快照。详见 `memory/catalog.md`。
