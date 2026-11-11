#ifndef RUNNER_FLUTTER_WINDOW_H_
#define RUNNER_FLUTTER_WINDOW_H_

#include <flutter/dart_project.h>
#include <flutter/encodable_value.h>
#include <flutter/flutter_view_controller.h>
#include <flutter/method_channel.h>

#include <memory>

#include "win32_window.h"

// A window that does nothing but host a Flutter view.
class FlutterWindow : public Win32Window {
 public:
  // Creates a new FlutterWindow hosting a Flutter view running |project|.
  explicit FlutterWindow(const flutter::DartProject& project);
  virtual ~FlutterWindow();

 protected:
  // Win32Window:
  bool OnCreate() override;
  void OnDestroy() override;
  LRESULT MessageHandler(HWND window, UINT const message, WPARAM const wparam,
                         LPARAM const lparam) noexcept override;

 private:
  // The project to run.
  flutter::DartProject project_;

  // The Flutter instance hosted by this window.
  std::unique_ptr<flutter::FlutterViewController> flutter_controller_;

  // 把 Win32 全局热键（WM_HOTKEY）转给 Dart 侧画中画控制器。
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>>
      pip_channel_;

  // 单独存一份 HWND：WM_DESTROY 时 Win32Window 会先把 window_handle_ 清空，
  // 之后 GetHandle() 拿不到窗口，注销热键就没法做了。
  HWND hotkey_window_ = nullptr;
};

#endif  // RUNNER_FLUTTER_WINDOW_H_
