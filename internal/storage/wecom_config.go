// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/wecom_config.go — 企业微信机器人配置持久化
// 包含两种模式：
//   - Webhook（群机器人，push-only）：SetWeComConfig / GetWeComConfig
//   - AI 机器人（双向长连接）：SetWeComBotConfig / GetWeComBotConfig / GetDecryptedWeComSecret
package storage

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"OTTClaw/config"
)

// ── AES-GCM 加解密（委托给 channel_crypto.go 的通用实现） ──────────────────────

// encryptWeComSecret AES-GCM 加密明文（使用 WECOM_ENCRYPT_KEY）
func encryptWeComSecret(plaintext string) (string, error) {
	if config.Cfg.WeComEncryptKey == "" {
		return "", errors.New("WECOM_ENCRYPT_KEY not configured; set it before saving bot credentials")
	}
	return EncryptSecret(plaintext, config.Cfg.WeComEncryptKey)
}

// decryptWeComSecret 解密 encryptWeComSecret 返回的 base64 字符串
func decryptWeComSecret(enc string) (string, error) {
	if config.Cfg.WeComEncryptKey == "" {
		return "", errors.New("WECOM_ENCRYPT_KEY not configured; set it before saving bot credentials")
	}
	return DecryptSecret(enc, config.Cfg.WeComEncryptKey)
}

// ── Webhook 模式 CRUD ──────────────────────────────────────────────────────────

// GetWeComConfig 获取指定用户的企微配置，不存在返回 (nil, nil)
func GetWeComConfig(userID string) (*WeComConfig, error) {
	var cfg WeComConfig
	err := DB.Where("user_id = ?", userID).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetWeComConfig 写入或更新企微 Webhook URL（不影响 bot_id / secret_enc）
func SetWeComConfig(userID, webhookURL string) error {
	existing, err := GetWeComConfig(userID)
	if err != nil {
		return err
	}
	if existing == nil {
		return DB.Create(&WeComConfig{UserID: userID, WebhookURL: webhookURL}).Error
	}
	return DB.Model(existing).Update("webhook_url", webhookURL).Error
}

// ── AI 机器人模式 CRUD ──────────────────────────────────────────────────────────

// SetWeComBotConfig 写入或更新用户的企微 AI 机器人凭证（bot_id + secret 加密存储）
func SetWeComBotConfig(userID, botID, secretPlain string) error {
	if botID == "" || secretPlain == "" {
		return errors.New("bot_id and secret are required")
	}
	enc, err := encryptWeComSecret(secretPlain)
	if err != nil {
		return fmt.Errorf("encrypt bot secret: %w", err)
	}
	existing, err := GetWeComConfig(userID)
	if err != nil {
		return err
	}
	if existing == nil {
		return DB.Create(&WeComConfig{
			UserID:    userID,
			BotID:     botID,
			SecretEnc: enc,
		}).Error
	}
	return DB.Model(existing).Updates(map[string]any{
		"bot_id":     botID,
		"secret_enc": enc,
	}).Error
}

// GetWeComBotConfig 返回指定用户的 Bot ID，未配置返回 ""
func GetWeComBotConfig(userID string) (string, error) {
	cfg, err := GetWeComConfig(userID)
	if err != nil || cfg == nil {
		return "", err
	}
	return cfg.BotID, nil
}

// GetDecryptedWeComSecret 获取并解密指定用户的 Bot Secret
func GetDecryptedWeComSecret(userID string) (string, error) {
	cfg, err := GetWeComConfig(userID)
	if err != nil {
		return "", err
	}
	if cfg == nil || cfg.SecretEnc == "" {
		return "", nil
	}
	return decryptWeComSecret(cfg.SecretEnc)
}

// GetAllWeComBotConfiguredUsers 返回所有已配置 AI 机器人凭证的用户 ID
func GetAllWeComBotConfiguredUsers() ([]string, error) {
	var cfgs []WeComConfig
	err := DB.Where("bot_id != '' AND secret_enc != ''").Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		ids = append(ids, c.UserID)
	}
	return ids, nil
}
