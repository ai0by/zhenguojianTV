package core

import (
	"encoding/json"
	"testing"

	"duanjuapp/native/core/hgv5"
)

func TestConfigureHongguoSigningBridge(t *testing.T) {
	defer hgv5.SelectSigningProfile("current")

	var engine *nativeEngine // 方法不依赖 engine 状态

	// 档名字符串形态。
	name, err := engine.configureHongguoSigning(json.RawMessage(`"v04.06.04"`))
	if err != nil || name != "v04.06.04" {
		t.Fatalf("按档名切换失败：name=%q err=%v", name, err)
	}
	st := hongguoSigningStatus()
	if st["deviceLevel"] != 4 || st["gzipBody"] != false {
		t.Fatalf("档状态不符：%+v", st)
	}

	// 完整档对象形态。
	p := hgv5.SigningProfile{
		Name: "custom", License: "646637794",
		Magic:      "2d4b4fca49750d433fb5ae2c226dcc56",
		SignKey:    "b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d",
		SDKVersion: "v04.10.03-ml-android", EnvVersion: "v04.10.03.04-bugfix",
		SDKCode: 0x040a0300, Medusa21: 312,
		Field24: `{"cmr":16777216}`, Path: "/novel/player/multi_video_model/player/v1",
		GzipBody: true, DeviceLevel: 1, DrScene: "player",
	}
	raw, _ := json.Marshal(p)
	name, err = engine.configureHongguoSigning(raw)
	if err != nil || name != "custom" {
		t.Fatalf("按对象切换失败：name=%q err=%v", name, err)
	}

	// 坏配置返回错误且不改档。
	if _, err := engine.configureHongguoSigning(json.RawMessage(`"no-such"`)); err == nil {
		t.Fatal("未知档应报错")
	}
	if hgv5.ActiveSigningProfile().Name != "custom" {
		t.Fatal("坏配置改变了当前档")
	}

	if _, err := engine.configureHongguoSigning(json.RawMessage(``)); err == nil {
		t.Fatal("空配置应报错")
	}
}
