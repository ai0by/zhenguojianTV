package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// 设备注册的 header 必须带齐服务端认的字段，缺了会返回 device_id: 0。
func TestHongguoDeviceHeaderHasRequiredFields(t *testing.T) {
	header := hongguoDeviceHeader()
	required := []string{"aid", "package", "app_version", "version_code", "channel", "os", "device_platform", "cdid", "openudid", "clientudid", "guest_mode"}
	for _, key := range required {
		if _, ok := header[key]; !ok {
			t.Fatalf("header 缺少字段 %q", key)
		}
	}
	if header["package"] != "com.phoenix.read" {
		t.Fatalf("package 不对：%v", header["package"])
	}
	if header["aid"] != 8662 {
		t.Fatalf("aid 不对：%v", header["aid"])
	}
	if header["version_code"] != 73532 {
		t.Fatalf("version_code 应与 hongguoAppVersionCode 一致，实际 %v", header["version_code"])
	}
	// 随机字段每次都要不同，否则多台设备会被服务端当成同一台。
	if hongguoDeviceHeader()["cdid"] == header["cdid"] {
		t.Fatal("cdid 每次应重新生成")
	}
}

// 注册成功要解析出 device_id_str / install_id_str。
func TestHongguoRegisterDeviceParsesResponse(t *testing.T) {
	body := `{"server_time":1793080048,"device_id":3295708855697689,"install_id":3295708855701785,"new_user":1,` +
		`"device_id_str":"3295708855697689","install_id_str":"3295708855701785","device_token":"AAA"}`
	seenPath := ""
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		seenPath = request.URL.Path
		if request.URL.Query().Get("aid") != "8662" {
			t.Fatalf("注册请求必须带 aid=8662，实际 %q", request.URL.RawQuery)
		}
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	})
	registration, err := d.hongguoRegisterDevice(context.Background())
	if err != nil {
		t.Fatalf("注册应成功：%v", err)
	}
	if seenPath != "/service/2/device_register/" {
		t.Fatalf("注册路径不对：%q", seenPath)
	}
	if registration.DeviceID != "3295708855697689" || registration.InstallID != "3295708855701785" {
		t.Fatalf("设备号解析不对：%+v", registration)
	}
	if registration.Token != "AAA" {
		t.Fatalf("device_token 解析不对：%q", registration.Token)
	}
}

// device_id 为 0 说明 header 没被接受，必须重试而不是当成成功。
func TestHongguoRegisterDeviceRejectsZeroID(t *testing.T) {
	attempts := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		attempts++
		return sourceFixtureResponse(request, http.StatusOK, `{"server_time":1,"device_id":0,"install_id":0}`), nil
	})
	if _, err := d.hongguoRegisterDevice(context.Background()); err == nil {
		t.Fatal("device_id 为 0 时应报错")
	}
	if attempts != hongguoDeviceRegisterAttempts {
		t.Fatalf("应重试 %d 次，实际 %d 次", hongguoDeviceRegisterAttempts, attempts)
	}
}

// ensureHongguoDevice 成功后要写回状态，且只注册一次。
func TestEnsureHongguoDeviceRegistersOnce(t *testing.T) {
	registrations := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.Path, "device_register") {
			t.Fatalf("不该请求 %s", request.URL.Path)
		}
		registrations++
		return sourceFixtureResponse(request, http.StatusOK,
			`{"device_id_str":"76338817222425","install_id_str":"76338817226521"}`), nil
	})
	client := d.hongguoClient()
	client.mu.Lock()
	client.state.Registered = false
	client.state.DeviceID = "1111111111111111"
	client.state.InstallID = "2222222222222222"
	client.mu.Unlock()

	if err := d.ensureHongguoDevice(context.Background()); err != nil {
		t.Fatalf("注册应成功：%v", err)
	}
	client.mu.Lock()
	registered, deviceID, installID := client.state.Registered, client.state.DeviceID, client.state.InstallID
	client.mu.Unlock()
	if !registered {
		t.Fatal("注册后应标记 Registered")
	}
	if deviceID != "76338817222425" || installID != "76338817226521" {
		t.Fatalf("设备号应换成注册得到的值，实际 %s / %s", deviceID, installID)
	}
	// 第二次不应再注册。
	if err := d.ensureHongguoDevice(context.Background()); err != nil {
		t.Fatalf("第二次调用应直接返回：%v", err)
	}
	if registrations != 1 {
		t.Fatalf("只应注册一次，实际 %d 次", registrations)
	}
}

// 旧版持久化数据（Version 1）里的设备号是本地随机的，必须丢弃。
func TestRestoreHongguoCatalogDropsLegacyVersion(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("不该发请求")
	})
	// fixture downloader 预置了 Registered=true，这里退回「刚启动、尚未注册」
	// 的状态，才能验证旧数据不会把设备号伪装成已注册。
	client := d.hongguoClient()
	client.mu.Lock()
	client.state.Registered = false
	client.mu.Unlock()
	legacy := &hongguoCatalogState{Version: 1, DeviceID: "3295708855697689", InstallID: "3295708855701785", Feeds: map[string]hongguoCatalogCursor{}}
	d.restoreHongguoCatalog(legacy)
	client.mu.Lock()
	restored := client.state.DeviceID
	registered := client.state.Registered
	client.mu.Unlock()
	if restored == legacy.DeviceID {
		t.Fatal("Version 1 的设备号应被丢弃（它没在服务端注册过）")
	}
	if registered {
		t.Fatal("旧状态不应被标记成已注册")
	}
}
