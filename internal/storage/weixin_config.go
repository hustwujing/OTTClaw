// internal/storage/weixin_config.go — 微信个人号配置持久化
package storage

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"OTTClaw/config"
)

func encryptWeixinToken(plaintext string) (string, error) {
	key := config.Cfg.WeixinEncryptKey
	if key == "" {
		return "", errors.New("WEIXIN_ENCRYPT_KEY not configured")
	}
	return EncryptSecret(plaintext, key)
}

func decryptWeixinToken(enc string) (string, error) {
	key := config.Cfg.WeixinEncryptKey
	if key == "" {
		return "", errors.New("WEIXIN_ENCRYPT_KEY not configured")
	}
	return DecryptSecret(enc, key)
}

func GetWeixinConfig(userID string) (*WeixinConfig, error) {
	var cfg WeixinConfig
	err := DB.Where("user_id = ?", userID).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &cfg, err
}

func SetWeixinConfig(userID, token, baseURL, botID, ilinkUserID string) error {
	if token == "" {
		return errors.New("token is required")
	}
	// 同一个微信账号（bot_id）只能绑定给一个用户
	if botID != "" {
		var count int64
		if err := DB.Model(&WeixinConfig{}).Where("bot_id = ? AND user_id != ?", botID, userID).Count(&count).Error; err != nil {
			return fmt.Errorf("check bot_id uniqueness: %w", err)
		}
		if count > 0 {
			return errors.New("该微信账号已被其他用户绑定，无法重复绑定")
		}
	}
	enc, err := encryptWeixinToken(token)
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}
	existing, err := GetWeixinConfig(userID)
	if err != nil {
		return err
	}
	if existing == nil {
		return DB.Create(&WeixinConfig{
			UserID: userID, TokenEnc: enc, BaseURL: baseURL,
			BotID: botID, OwnerIlinkUserID: ilinkUserID,
		}).Error
	}
	return DB.Model(existing).Updates(map[string]any{
		"token_enc": enc, "base_url": baseURL,
		"bot_id": botID, "owner_ilink_user_id": ilinkUserID,
	}).Error
}

// SetWeixinOwnerID 仅更新 owner_ilink_user_id 字段（用于首次收到消息时自动回填）
func SetWeixinOwnerID(userID, ilinkUserID string) error {
	return DB.Model(&WeixinConfig{}).Where("user_id = ?", userID).
		Update("owner_ilink_user_id", ilinkUserID).Error
}

func DeleteWeixinConfig(userID string) error {
	return DB.Where("user_id = ?", userID).Delete(&WeixinConfig{}).Error
}

func GetDecryptedWeixinToken(userID string) (string, error) {
	cfg, err := GetWeixinConfig(userID)
	if err != nil || cfg == nil || cfg.TokenEnc == "" {
		return "", err
	}
	return decryptWeixinToken(cfg.TokenEnc)
}

func GetAllWeixinConfiguredUsers() ([]string, error) {
	var cfgs []WeixinConfig
	err := DB.Where("token_enc != ''").Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		ids = append(ids, c.UserID)
	}
	return ids, nil
}
