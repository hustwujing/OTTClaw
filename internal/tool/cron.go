// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/tool/cron.go — cron 定时任务工具处理器
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	cronpkg "OTTClaw/internal/cron"
	"OTTClaw/internal/storage"
)

// handleCron 处理 cron 工具调用，支持 status/list/add/update/remove/run 六种 action
func handleCron(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Action   string          `json:"action"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Schedule json.RawMessage `json:"schedule"` // schedule 对象，直接传 JSON object
		Message  string          `json:"message"`
		Enabled  *bool           `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse cron args: %w", err)
	}

	userID := userIDFromCtx(ctx)

	switch args.Action {
	case "status":
		return cronStatus()
	case "list":
		return cronList(userID)
	case "add":
		return cronAdd(userID, sessionIDFromCtx(ctx), args.Name, string(args.Schedule), args.Message)
	case "update":
		return cronUpdate(userID, args.ID, args.Name, string(args.Schedule), args.Message, args.Enabled)
	case "remove":
		return cronRemove(userID, args.ID)
	case "run":
		return cronRun(userID, args.ID)
	default:
		return "", fmt.Errorf("unknown cron action %q; supported: status|list|add|update|remove|run", args.Action)
	}
}

func cronStatus() (string, error) {
	b, _ := json.Marshal(map[string]any{
		"status": "running",
		"tick":   "30s",
	})
	return string(b), nil
}

func cronList(userID string) (string, error) {
	jobs, err := cronpkg.ListJobs(userID)
	if err != nil {
		return "", fmt.Errorf("list cron jobs: %w", err)
	}
	b, _ := json.MarshalIndent(jobs, "", "  ")
	return string(b), nil
}

func cronAdd(userID, sessionID, name, schedJSON, message string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if schedJSON == "" || schedJSON == "null" {
		return "", fmt.Errorf("schedule is required")
	}
	if message == "" {
		return "", fmt.Errorf("message is required")
	}

	sched, err := cronpkg.ParseSchedule(schedJSON)
	if err != nil {
		return "", err
	}

	now := time.Now()
	nextRunAt, err := cronpkg.CalcNextRunAt(sched, nil, now)
	if err != nil {
		return "", fmt.Errorf("calc next run: %w", err)
	}

	job := &storage.CronJob{
		ID:               uuid.New().String(),
		UserID:           userID,
		Name:             name,
		Enabled:          true,
		ScheduleJSON:     schedJSON,
		Message:          message,
		CreatorSessionID: sessionID,
		DeleteAfterRun:   sched.Kind == cronpkg.KindAt,
		NextRunAt:        nextRunAt,
	}

	if err := cronpkg.AddJob(job); err != nil {
		return "", fmt.Errorf("add cron job: %w", err)
	}

	b, _ := json.MarshalIndent(job, "", "  ")
	return string(b), nil
}

func cronUpdate(userID, id, name, schedJSON, message string, enabled *bool) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	patch := map[string]any{}
	if name != "" {
		patch["name"] = name
	}
	if message != "" {
		patch["message"] = message
	}
	if enabled != nil {
		patch["enabled"] = *enabled
	}
	if schedJSON != "" && schedJSON != "null" {
		sched, err := cronpkg.ParseSchedule(schedJSON)
		if err != nil {
			return "", err
		}
		patch["schedule_json"] = schedJSON
		patch["delete_after_run"] = sched.Kind == cronpkg.KindAt
		now := time.Now()
		nextRunAt, err := cronpkg.CalcNextRunAt(sched, nil, now)
		if err != nil {
			return "", fmt.Errorf("calc next run: %w", err)
		}
		patch["next_run_at"] = nextRunAt
	}

	if len(patch) == 0 {
		return "", fmt.Errorf("no fields to update; provide at least one of: name, schedule, message, enabled")
	}

	if err := cronpkg.UpdateJob(id, userID, patch); err != nil {
		return "", fmt.Errorf("update cron job: %w", err)
	}
	return `{"ok":true}`, nil
}

func cronRemove(userID, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := cronpkg.RemoveJob(id, userID); err != nil {
		return "", fmt.Errorf("remove cron job: %w", err)
	}
	return `{"ok":true}`, nil
}

func cronRun(userID, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	job, err := cronpkg.GetJob(id, userID)
	if err != nil {
		return "", fmt.Errorf("get cron job: %w", err)
	}
	cronpkg.Default.RunJobNow(*job)
	return `{"ok":true,"message":"job triggered in background"}`, nil
}
