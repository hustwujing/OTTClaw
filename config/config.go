// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// config/config.go — 全局配置，所有配置项集中管理
//
// 优先级（从高到低）：
//  1. 系统环境变量（export KEY=value 或 Docker/K8s 注入）
//  2. .env 文件（项目根目录，存放敏感密钥，不提交 git）
//  3. 代码内置默认值
//
// .env 文件不存在时静默跳过，不影响启动。
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// LLMEndpointConfig 单个 LLM 节点配置
type LLMEndpointConfig struct {
	BaseURL   string // LLM_BASE_URL[_N]
	APIKey    string // LLM_API_KEY[_N]
	Model     string // LLM_MODEL[_N]
	Provider  string // LLM_PROVIDER[_N]: "openai"（默认）| "anthropic"
	MaxTokens int    // LLM_MAX_TOKENS[_N]
	RPM       int    // LLM_RPM[_N]: 每分钟最大请求数，0 表示不限制
}

// AppConfig 全局配置结构体
type AppConfig struct {
	// 服务器
	ServerPort string

	// JWT
	JWTSecret string

	// LLM 接入（日志/token 统计用，实际调用通过 LLMEndpoints）
	LLMModel string

	// 数据存储
	DatabaseDriver   string // DATABASE_DRIVER: "sqlite"（默认）| "mysql"
	DatabasePath     string // SQLite 文件路径（仅 sqlite 模式）
	DatabaseHost     string // MySQL 主机
	DatabasePort     string // MySQL 端口
	DatabaseName     string // MySQL 数据库名
	DatabaseUser     string // MySQL 用户名
	DatabasePassword string // MySQL 密码（建议通过环境变量注入）

	// 文件路径
	SkillsDir  string
	RoleMDPath string
	ToolMDPath string
	UploadDir  string // UPLOAD_DIR：上传文件根目录，默认 uploads
	OutputDir  string // OUTPUT_DIR：生成文件根目录，默认 output（按 MD5 第二位分桶）

	// 日志
	LogLevel      string
	LogDir        string // LOG_DIR：日志文件目录，空字符串表示只写 stdout
	LogFile       string // LOG_FILE：日志文件名，空字符串表示只写 stdout
	LogDataMaxLen int    // LOG_DATA_MAX_LEN：DebugData 截断阈值（字符数），超出则打印首尾各半；0 不截断，默认 600

	// LLM Provider
	LLMProvider  string // LLM_PROVIDER: "openai"（默认）| "anthropic"
	LLMMaxTokens int    // LLM_MAX_TOKENS: Anthropic 必填，默认 8096
	LLMRateLimit int    // LLM_RPM: 每分钟最大请求数，0 表示不限制

	// LLM 多节点（负载均衡）
	// 第一个节点始终为主节点（LLM_BASE_URL / LLM_API_KEY / LLM_MODEL）。
	// 额外节点通过 LLM_BASE_URL_2、LLM_API_KEY_2、LLM_MODEL_2 ... 配置，
	// 直到某个编号的 LLM_BASE_URL_N 为空时停止扫描（最多支持 N=10）。
	// LLMEndpoints 始终至少包含一个元素（主节点）。
	LLMEndpoints []LLMEndpointConfig

	// 对话历史压缩
	// MaxContextTokens: 触发压缩的 token 估算阈值（超过则压缩旧消息）
	// CompressKeepRecent: 压缩时保留最新的 N 条消息不参与摘要
	MaxContextTokens   int
	CompressKeepRecent int

	// 飞书集成
	FeishuEncryptKey        string // FEISHU_ENCRYPT_KEY：AES-GCM 加密 AppSecret 的密钥（32字节hex或任意长字符串）
	FeishuAPIBase           string // FEISHU_API_BASE：飞书 Open API 基础地址，私有部署或代理时修改，默认 https://open.feishu.cn
	FeishuSpinnerIntervalMs int    // FEISHU_SPINNER_INTERVAL_MS：飞书侧 spinner 动画刷新间隔毫秒数，默认 800
	FeishuPendingTimeoutMin int    // FEISHU_PENDING_TIMEOUT_MIN：等待用户操作（上传文件等）的超时分钟数，默认 30

	// nano-banana 图像生成
	NanoBananaAPIKey  string // NANO_BANANA_API_KEY：Bilibili LLM API 密钥（必填）
	NanoBananaBaseURL string // NANO_BANANA_BASE_URL：API 基础地址，默认 http://llmapi.bilibili.co/v1
	NanoBananaModel   string // NANO_BANANA_MODEL：模型名称，默认 ppio/nano-banana-pro

	// Agent 循环
	AgentMaxIterations        int    // AGENT_MAX_ITERATIONS：LLM 循环最大轮数，默认 20
	ProgressLabel             string // PROGRESS_LABEL：进度事件的 step 标识，前端可展示，默认 "progress"
	SelfImprovingMinToolIters int    // SELF_IMPROVING_MIN_TOOL_ITERS：触发自我进化技能生成所需的最少工具调用轮次，默认 3
	SelfImprovingMaxSkills      int // SELF_IMPROVING_MAX_SKILLS：每用户允许保留的自进化技能上限，超出时按近似 LFU 淘汰，默认 20，0 禁用
	SelfImprovingLFUDecayHours  int // SELF_IMPROVING_LFU_DECAY_HOURS：近似 LFU 计数器半衰期（小时），默认 24
	SelfImprovingProtectMinutes int // SELF_IMPROVING_PROTECT_MINUTES：新技能保护窗口（分钟），窗口内不参与淘汰，默认 60，0 禁用

	// 浏览器自动化
	BrowserServerPort   string // BROWSER_SERVER_PORT：Node.js Playwright sidecar 监听端口，默认 9222
	BrowserServerScript string // BROWSER_SERVER_SCRIPT：Node.js 脚本路径，默认 browser-server/server.js
	BrowserHeadless     bool   // BROWSER_HEADLESS：是否无头模式，默认 true；开发调试时设为 false 可看到浏览器窗口

	// 工具执行超时
	ToolScriptTimeoutSec   int // TOOL_SCRIPT_TIMEOUT_SEC：run_script 工具脚本执行超时秒数，默认 60
	ToolExecTimeoutSec     int // TOOL_EXEC_TIMEOUT_SEC：exec 工具默认总超时秒数，默认 1800（30 分钟）
	ToolExecYieldMs        int // TOOL_EXEC_YIELD_MS：exec 工具默认 yield 等待毫秒数，默认 10000
	ToolWebFetchTimeoutSec int // TOOL_WEB_FETCH_TIMEOUT_SEC：web_fetch 工具 HTTP 请求超时秒数，默认 15
	WeComWebhookTimeoutSec int // WECOM_WEBHOOK_TIMEOUT_SEC：企业微信 Webhook 请求超时秒数，默认 10
	DownloadTTLMin         int // DOWNLOAD_TTL_MIN：output_file 生成的下载链接有效期（分钟），默认 30

	// 工具结果 DB 截断
	ToolResultMaxDBBytes int // TOOL_RESULT_MAX_DB_BYTES：工具结果写入 DB 的最大字节数，超出时截断并追加提示；0 不截断，默认 2000

	// 工具失败进度摘要截断
	ToolErrorSummaryLen int // TOOL_ERROR_SUMMARY_LEN：工具调用失败时推送给前端的错误摘要最大字符数（rune），默认 32

	// fs(action=read) 文本文件大小上限
	FsReadMaxBytes int // FS_READ_MAX_BYTES：fs read 允许读取的最大字节数，超出拒绝；默认 524288（512 KB）

	// 文件上传大小上限
	UploadMaxBytes int // UPLOAD_MAX_BYTES：单次上传文件的最大字节数，超出拒绝；0 不限制，默认 20971520（20 MB）

	// read_file 文本提取大小上限
	ReadFileMaxBytes int // READ_FILE_MAX_BYTES：read_file 从 .docx/.pdf/.pptx 提取文本的最大字节数；0 不限制，默认 20971520（20 MB）

	// read_image 图片读取大小上限
	ReadImageMaxBytes int // READ_IMAGE_MAX_BYTES：read_image 读取图片的最大字节数，超出自动缩放；0 不限制，默认 5242880（5 MB）

	// MCP 外接能力
	MCPConfigPath string // MCP_CONFIG_PATH：MCP server 配置文件路径，默认 config/mcp.json

	// 长期记忆
	MemoryEnabled          bool // MEMORY_ENABLED：是否启用 memory 工具（notes/persona 读写），默认 true
	MemoryFlushMinTurns    int  // MEMORY_FLUSH_MIN_TURNS：触发 session 结束 flush 所需的最少 user 消息数，默认 6，0 禁用
	MemoryNudgeInterval    int  // MEMORY_NUDGE_INTERVAL：后台 review 触发轮次间隔，默认 10，0 禁用
	MemoryNotesCharLimit   int  // MEMORY_NOTES_CHAR_LIMIT：Agent notes 字符上限，默认 2200
	MemoryPersonaCharLimit int  // MEMORY_PERSONA_CHAR_LIMIT：用户人设字符上限，默认 1375

	// 跨会话搜索
	SessionSearchEnabled         bool // SESSION_SEARCH_ENABLED：是否启用 session_search 工具，默认 true
	SessionSearchSummaryMaxChars int  // SESSION_SEARCH_SUMMARY_MAX_CHARS：摘要上下文窗口最大字符数，默认 50000

	// Honcho AI-native memory platform
	HonchoEnabled bool   // HONCHO_ENABLED：是否启用 Honcho 集成，默认 false
	HonchoBaseURL string // HONCHO_BASE_URL：Honcho API 地址，默认 https://demo.honcho.dev
	HonchoAPIKey  string // HONCHO_API_KEY：Honcho API 密钥
	HonchoAppID   string // HONCHO_APP_ID：预先创建的 App ID（空则用 HonchoAppName 自动 get_or_create）
	HonchoAppName string // HONCHO_APP_NAME：Honcho 应用名称，默认 ottclaw
}

// Cfg 全局配置单例，进程启动时初始化一次
var Cfg = loadConfig()

// dotEnvCache 保存从 .env 文件读取的 KV（不写入 os 环境，避免污染子进程）
var dotEnvCache map[string]string

// loadLLMEndpoints 构建 LLM 节点列表。
// 始终将主节点（LLM_BASE_URL / LLM_API_KEY / LLM_MODEL）作为第一个元素，
// 再依次扫描 LLM_BASE_URL_2、LLM_BASE_URL_3 ... LLM_BASE_URL_10，
// 遇到空值则停止扫描。
func loadLLMEndpoints(primaryProvider string, primaryMaxTokens, primaryRPM int) []LLMEndpointConfig {
	primary := LLMEndpointConfig{
		BaseURL:   getEnv("LLM_BASE_URL", "https://api.openai.com"),
		APIKey:    getEnv("LLM_API_KEY", ""),
		Model:     getEnv("LLM_MODEL", "gpt-4o"),
		Provider:  primaryProvider,
		MaxTokens: primaryMaxTokens,
		RPM:       primaryRPM,
	}
	endpoints := []LLMEndpointConfig{primary}

	for i := 2; i <= 10; i++ {
		suffix := fmt.Sprintf("_%d", i)
		baseURL := getEnv("LLM_BASE_URL"+suffix, "")
		if baseURL == "" {
			break
		}
		ep := LLMEndpointConfig{
			BaseURL:   baseURL,
			APIKey:    getEnv("LLM_API_KEY"+suffix, primary.APIKey),
			Model:     getEnv("LLM_MODEL"+suffix, primary.Model),
			Provider:  getEnv("LLM_PROVIDER"+suffix, primaryProvider),
			MaxTokens: getEnvInt("LLM_MAX_TOKENS"+suffix, primaryMaxTokens),
			RPM:       getEnvInt("LLM_RPM"+suffix, 0),
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// loadConfig 按优先级读取所有配置项
func loadConfig() *AppConfig {
	loadDotEnv(".env")

	cfg := &AppConfig{
		ServerPort:              getEnv("SERVER_PORT", "8080"),
		JWTSecret:               getEnv("JWT_SECRET", "change-me-in-production-secret-key"),
		LLMModel:                getEnv("LLM_MODEL", "gpt-4o"),
		DatabaseDriver:          getEnv("DATABASE_DRIVER", "sqlite"),
		DatabasePath:            getEnv("DATABASE_PATH", "data.db"),
		DatabaseHost:            getEnv("DATABASE_HOST", "127.0.0.1"),
		DatabasePort:            getEnv("DATABASE_PORT", "3306"),
		DatabaseName:            getEnv("DATABASE_NAME", "skill_executor"),
		DatabaseUser:            getEnv("DATABASE_USER", "root"),
		DatabasePassword:        getEnv("DATABASE_PASSWORD", ""),
		SkillsDir:               getEnv("SKILLS_DIR", "skills"),
		RoleMDPath:              getEnv("ROLE_MD_PATH", "config/ROLE.md"),
		ToolMDPath:              getEnv("TOOL_MD_PATH", "config/TOOL.md"),
		UploadDir:               getEnv("UPLOAD_DIR", "uploads"),
		OutputDir:               getEnv("OUTPUT_DIR", "output"),
		LogLevel:                getEnv("LOG_LEVEL", "info"),
		LogDir:                  getEnv("LOG_DIR", ""),
		LogFile:                 getEnv("LOG_FILE", ""),
		LogDataMaxLen:           getEnvInt("LOG_DATA_MAX_LEN", 600),
		LLMProvider:             getEnv("LLM_PROVIDER", "openai"),
		LLMMaxTokens:            getEnvInt("LLM_MAX_TOKENS", 8096),
		LLMRateLimit:            getEnvInt("LLM_RPM", 0),
		MaxContextTokens:        getEnvInt("MAX_CONTEXT_TOKENS", 6000),
		CompressKeepRecent:      getEnvInt("COMPRESS_KEEP_RECENT", 10),
		FeishuEncryptKey:        getEnv("FEISHU_ENCRYPT_KEY", ""),
		FeishuAPIBase:           getEnv("FEISHU_API_BASE", "https://open.feishu.cn"),
		FeishuSpinnerIntervalMs: getEnvInt("FEISHU_SPINNER_INTERVAL_MS", 800),
		FeishuPendingTimeoutMin: getEnvInt("FEISHU_PENDING_TIMEOUT_MIN", 30),
		NanoBananaAPIKey:        getEnv("NANO_BANANA_API_KEY", ""),
		NanoBananaBaseURL:       getEnv("NANO_BANANA_BASE_URL", "http://llmapi.bilibili.co/v1"),
		NanoBananaModel:         getEnv("NANO_BANANA_MODEL", "ppio/nano-banana-pro"),
		AgentMaxIterations:        getEnvInt("AGENT_MAX_ITERATIONS", 20),
		ProgressLabel:             getEnv("PROGRESS_LABEL", "progress"),
		SelfImprovingMinToolIters: getEnvInt("SELF_IMPROVING_MIN_TOOL_ITERS", 3),
		SelfImprovingMaxSkills:      getEnvInt("SELF_IMPROVING_MAX_SKILLS", 20),
		SelfImprovingLFUDecayHours:  getEnvInt("SELF_IMPROVING_LFU_DECAY_HOURS", 24),
		SelfImprovingProtectMinutes: getEnvInt("SELF_IMPROVING_PROTECT_MINUTES", 60),
		BrowserServerPort:       getEnv("BROWSER_SERVER_PORT", "9222"),
		BrowserServerScript:     getEnv("BROWSER_SERVER_SCRIPT", "browser-server/server.js"),
		BrowserHeadless:         getEnv("BROWSER_HEADLESS", "true") != "false",
		ToolScriptTimeoutSec:    getEnvInt("TOOL_SCRIPT_TIMEOUT_SEC", 60),
		ToolExecTimeoutSec:      getEnvInt("TOOL_EXEC_TIMEOUT_SEC", 1800),
		ToolExecYieldMs:         getEnvInt("TOOL_EXEC_YIELD_MS", 10_000),
		ToolWebFetchTimeoutSec:  getEnvInt("TOOL_WEB_FETCH_TIMEOUT_SEC", 15),
		WeComWebhookTimeoutSec:  getEnvInt("WECOM_WEBHOOK_TIMEOUT_SEC", 10),
		DownloadTTLMin:          getEnvInt("DOWNLOAD_TTL_MIN", 30),
		ToolResultMaxDBBytes:    getEnvInt("TOOL_RESULT_MAX_DB_BYTES", 2000),
		ToolErrorSummaryLen:     getEnvInt("TOOL_ERROR_SUMMARY_LEN", 32),
		FsReadMaxBytes:          getEnvInt("FS_READ_MAX_BYTES", 512*1024),
		UploadMaxBytes:          getEnvInt("UPLOAD_MAX_BYTES", 20*1024*1024),
		ReadFileMaxBytes:        getEnvInt("READ_FILE_MAX_BYTES", 20*1024*1024),
		ReadImageMaxBytes:       getEnvInt("READ_IMAGE_MAX_BYTES", 5*1024*1024),
		MCPConfigPath:           getEnv("MCP_CONFIG_PATH", "config/mcp.json"),
		MemoryEnabled:          getEnvBool("MEMORY_ENABLED", true),
		MemoryFlushMinTurns:    getEnvInt("MEMORY_FLUSH_MIN_TURNS", 6),
		MemoryNudgeInterval:    getEnvInt("MEMORY_NUDGE_INTERVAL", 10),
		MemoryNotesCharLimit:   getEnvInt("MEMORY_NOTES_CHAR_LIMIT", 2200),
		MemoryPersonaCharLimit: getEnvInt("MEMORY_PERSONA_CHAR_LIMIT", 1375),
		SessionSearchEnabled:         getEnvBool("SESSION_SEARCH_ENABLED", true),
		SessionSearchSummaryMaxChars: getEnvInt("SESSION_SEARCH_SUMMARY_MAX_CHARS", 50000),
		HonchoEnabled:          getEnvBool("HONCHO_ENABLED", false),
		HonchoBaseURL:          getEnv("HONCHO_BASE_URL", "https://demo.honcho.dev"),
		HonchoAPIKey:           getEnv("HONCHO_API_KEY", ""),
		HonchoAppID:            getEnv("HONCHO_APP_ID", ""),
		HonchoAppName:          getEnv("HONCHO_APP_NAME", "ottclaw"),
	}
	cfg.LLMEndpoints = loadLLMEndpoints(cfg.LLMProvider, cfg.LLMMaxTokens, cfg.LLMRateLimit)
	return cfg
}

// DotEnv 返回从 .env 文件读取的所有 KV（只读副本）。
// 供需要向子进程注入 .env 配置的代码使用（如 MCP stdio subprocess）。
func DotEnv() map[string]string {
	result := make(map[string]string, len(dotEnvCache))
	for k, v := range dotEnvCache {
		result[k] = v
	}
	return result
}

// loadDotEnv 读取 .env 文件到 dotEnvCache，文件不存在时静默忽略
// 使用 godotenv.Read 只解析文件内容，不调用 os.Setenv，
// 保证系统环境变量始终具有最高优先级。
func loadDotEnv(path string) {
	m, err := godotenv.Read(path)
	if err != nil {
		// 文件不存在或无法读取：静默跳过，不影响启动
		dotEnvCache = map[string]string{}
		return
	}
	dotEnvCache = m
}

// getEnvBool 按优先级返回 bool 配置值。
// "false" / "0" 视为 false，其余非空值视为 true，空值使用默认值。
func getEnvBool(key string, defaultVal bool) bool {
	if s := getEnv(key, ""); s != "" {
		return s != "false" && s != "0"
	}
	return defaultVal
}

// getEnvInt 按优先级返回 int 配置值，解析失败时使用默认值
func getEnvInt(key string, defaultVal int) int {
	if s := getEnv(key, ""); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return defaultVal
}

// getEnv 按优先级返回配置值：
//  1. os.Getenv（系统环境变量）
//  2. dotEnvCache（.env 文件）
//  3. defaultVal（内置默认值）
func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := dotEnvCache[key]; ok && v != "" {
		return v
	}
	return defaultVal
}
