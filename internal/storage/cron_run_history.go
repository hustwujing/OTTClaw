// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/storage/cron_run_history.go — 定时任务执行历史 CRUD
package storage

import "time"

// CreateCronRunHistory 写入一条 running 状态的执行记录，返回主键 ID 供后续更新。
func CreateCronRunHistory(jobID, userID, jobName, sessionID string) (uint, error) {
	r := &CronRunHistory{
		JobID:     jobID,
		UserID:    userID,
		JobName:   jobName,
		SessionID: sessionID,
		Status:    "running",
		StartedAt: time.Now(),
	}
	if err := DB.Create(r).Error; err != nil {
		return 0, err
	}
	return r.ID, nil
}

// UpdateCronRunHistory 更新执行记录的终态（succeeded / failed / timed_out / cancelled）及结束时间。
// 仅更新仍处于 running 状态的记录，防止覆盖 ForceKillCronRunHistory 已写入的终态。
func UpdateCronRunHistory(id uint, status, errorMsg string) error {
	now := time.Now()
	updates := map[string]any{
		"status":   status,
		"ended_at": now,
	}
	if errorMsg != "" {
		updates["error_msg"] = errorMsg
	}
	return DB.Model(&CronRunHistory{}).
		Where("id = ? AND status = ?", id, "running").
		Updates(updates).Error
}

// ForceKillCronRunHistory 立即将指定 job 的 running 状态记录强制置为 cancelled。
// 用于强制中止场景：调用方已发送 context cancel 信号，此函数同步更新 DB，
// 无需等待 goroutine 自然退出。
func ForceKillCronRunHistory(jobID string) error {
	now := time.Now()
	return DB.Model(&CronRunHistory{}).
		Where("job_id = ? AND status = ?", jobID, "running").
		Updates(map[string]any{
			"status":    "cancelled",
			"ended_at":  now,
			"error_msg": "force killed",
		}).Error
}

// ListCronRunHistory 返回指定 job 最近 limit 条历史（按 started_at 倒序）。
// jobID 为空时返回该用户下所有 job 的最近记录。limit ≤ 0 时默认 20，最大 100。
func ListCronRunHistory(jobID, userID string, limit int) ([]CronRunHistory, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var rows []CronRunHistory
	q := DB.Where("user_id = ?", userID).Order("started_at DESC").Limit(limit)
	if jobID != "" {
		q = q.Where("job_id = ?", jobID)
	}
	err := q.Find(&rows).Error
	return rows, err
}

// ListCronRunHistoryPaged 返回分页历史记录及总数。page 从 1 开始，pageSize 默认 20 最大 100。
// jobName 非空时对 job_name 做 LIKE 模糊匹配。
func ListCronRunHistoryPaged(userID, jobName string, page, pageSize int) ([]CronRunHistory, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	q := DB.Model(&CronRunHistory{}).Where("user_id = ?", userID)
	if jobName != "" {
		q = q.Where("job_name LIKE ?", "%"+jobName+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CronRunHistory
	offset := (page - 1) * pageSize
	err := q.Order("started_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// DeleteExpiredCronRunHistory 删除 started_at 早于 before 的终态记录（succeeded/failed/timed_out）。
// running 状态的记录不删除（避免删除正在执行中的记录）。
// 返回实际删除的行数。
func DeleteExpiredCronRunHistory(before time.Time) (int64, error) {
	result := DB.
		Where("status IN ? AND started_at < ?",
			[]string{"succeeded", "failed", "timed_out", "cancelled"}, before).
		Delete(&CronRunHistory{})
	return result.RowsAffected, result.Error
}
