// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/stats.go — GET /api/stats
// 返回当前正在执行的 agent 运行汇总，用于可观测性仪表板
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/runtrack"
)

// GetStats 处理 GET /api/stats
// 返回：summary（各 runtime 并发计数）+ runs（详细快照列表）
func GetStats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"summary": runtrack.Default.GetSummary(),
		"runs":    runtrack.Default.Snapshot(),
	})
}
