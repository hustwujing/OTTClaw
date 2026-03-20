// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/tool_request.go — /api/tool-requests
// 返回所有 LLM 上报的工具需求，供开发者跟进
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"OTTClaw/internal/storage"
)

// ListToolRequests 返回工具需求记录，支持 ?status=pending|done 过滤
// 不传 status 时返回全部
func ListToolRequests(c *gin.Context) {
	status := c.Query("status")
	rows, err := storage.ListToolRequests(status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []storage.ToolRequest{}
	}
	c.JSON(http.StatusOK, gin.H{"tool_requests": rows})
}

// UpdateToolRequestStatus PATCH /api/tool-requests/:id
// Body: {"status": "done"} 或 {"status": "pending"}
func UpdateToolRequestStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Status != "pending" && body.Status != "done" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status must be pending or done"})
		return
	}

	if err := storage.UpdateToolRequestStatus(uint(id), body.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
