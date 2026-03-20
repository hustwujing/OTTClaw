// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/storage/wecom_config.go — 企业微信机器人配置持久化
package storage

import (
	"errors"

	"gorm.io/gorm"
)

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

// SetWeComConfig 写入或更新企微 Webhook URL
func SetWeComConfig(userID, webhookURL string) error {
	record := &WeComConfig{
		UserID:     userID,
		WebhookURL: webhookURL,
	}
	return DB.Save(record).Error
}
