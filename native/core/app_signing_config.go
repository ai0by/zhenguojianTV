package core

// 红果取流签名档热更新（0.8.35）。
//
// 远程配置通过 configureHongguoSigning 注入，支持两种形态：
//   1. JSON 字符串：预置档名，如 "v04.06.04" / "current"；
//   2. JSON 对象：完整档参数（见 hgv5.SigningProfile），用于上游换了全新档时下发。
//
// 只作用于取流签名，目录/详情/搜索/弹幕的通用签名不受影响。校验失败不改当前档。

import (
	"bytes"
	"encoding/json"
	"fmt"

	"duanjuapp/native/core/hgv5"
)

// configureHongguoSigning 应用一份远程签名档配置，返回当前生效档名。
func (engine *nativeEngine) configureHongguoSigning(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("签名档配置为空")
	}

	// 形态 1：档名字符串。
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return "", fmt.Errorf("签名档名称无效")
		}
		if name == "" {
			return "", fmt.Errorf("签名档名称为空")
		}
		if err := hgv5.SelectSigningProfile(name); err != nil {
			return "", err
		}
		return hgv5.ActiveSigningProfile().Name, nil
	}

	// 形态 2：完整档对象。
	var p hgv5.SigningProfile
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return "", fmt.Errorf("签名档配置格式无效")
	}
	if err := hgv5.ApplySigningProfile(p); err != nil {
		return "", err
	}
	return hgv5.ActiveSigningProfile().Name, nil
}

// hongguoSigningStatus 返回当前生效档摘要，便于 UI / 诊断核对配置是否落地。
func hongguoSigningStatus() map[string]any {
	p := hgv5.ActiveSigningProfile()
	return map[string]any{
		"name":        p.Name,
		"medusa21":    p.Medusa21,
		"path":        p.Path,
		"deviceLevel": p.DeviceLevel,
		"gzipBody":    p.GzipBody,
	}
}
