#include "flutter_window.h"

#include <optional>

#include "flutter/generated_plugin_registrant.h"
#include <flutter/standard_method_codec.h>

namespace {

// 仓库没有显式定义 WINVER，老一点 Windows SDK 的 winuser.h 只在
// WINVER >= 0x0601 时声明 MOD_NOREPEAT；补一个兜底，避免出包时才编译失败。
#ifndef MOD_NOREPEAT
#define MOD_NOREPEAT 0x4000
#endif

// Ctrl+Shift+P：显示 / 隐藏画中画小窗。用全局热键而不是 Flutter 的键盘
// 快捷键，是因为小窗置顶时用户通常在别的程序里，Flutter 收不到按键。
constexpr int kPipHotkeyId = 0x5A47;
constexpr UINT kPipModifiers = MOD_CONTROL | MOD_SHIFT | MOD_NOREPEAT;
constexpr UINT kPipVirtualKey = 'P';

}  // namespace

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());
  SetChildContent(flutter_controller_->view()->GetNativeWindow());

  // 全局热键 → Dart：名字必须与 lib/windows_pip.dart 的 MethodChannel 一致。
  // 注册失败（快捷键被别的程序占用）不阻断启动，播放页里还有小窗按钮可用。
  pip_channel_ =
      std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
          flutter_controller_->engine()->messenger(), "duanju/windows_pip",
          &flutter::StandardMethodCodec::GetInstance());
  hotkey_window_ = GetHandle();
  RegisterHotKey(hotkey_window_, kPipHotkeyId, kPipModifiers, kPipVirtualKey);

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    this->Show();
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::OnDestroy() {
  if (hotkey_window_) {
    UnregisterHotKey(hotkey_window_, kPipHotkeyId);
    hotkey_window_ = nullptr;
  }
  pip_channel_.reset();
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;

    case WM_HOTKEY:
      // 全局热键即使在窗口隐藏 / 失焦时也会送到这里。交给 Dart 决定
      // 「首次按下进入小窗」还是「显示 / 隐藏」，C++ 侧不持有窗口形态状态。
      if (wparam == kPipHotkeyId && pip_channel_) {
        pip_channel_->InvokeMethod(
            "togglePip", std::make_unique<flutter::EncodableValue>());
      }
      return 0;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
