// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/agent"
	"OTTClaw/internal/middleware"
	"OTTClaw/internal/storage"
)

// CancelSubTask 处理 POST /api/subtask/:task_id/cancel
// 向正在运行的子任务发送取消信号（graceful）。
func CancelSubTask(c *gin.Context) {
	userID := middleware.GetUserID(c)
	idStr := c.Param("task_id")
	id64, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task_id"})
		return
	}
	taskID := uint(id64)

	// 验证任务归属
	task, err := storage.GetSubTask(taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if task == nil || task.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 已处于终态则直接返回
	switch task.Status {
	case "succeeded", "failed", "timed_out", "lost", "cancelled", "killed":
		c.JSON(http.StatusOK, gin.H{"ok": false, "status": task.Status, "note": "task already in terminal state"})
		return
	}

	wasRunning := agent.Get().CancelSubTask(taskID)
	c.JSON(http.StatusOK, gin.H{"ok": wasRunning, "was_running": wasRunning})
}
