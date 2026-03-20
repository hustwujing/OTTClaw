// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/storage/feishu_config.go — 飞书配置持久化（AES-GCM 加密 AppSecret）
package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm"

	"OTTClaw/config"
)

// ── AES-GCM 加解密 ─────────────────────────────────────────────────────────────

// deriveKey 从 FeishuEncryptKey 派生 32 字节密钥（不足补 0，超出截断）
func deriveKey() ([]byte, error) {
	raw := config.Cfg.FeishuEncryptKey
	if raw == "" {
		return nil, errors.New("FEISHU_ENCRYPT_KEY not configured")
	}
	key := make([]byte, 32)
	copy(key, []byte(raw))
	return key, nil
}

// encryptSecret AES-GCM 加密明文，返回 base64(nonce+ciphertext)
func encryptSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := deriveKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("rand nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptSecret 解密 encryptSecret 返回的 base64 字符串
func decryptSecret(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	key, err := deriveKey()
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(plain), nil
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
