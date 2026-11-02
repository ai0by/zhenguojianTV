# 开发与构建

产品介绍见 [README](../README.md)，版本变化见 [更新日志](../CHANGELOG.md)。

## 环境

Flutter `3.47.4`、Dart `3.12+`、Go `1.24.1+`、Python `3.10+`；Android 需 JDK 17、SDK 36、NDK `28.2.13676358`。

## 本地构建

```sh
# 全站源版（真果鉴TV）
python3 scripts/build_android.py --abi arm64-v8a --all-sources
# 仅红果版（红果鉴）
python3 scripts/build_android.py --abi arm64-v8a
# 电视版（arm64-v8a + armeabi-v7a 分包，并额外产出 universal/all APK）
python3 scripts/build_android.py --tv --universal --all-sources
```

- **必须在纯英文路径下构建**（中文路径会导致工具链失败）。
- 构建时不要设 HTTP 代理；访问 GitHub 时才需要代理。
- 产物在 `dist/android/`，`SHA256SUMS.txt` 为校验清单。

## 走 Actions 构建

**Actions → Build APK → Run workflow**：

- `all_sources` 决定真果鉴TV / 红果鉴；
- `tv` 产出 arm64-v8a + armeabi-v7a 分包；
- `universal` 额外产出合并 APK（只含 ARM 两套 ABI）；
- 跑完在本次运行的 Artifacts 里下载 `apk-<run_number>.zip`。

工作流固定 Flutter `3.47.4`、Go `1.24.1`、NDK `28.2.13676358`，与本机脚本一致。

### 出包校验

CI 会用官方工具逐个验，任何一项不过直接失败：

- `apksigner verify` — 签名（v2 必须为 true）；
- `zipalign -c 4` — 对齐；
- `aapt2 dump badging` — minSdk、versionCode 与 ABI。

打包脚本本身还有两道硬检查：通用包**只允许** ARM 两套 ABI（依赖 AAR 会带半套 `lib/x86_64/`，安装器按它校验会报「解析软件包失败」），并且通用包 versionCode 抬高 4000，避免低于 `--split-per-abi` 的 1085/2085 被判版本降级。

### 签名

签名密钥从仓库 Secret `CI_KEYSTORE_BASE64` 还原（base64 的 keystore），**缺失即构建失败**。`Verify APK` 之后还有一道 `Assert signing certificate` 硬断言：实际证书指纹必须等于预期值，不符当场终止。

历史上密钥存在 `actions/cache` 里，缓存一被清理就现造一把新的，导致 0.8.5 / 0.8.7 / 0.8.9 / 0.8.10~0.8.18 是**四把不同的证书**，跨这几版覆盖安装必然报 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`。别退回 keytool 现场生成 —— 换一次签名就等于让所有老用户卸载重装。

未配置 `android/key.properties` 时，Gradle 按 `release` → `ciDebug` → `debug` 的顺序回落。

## 剧集快照（`catalog` 分支）

`Build Catalog Snapshot` 工作流每 2 小时（`15 */2 * * *`）跑一轮，把各站源的剧集目录导出、加密、提交到 `catalog` 分支：

```
index.json                    明文索引：version / generatedAt / digest / sources{file, sha256, count}
sources/<站源>.json.enc       [12 字节 nonce][AES-256-GCM 密文]
```

- 密钥 = `sha256("zhenguojianTV/catalog-snapshot/v1")`，与 `native/core/app_catalog_snapshot.go` 的 `catalogSnapshotSecret`、`scripts/build_catalog_snapshot.py` 的 `SECRET` 三处必须一致。**密钥随 APK 分发，这是混淆不是安全边界。**
- 客户端走 jsDelivr 分支引用（边缘缓存 12 小时），兜底 `gh-proxy.org` 代理 raw。`GeneratedAt` 没变就一个文件都不下载。
- **内容不变就不发新版**：nonce 由明文派生（`sha256(明文)[:12]`），密文里不写时间戳，`index.json` 用 `digest` 判断内容是否真的变了 —— 否则每 2 小时都会产出一版「新快照」，客户端跟着重下几 MB。
- 客户端只下载**用户启用的站源**（整份快照 12 源约 8MB，只开红果只需 1.9MB）；后来又开了别的源，下一轮会补下缺的那些。
- **客户端只在手动触发时同步**（0.8.20）：入口是「设置 → 站源管理 → 同步远程数据源」，首页 `initState` 与 `CatalogAutoUpdater.runNow()` 都不再自动拉快照。页面上那行「远端站源数据：…」来自 `catalogSnapshotInfo` 这个只读 action（只拉 `index.json`，不下载任何站源文件）。
- 加密/索引由 `scripts/build_catalog_snapshot.py` 完成，导出由 `native/cmd/catalogdump` 完成；单测在 `scripts/test_build_catalog_snapshot.py` 与 `native/core/app_catalog_snapshot_test.go`。
- 分支用 `git push --force` 只保留一个提交 —— 每 2 小时一版，留历史会让仓库无限膨胀；jsDelivr 按分支名取最新，不受影响。
- 抓不到的站源会沿用上一轮的文件（`previous_entries`），不会因为 runner 一时连不上就把某个站源从快照里抹掉。
- **花果 / 网果 / 发果在境外 runner 上是 HTTP 403**（Cloudflare / 站点自己的 WAF 按 IP 拦，换 UA 无效），CI 抓不到它们；App 在国内网络下照常现场抓取。
  - 花果、网果已用国内机器跑 `catalogdump` 导出后手工提交到 `catalog` 分支（2151 / 1440 条），之后的 CI 轮次靠 `previous_entries` 一直沿用这份，不会因为抓不到就被抹掉。
  - 发果从国内也拿不到（Cloudflare 直接 403），`index.json` 里没有它。
  - `dumpSource` 有一条硬规矩：**翻页中途出错就算整站失败**，不能把「第 1 页成功、第 2 页 403」的几十条当成完整目录去覆盖上一轮（花果真实目录 2000+，早期就踩过 36 条顶掉全量的坑）。

## 源码结构

| 目录 | 内容 |
| --- | --- |
| `lib/` | 页面、播放器、本地用户、FFI、下载与媒体处理 |
| `native/core/`、`native/bridge/` | Go 站源核心、缓存、下载与 C ABI |
| `native/cmd/` | 辅助工具（`catalogdump` 导出剧集快照） |
| `android/`、`windows/`、`ios/` | 平台工程与必要资源 |
| `scripts/`、`.github/workflows/` | 构建、签名、验证、版本快照与剧集快照 |
| `test/`、`integration_test/` | 自动化与设备回归 |

仓库只提交业务源码，不包含构建产物、依赖缓存、本地机器配置、签名文件与任何密钥。

## 第三方组件

播放器使用 [media_kit](https://github.com/media-kit/media-kit) / libmpv，合并与导出使用 [FFmpegKit min-gpl](https://github.com/sk3llo/ffmpeg_kit_flutter)（含 FFmpeg、x264 / x265 等 GPL 组件，适用上游许可证）。

## 更新日志的写法

`Build APK` 的 release 步骤会从 `CHANGELOG.md` 里抓出**当前版本**的所有 `### <版本> · <主题>` 小节，生成 Release 说明。新增版本时请沿用这个标题格式，否则 Release 只会有一句兜底文案。
