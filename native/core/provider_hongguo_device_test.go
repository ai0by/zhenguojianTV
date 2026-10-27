package core

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

// 注册接口固定返回 gzip 正文，必须能自动解压。
//
// 0.8.14 首个构建就是因为请求里自己设了 `Accept-Encoding: gzip`：net/http 的
// Transport 只在请求头没有 Accept-Encoding 时才自动解压，于是 io.ReadAll 拿到
// 压缩字节、json.Unmarshal 失败、注册重试 3 次全废，取流退回本地随机设备号 →
// 空正文 → 用户侧表现为「红果公开片源不可用」。
//
// 这个用例用真实 Transport 打 httptest server，所以能真正守住这条线：
// 一旦有人再把 Accept-Encoding 加回去，注册就会解析失败。
func TestHongguoRegisterDeviceDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(writer)
		_, _ = gzipWriter.Write([]byte(`{"device_id":2468876129669946,"install_id":2468876129674042,"device_id_str":"2468876129669946","install_id_str":"2468876129674042","device_token":"token"}`))
		_ = gzipWriter.Close()
	}))
	defer server.Close()
	original := hongguoDeviceRegisterURL
	hongguoDeviceRegisterURL = server.URL
	t.Cleanup(func() { hongguoDeviceRegisterURL = original })

	// 刻意用默认 Transport：自动解压是 Transport 的行为，fixture transport 测不到。
	downloader := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1},
		client:        &http.Client{},
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	registration, err := downloader.hongguoRegisterDevice(context.Background())
	if err != nil {
		t.Fatalf("gzip 响应应能被自动解压，实际报错：%v", err)
	}
	if registration.DeviceID != "2468876129669946" || registration.InstallID != "2468876129674042" {
		t.Fatalf("设备号解析错误：%+v", registration)
	}
}

// 注册失败后要进冷却，不能每次取流都重试满 3 次。
func TestEnsureHongguoDeviceCooldownSkipsRetry(t *testing.T) {
	var registrations int
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "device_register") {
			registrations++
			return nil, errors.New("网络不通")
		}
		return nil, errors.New("不该发这个请求")
	})
	client := downloader.hongguoClient()
	client.mu.Lock()
	client.state.Registered = false
	client.mu.Unlock()

	if err := downloader.ensureHongguoDevice(context.Background()); err == nil {
		t.Fatal("网络不通时注册应失败")
	}
	if registrations != hongguoDeviceRegisterAttempts {
		t.Fatalf("首次应尝试 %d 次，实际 %d 次", hongguoDeviceRegisterAttempts, registrations)
	}
	// 冷却期内再调用，不应再发请求。
	if err := downloader.ensureHongguoDevice(context.Background()); err == nil {
		t.Fatal("冷却期内应直接返回上次的错误")
	}
	if registrations != hongguoDeviceRegisterAttempts {
		t.Fatalf("冷却期内不应重试，实际又发了 %d 次", registrations-hongguoDeviceRegisterAttempts)
	}
}

// 设备没注册成功时，取流必然空正文；公开片源与网页只会给 10 秒预告，
// 所以主链路要直接短路，不浪费两个网络往返、也不把根因埋在别的错误后面。
func TestResolveHongguoMediaShortCircuitsWhenDeviceUnregistered(t *testing.T) {
	var paths []string
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if strings.Contains(request.URL.Path, "device_register") {
			return nil, errors.New("网络不通")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	client := downloader.hongguoClient()
	client.mu.Lock()
	client.state.Registered = false
	client.mu.Unlock()

	task := Task{
		DramaID: "hongguo:7605500183484320792",
		Chapter: Chapter{VideoURL: "hongguo-cenc://7605502162419846206"},
	}
	_, err := downloader.resolveHongguoMedia(context.Background(), task)
	if err == nil {
		t.Fatal("设备未注册时取流应失败")
	}
	if !strings.Contains(err.Error(), "红果游客设备注册失败") {
		t.Fatalf("错误应直指根因，实际：%v", err)
	}
	for _, path := range paths {
		if strings.Contains(path, "/video/public/") {
			t.Fatalf("注册失败后不该再试公开片源，实际请求了 %s", path)
		}
	}
}
