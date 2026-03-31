// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/cron.go — cron 任务的 REST API 处理器
package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	cronpkg "OTTClaw/internal/cron"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/middleware"
	"OTTClaw/internal/storage"
)

// GetCronHistory 处理 GET /api/cron/history
// Query 参数：q（任务名称模糊搜索，可选）、page（从 1 开始，默认 1）、page_size（默认 20，最大 100）
func GetCronHistory(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)

	jobName := c.Query("q")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	rows, total, err := storage.ListCronRunHistoryPaged(userID, jobName, page, pageSize)
	if err != nil {
		logger.Error("cron-history", userID, "", "list cron run history failed", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load history failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rows":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// CancelCronJob 处理 POST /api/cron/:job_id/cancel
// 向正在运行的 job 发送取消信号（graceful）；goroutine 响应后 history 状态置为 cancelled。
func CancelCronJob(c *gin.Context) {
	userID := middleware.GetUserID(c)
	jobID := c.Param("job_id")

	// 验证 job 归属
	if _, err := cronpkg.GetJob(jobID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	wasRunning := cronpkg.Default.CancelJob(jobID)
	c.JSON(http.StatusOK, gin.H{"ok": wasRunning, "was_running": wasRunning})
}

// ForceKillCronJob 处理 POST /api/cron/:job_id/force-kill
// 立即更新 DB 记录为 cancelled，并发送 context cancel 信号。
// DB 同步更新，无需等待 goroutine 退出。
func ForceKillCronJob(c *gin.Context) {
	userID := middleware.GetUserID(c)
	jobID := c.Param("job_id")

	// 验证 job 归属
	if _, err := cronpkg.GetJob(jobID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 先更新 DB（同步），再发信号给 goroutine
	if err := storage.ForceKillCronRunHistory(jobID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cronpkg.Default.CancelJob(jobID) // 发送信号；goroutine 的 UpdateCronRunHistory 因 status 已终态而幂等
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RunCronJobNow 处理 POST /api/cron/:job_id/run
// 同步预创建 history 记录（status=running），立即返回该记录，goroutine 在后台执行并在结束时更新终态。
// 前端收到响应后可立即将该记录插入历史列表，无需等待异步轮询。
func RunCronJobNow(c *gin.Context) {
	userID := middleware.GetUserID(c)
	jobID := c.Param("job_id")

	job, err := cronpkg.GetJob(jobID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 确定 sessionID（与 scheduler.runJob 逻辑一致）
	sessionID := job.CreatorSessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
		if err2 := storage.CreateSessionWithSource(sessionID, job.UserID, "cron"); err2 != nil {
			logger.Error("cron-run-now", userID, "", "create fallback session failed", err2, 0)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create session failed"})
			return
		}
	}

	// 同步预创建 history 记录，获得确定的 ID 和 started_at
	startedAt := time.Now()
	histID, err := storage.CreateCronRunHistory(job.ID, job.UserID, job.Name, sessionID)
	if err != nil {
		logger.Error("cron-run-now", userID, "", "create run history failed", err, 0)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create history failed"})
		return
	}

	// 启动 goroutine；若 job 已在运行中则返回 false
	started := cronpkg.Default.RunJobNow(*job, sessionID, histID)
	if !started {
		// 已有运行中的 goroutine — 将孤儿记录标记为 cancelled
		_ = storage.UpdateCronRunHistory(histID, "cancelled", "job already running")
		c.JSON(http.StatusOK, gin.H{
			"ok":      false,
			"started": false,
			"message": "job is already running",
		})
		return
	}

	// 返回预创建的记录，前端可立即渲染
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"started": true,
		"record": map[string]any{
			"id":         histID,
			"job_id":     job.ID,
			"job_name":   job.Name,
			"user_id":    job.UserID,
			"session_id": sessionID,
			"status":     "running",
			"started_at": startedAt,
		},
	})
}

// DeleteCronJob 处理 DELETE /api/cron/:job_id
// 永久删除定时任务（若 job 正在运行，先发送取消信号）。
func DeleteCronJob(c *gin.Context) {
	userID := middleware.GetUserID(c)
	jobID := c.Param("job_id")

	// 若 job 正在运行，先发取消信号
	cronpkg.Default.CancelJob(jobID)

	if err := cronpkg.RemoveJob(jobID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
