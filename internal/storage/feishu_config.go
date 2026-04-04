// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/feishu_config.go — 飞书配置持久化（AES-GCM 加密 AppSecret）
package storage

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"OTTClaw/config"
)

// ── AES-GCM 加解密（委托给 channel_crypto.go 的通用实现） ──────────────────────

// encryptSecret AES-GCM 加密明文（使用 FEISHU_ENCRYPT_KEY）
func encryptSecret(plaintext string) (string, error) {
	if config.Cfg.FeishuEncryptKey == "" {
		return "", errors.New("FEISHU_ENCRYPT_KEY not configured")
	}
	return EncryptSecret(plaintext, config.Cfg.FeishuEncryptKey)
}

// decryptSecret 解密 encryptSecret 返回的 base64 字符串
func decryptSecret(enc string) (string, error) {
	if config.Cfg.FeishuEncryptKey == "" {
		return "", errors.New("FEISHU_ENCRYPT_KEY not configured")
	}
	return DecryptSecret(enc, config.Cfg.FeishuEncryptKey)
}

// ── DB CRUD ────────────────────────────────────────────────────────────────────

// GetFeishuConfig 获取指定用户的飞书配置，不存在返回 (nil, nil)
func GetFeishuConfig(userID string) (*FeishuConfig, error) {
	var cfg FeishuConfig
	err := DB.Where("user_id = ?", userID).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetFeishuConfig 写入或更新飞书配置。
// appSecretPlain 为明文 AppSecret（非空则加密存储，空则保留已有值）。
// selfOpenID 为用户自己的飞书 open_id（空则保留已有值）。
func SetFeishuConfig(userID string, appID, appSecretPlain, webhookURL, selfOpenID string) error {
	existing, err := GetFeishuConfig(userID)
	if err != nil {
		return err
	}

	enc := ""
	if appSecretPlain != "" {
		enc, err = encryptSecret(appSecretPlain)
		if err != nil {
			return fmt.Errorf("encrypt app secret: %w", err)
		}
	} else if existing != nil {
		enc = existing.AppSecretEnc // 保留已有加密值
	}

	// selfOpenID 为空时保留已有值
	if selfOpenID == "" && existing != nil {
		selfOpenID = existing.SelfOpenID
	}

	record := &FeishuConfig{
		UserID:       userID,
		AppID:        appID,
		AppSecretEnc: enc,
		WebhookURL:   webhookURL,
		SelfOpenID:   selfOpenID,
	}

	return DB.Save(record).Error
}

// GetSelfOpenID 返回指定用户绑定的飞书 open_id，未绑定返回 ""
func GetSelfOpenID(userID string) (string, error) {
	cfg, err := GetFeishuConfig(userID)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.SelfOpenID, nil
}

// GetDecryptedAppSecret 获取并解密指定用户的 AppSecret
func GetDecryptedAppSecret(userID string) (string, error) {
	cfg, err := GetFeishuConfig(userID)
	if err != nil {
		return "", err
	}
	if cfg == nil || cfg.AppSecretEnc == "" {
		return "", nil
	}
	return decryptSecret(cfg.AppSecretEnc)
}

// GetAllConfiguredUsers 返回所有已配置飞书机器人（有 AppID + AppSecretEnc）的用户 ID
func GetAllConfiguredUsers() ([]string, error) {
	var cfgs []FeishuConfig
	err := DB.Where("app_id != '' AND app_secret_enc != ''").Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		ids = append(ids, c.UserID)
	}
	return ids, nil
}
