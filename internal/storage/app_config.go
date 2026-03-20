// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/storage/app_config.go — 持久化应用 UI 配置（config/app.json）
package storage

import (
	"encoding/json"
	"os"
	"sync"
)

const appConfigPath = "config/app.json"

// AppConfig UI 级别的应用配置（头像、fs 白名单等），持久化到 config/app.json。
type AppConfig struct {
	AvatarURL      string   `json:"avatar_url"`
	Initialized    bool     `json:"initialized"`      // bootstrap 完成后置 true，之后禁止修改头像
	ExtraFsDirs    []string `json:"extra_fs_dirs"`    // fs 工具额外可读目录，由管理员在 bootstrap 时配置
	ServiceBaseURL string   `json:"service_base_url"` // 服务对外 base URL（scheme://host），首次 HTTP 请求后自动写入
}

var appConfigMu sync.RWMutex

// GetAppConfig 读取 config/app.json，文件不存在时返回空配置。
func GetAppConfig() (*AppConfig, error) {
	appConfigMu.RLock()
	defer appConfigMu.RUnlock()
	return readAppConfig()
}

// SetAvatarURL 将 avatar_url 持久化到 config/app.json（线程安全）。
func SetAvatarURL(url string) error {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()

	cfg, _ := readAppConfig()
	cfg.AvatarURL = url

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appConfigPath, data, 0644)
}

// SetExtraFsDirs 将 extra_fs_dirs 持久化到 config/app.json（线程安全）。
func SetExtraFsDirs(dirs []string) error {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()

	cfg, _ := readAppConfig()
	cfg.ExtraFsDirs = dirs

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appConfigPath, data, 0644)
}

// MigrateAppConfig 启动时补齐历史数据：若 avatar_url 已存在但 initialized 仍为 false，
// 说明是在本功能上线前完成的 bootstrap，自动补全标记。
func MigrateAppConfig() {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()

	cfg, err := readAppConfig()
	if err != nil || cfg.Initialized || cfg.AvatarURL == "" {
		return
	}
	cfg.Initialized = true
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(appConfigPath, data, 0644)
}

// MarkInitialized 将 initialized 标记为 true，bootstrap 完成后调用。
// 之后 update_role_md 将拒绝一切请求，直到管理员手动将 initialized 改回 false。
func MarkInitialized() error {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()

	cfg, _ := readAppConfig()
	cfg.Initialized = true

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appConfigPath, data, 0644)
}

// SetServiceBaseURL 将服务 base URL 持久化到 config/app.json（线程安全）。
// 仅在值发生变化时写文件，避免不必要的 IO。
func SetServiceBaseURL(baseURL string) error {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()

	cfg, _ := readAppConfig()
	if cfg.ServiceBaseURL == baseURL {
		return nil // 无变化，跳过写文件
	}
	cfg.ServiceBaseURL = baseURL

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(appConfigPath, data, 0644)
}

// readAppConfig 内部读取函数（调用方须持有锁）。
func readAppConfig() (*AppConfig, error) {
	data, err := os.ReadFile(appConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &AppConfig{}, nil
		}
		return &AppConfig{}, err
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &AppConfig{}, nil
	}
	return &cfg, nil
}
