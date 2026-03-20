// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/cron/store.go — CronJob CRUD 封装
package cron

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"OTTClaw/internal/storage"
)

// AddJob 新增定时任务
func AddJob(job *storage.CronJob) error {
	return storage.DB.Create(job).Error
}

// ListJobs 列出指定用户的所有定时任务（按创建时间升序）
func ListJobs(userID string) ([]storage.CronJob, error) {
	var jobs []storage.CronJob
	err := storage.DB.Where("user_id = ?", userID).
		Order("created_at ASC").
		Find(&jobs).Error
	return jobs, err
}

// GetJob 按 ID 和 userID 查询单条任务（用于权限校验）
func GetJob(id, userID string) (*storage.CronJob, error) {
	var job storage.CronJob
	result := storage.DB.Where("id = ? AND user_id = ?", id, userID).First(&job)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("cron job %q not found", id)
		}
		return nil, result.Error
	}
	return &job, nil
}

// UpdateJob 对指定 job 执行部分字段更新（patch 不能为空）
func UpdateJob(id, userID string, patch map[string]any) error {
	res := storage.DB.Model(&storage.CronJob{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(patch)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("cron job %q not found or access denied", id)
	}
	return nil
}

// RemoveJob 删除指定 job（需验证 userID 归属）
func RemoveJob(id, userID string) error {
	res := storage.DB.Where("id = ? AND user_id = ?", id, userID).
		Delete(&storage.CronJob{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("cron job %q not found or access denied", id)
	}
	return nil
}

// GetDueJobs 查询所有到期的已启用任务（跨用户，供调度器使用）
func GetDueJobs() ([]storage.CronJob, error) {
	var jobs []storage.CronJob
	now := time.Now()
	err := storage.DB.Where("enabled = ? AND next_run_at <= ?", true, now).
		Find(&jobs).Error
	return jobs, err
}

// RecordRun 记录一次任务运行结果：更新 last_run_at、run_count、next_run_at。
// 若 deleteAfterRun 为 true，则直接删除该 job（适用于 at 类型单次任务）。
func RecordRun(id string, nextRunAt *time.Time, deleteAfterRun bool) error {
	if deleteAfterRun {
		return storage.DB.Where("id = ?", id).Delete(&storage.CronJob{}).Error
	}
	now := time.Now()
	return storage.DB.Model(&storage.CronJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_run_at": now,
			"run_count":   gorm.Expr("run_count + 1"),
			"next_run_at": nextRunAt,
			"updated_at":  now,
		}).Error
}
