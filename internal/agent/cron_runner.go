// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/agent/cron_runner.go — 定时任务执行路由
//
// RunCronJob 是 cron.AgentRunFunc 的唯一实现入口。
// 根据 creatorSession 的来源（web / feishu / cron）自动选择合适的 StreamWriter：
//
//   - web    → push.CronWriter    实时 SSE 事件推送到前端 /api/notify
//   - feishu → feishu.CronWriter  主动推送飞书进度卡片，完成后更新为最终结果
//   - cron   → resultWriter       纯后台静默执行，结果仅落 session_messages，不推送
package agent

import (
	"context"

	"OTTClaw/internal/feishu"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/push"
	"OTTClaw/internal/runtrack"
	"OTTClaw/internal/storage"
)

// RunCronJob 执行定时任务。由 main.go 注入 cron.SetAgentRunner 后，由调度器调用。
// creatorSessionID 是创建该任务时所在的会话，用于判断回写渠道和存储执行记录。
func (a *Agent) RunCronJob(ctx context.Context, userID, creatorSessionID, jobName, message string) error {
	defer runtrack.Default.Register("cron", userID, creatorSessionID)()
	w := a.newCronWriter(creatorSessionID, jobName, message)
	return a.Run(ctx, userID, creatorSessionID, message, w)
}

// newCronWriter 根据 creatorSession 的来源选择回写渠道。
//
//   - "web"    → push.CronWriter（实时 SSE，前端已订阅 /api/notify 时可见）
//   - "feishu" → feishu.FeishuCronWriter（主动消息，用户在飞书对话中可见）
//   - "cron" / 其他 → resultWriter（静默，结果仅存 session_messages）
func (a *Agent) newCronWriter(creatorSessionID, jobName, message string) StreamWriter {
	if creatorSessionID == "" {
		return &resultWriter{}
	}

	sess, err := storage.GetSession(creatorSessionID)
	if err != nil || sess == nil {
		logger.Warn("cron", "", creatorSessionID,
			"newCronWriter: failed to get creator session, falling back to resultWriter", 0)
		return &resultWriter{}
	}

	switch sess.Source {
	case "web":
		// 实时推送到前端 /api/notify SSE 通道
		return push.NewCronWriter(creatorSessionID, jobName, message)

	case "feishu":
		// 主动推送飞书卡片：启动时发"执行中"，完成后更新为最终结果
		if cfg, err := storage.GetFeishuConfig(sess.UserID); err == nil &&
			cfg != nil && cfg.AppID != "" {
			receiveIDType := receiveIDTypeFromPeer(sess.FeishuPeer)
			return feishu.NewCronWriter(
				sess.UserID,
				sess.FeishuPeer,
				cfg.AppID,
				receiveIDType,
				jobName,
			)
		}
		logger.Warn("cron", sess.UserID, creatorSessionID,
			"newCronWriter: feishu config missing, falling back to resultWriter", 0)
		return &resultWriter{}

	default:
		// "cron" source 或未知来源：静默后台执行
		return &resultWriter{}
	}
}
