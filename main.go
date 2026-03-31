// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// main.go — 服务入口：初始化所有组件，注册路由，启动 HTTP 服务
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"OTTClaw/config"
	"OTTClaw/internal/agent"
	"OTTClaw/internal/browser"
	"OTTClaw/internal/cron"
	"OTTClaw/internal/feishu"
	"OTTClaw/internal/handler"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/mcp"
	"OTTClaw/internal/middleware"
	"OTTClaw/internal/runtrack"
	"OTTClaw/internal/skill"
	"OTTClaw/internal/storage"
	"OTTClaw/internal/tool"
)

func main() {
	// ---- 0. 初始化日志（文件输出，可选） ----
	if err := logger.Init(config.Cfg.LogDir, config.Cfg.LogFile, config.Cfg.LogLevel, config.Cfg.LogDataMaxLen); err != nil {
		log.Fatalf("init logger: %v", err)
	}

	// ---- 1. 初始化数据库 ----
	if err := storage.InitDB(); err != nil {
		log.Fatalf("init db: %v", err)
	}
	logger.Info("main", "", "", "database initialized", 0)

	// ---- 1.5 补齐历史 app_config 状态（avatar_url 已存在但 initialized=false 时自动修复） ----
	storage.MigrateAppConfig()

	// ---- 1.6 从持久化配置恢复 service base URL（供 Feishu 等非 HTTP 渠道构造完整下载链接） ----
	if appCfg, err := storage.GetAppConfig(); err == nil && appCfg.ServiceBaseURL != "" {
		tool.SetServerBaseURL(appCfg.ServiceBaseURL)
		logger.Info("main", "", "", "service base URL restored: "+appCfg.ServiceBaseURL, 0)
	}

	// ---- 2. 加载技能 HEAD（不加载 CONTENT） ----
	if err := skill.Store.LoadAll(config.Cfg.SkillsDir); err != nil {
		log.Fatalf("load skills: %v", err)
	}
	heads := skill.Store.GetAllHeads()
	logger.Info("skill", "", "", fmt.Sprintf("loaded %d skills", len(heads)), 0)

	// ---- 2.5 初始化 MCP Registry ----
	mcp.Global = &mcp.Registry{}
	if err := mcp.Global.Load(config.Cfg.MCPConfigPath); err != nil {
		logger.Warn("main", "", "", fmt.Sprintf("mcp config not loaded (%s), mcp tools disabled: %v", config.Cfg.MCPConfigPath, err), 0)
		mcp.Global = nil
	} else {
		logger.Info("main", "", "", fmt.Sprintf("mcp registry loaded from %s (%d servers)", config.Cfg.MCPConfigPath, len(mcp.Global.Servers())), 0)
	}

	// ---- 3. 初始化 Agent（加载 ROLE.md 和 TOOL.md） ----
	if err := agent.Init(); err != nil {
		log.Fatalf("init agent: %v", err)
	}
	logger.Info("agent", "", "", "agent initialized", 0)

	// ---- 3.5 孤儿子任务恢复 ----
	// 将上次进程退出时卡在 queued/running 的子任务标记为 failed，并通知父 agent。
	// 在后台 goroutine 中执行，不阻塞启动流程。
	go agent.Get().RecoverOrphanSubTasks()
	// 重试上次进程退出时因 LLM 调用失败（context canceled）而丢失的批量通知。
	go agent.Get().RecoverFailedNotifications()

	// ---- 3.6 同步已实现工具状态 ----
	if err := agent.SyncToolRequestStatus(); err != nil {
		logger.Warn("main", "", "", fmt.Sprintf("sync tool request status: %v", err), 0)
	} else {
		logger.Info("main", "", "", "tool request status synced", 0)
	}

	// ---- 3.7 启动 Node.js Playwright 浏览器 sidecar ----
	if err := browser.Default.Start(); err != nil {
		logger.Warn("main", "", "", fmt.Sprintf("browser server not started: %v", err), 0)
	} else {
		defer browser.Default.Stop()
	}

	// ---- 3.8 注入 agent runner 到 cron 包，并启动定时任务调度器 ----
	// RunCronJob 内部根据 creatorSession.Source 自动选择 writer：
	//   web    → push.CronWriter（SSE 实时推送）
	//   feishu → feishu.FeishuCronWriter（主动卡片消息）
	//   cron / 其他 → resultWriter（静默后台）
	cron.SetAgentRunner(func(ctx context.Context, userID, creatorSessionID, jobName, message string) error {
		return agent.Get().RunCronJob(ctx, userID, creatorSessionID, jobName, message)
	})
	cron.Default.Start()
	defer cron.Default.Stop()
	logger.Info("main", "", "", "cron scheduler started", 0)

	// ---- 4. 配置 Gin 路由 ----
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())       // 全局 panic recovery
	r.Use(middleware.BaseURL()) // 缓存 scheme://host，供工具拼接完整下载链接

	// 公开接口（无需鉴权）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.POST("/api/auth/login", handler.Login) // 邀请码换 JWT

	// 聊天前端页面：GET /
	r.StaticFile("/", "client/index.html")
	r.StaticFile("/favicon.ico", "client/favicon.ico")
	r.StaticFile("/marked.min.js", "client/marked.min.js")

	// 需要 JWT 鉴权的接口组
	authed := r.Group("/")
	authed.Use(middleware.JWTAuth())
	{
		// 应用信息（名称取自 ROLE.md）：GET /api/app-info
		authed.GET("/api/app-info", handler.GetAppInfo)

		// 创建会话：POST /api/session/create
		authed.POST("/api/session/create", handler.CreateSession)

		// 会话列表：GET /api/sessions
		authed.GET("/api/sessions", handler.ListSessions)

		// 删除会话：DELETE /api/session/:session_id
		authed.DELETE("/api/session/:session_id", handler.DeleteSession)

		// 工具需求列表：GET /api/tool-requests?status=pending|done（不传返回全部）
		authed.GET("/api/tool-requests", handler.ListToolRequests)
		// 更新工具需求状态：PATCH /api/tool-requests/:id  {"status":"done"|"pending"}
		authed.PATCH("/api/tool-requests/:id", handler.UpdateToolRequestStatus)

		// 文件上传：POST /api/upload（multipart/form-data, field: file）
		authed.POST("/api/upload", handler.Upload)

		// Token 消耗统计：GET /api/token-usage
		authed.GET("/api/token-usage", handler.GetTokenUsage)

		// 实时并发统计：GET /api/stats
		authed.GET("/api/stats", handler.GetStats)

		// 定时任务执行历史：GET /api/cron/history?q=&page=&page_size=
		authed.GET("/api/cron/history", handler.GetCronHistory)
		// 定时任务操作：取消 / 强制中止 / 立即触发 / 永久删除
		authed.POST("/api/cron/:job_id/cancel", handler.CancelCronJob)
		authed.POST("/api/cron/:job_id/force-kill", handler.ForceKillCronJob)
		authed.POST("/api/cron/:job_id/run", handler.RunCronJobNow)
		authed.DELETE("/api/cron/:job_id", handler.DeleteCronJob)

		// 子任务操作：取消
		authed.POST("/api/subtask/:task_id/cancel", handler.CancelSubTask)

		// 需要 JWT + 会话归属校验的接口
		sessionAuthed := authed.Group("/")
		sessionAuthed.Use(middleware.SessionOwner())
		{
			// WebSocket：GET /ws?session_id=xxx&token=xxx
			sessionAuthed.GET("/ws", handler.WS)

			// SSE：POST /sse?session_id=xxx&token=xxx
			sessionAuthed.POST("/sse", handler.SSE)

			// 会话历史消息：GET /api/session/messages?session_id=xxx
			sessionAuthed.GET("/api/session/messages", handler.GetSessionMessages)

			// 服务端主动推送：GET /api/notify?session_id=xxx&token=xxx（cron 任务结果实时推送）
			sessionAuthed.GET("/api/notify", handler.Notify)
		}
	}

	// 临时文件下载：GET /download/:token（由 serve_file_download 工具生成 token）
	r.GET("/download/:token", handler.Download)

	// 上传文件静态访问：GET /uploads/{dir}/{filename}
	r.Static("/uploads", config.Cfg.UploadDir)

	// 生成文件静态访问：GET /output/{dir}/{filename}（供前端内联展示图片等）
	r.Static("/output", config.Cfg.OutputDir)

	// ---- 5. 注入 agent runner 到 feishu 包，并启动长连接 ----
	feishu.SetAgentRunner(func(ctx context.Context, userID, sessionID, userText string, writer feishu.StreamWriter) error {
		defer runtrack.Default.Register("feishu", userID, sessionID)()
		// /subagents spawn <task> 命令：绕过 LLM，直接派发子 agent
		if task, ok := agent.ParseSpawnCmd(userText); ok {
			taskID, _, spawnErr := agent.Get().SpawnSubagentCmd(ctx, userID, sessionID, task)
			if spawnErr != nil {
				_ = writer.WriteError(fmt.Sprintf("子 agent 派发失败：%v", spawnErr))
			} else {
				_ = writer.WriteText(agent.SpawnCmdText(task, taskID))
			}
			_ = writer.WriteEnd()
			return spawnErr
		}
		return agent.Get().Run(ctx, userID, sessionID, userText, writer)
	})
	go feishu.Registry.StartAll(context.Background())
	logger.Info("main", "", "", "feishu registry started", 0)

	addr := ":" + config.Cfg.ServerPort
	logger.Info("main", "", "", "server starting on "+addr, 0)

	// 自定义 Listener：对每个 TCP 连接设置 TCP_NODELAY，禁用 Nagle 算法，
	// 确保 SSE 事件 Flush 后立即发送而不被合并到下一个 TCP 段。
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: r}

	// 将 Serve 放入后台 goroutine，main 阻塞在信号等待上。
	// 这样 shutdown 在 main goroutine 中同步完成后自然返回，
	// 所有 defer 均正常触发（atexit 等价），避免 os.Exit 绕过清理逻辑。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	// SIGUSR1：热重载 ROLE.md / TOOL.md / 技能，不重启进程、不中断运行中的会话。
	// 用法：kill -USR1 <pid>
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGUSR1)
	go func() {
		for range reload {
			logger.Info("main", "", "", "SIGUSR1: reloading config...", 0)
			if err := agent.Get().Reload(); err != nil {
				logger.Warn("main", "", "", "config reload failed: "+err.Error(), 0)
			} else {
				logger.Info("main", "", "", "ROLE.md + TOOL.md reloaded", 0)
			}
			if err := skill.Store.LoadAll(config.Cfg.SkillsDir); err != nil {
				logger.Warn("main", "", "", "skill reload failed: "+err.Error(), 0)
			} else {
				heads := skill.Store.GetAllHeads()
				logger.Info("main", "", "", fmt.Sprintf("skills reloaded: %d skill(s)", len(heads)), 0)
			}
		}
	}()

	go func() {
		if err := srv.Serve(tcpNoDelayListener{ln}); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// 阻塞直到收到信号
	<-quit
	logger.Info("main", "", "", "shutdown signal received, draining...", 0)

	// 停止所有飞书长连接（阻止新的 agent 调用，让进行中的 runAgent 靠 botCtx 取消）
	feishu.Registry.StopAll()

	// agent.Shutdown() 和 srv.Shutdown() 必须并行：
	// agent.Shutdown() 第一步关闭 shutdownCh，通知 SSE handler 取消 agentCtx；
	// SSE handler 收到信号后快速退出，srv.Shutdown() 才能完成连接排空。
	// 若串行（先 srv 后 agent），两者互相等待，导致 srv.Shutdown() 等满 30s 超时。
	agentDone := make(chan struct{})
	go func() {
		agent.Get().Shutdown(30 * time.Second)
		close(agentDone)
	}()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Warn("main", "", "", "http server shutdown error: "+err.Error(), 0)
	}

	// 等待后台任务（bgWg / honchoWg）全部完成
	<-agentDone
	logger.Info("main", "", "", "graceful shutdown complete", 0)
	// main 自然返回，所有 defer 正常执行
}

// tcpNoDelayListener 包装 net.Listener，对每个 Accept 的 TCP 连接设置 NoDelay。
type tcpNoDelayListener struct{ net.Listener }

func (l tcpNoDelayListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return conn, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	return conn, nil
}

