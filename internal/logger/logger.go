// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/logger/logger.go — 文本日志，格式：
//
//	2026-03-15 10:23:45.123  INFO   [module]  user=xxx  sess=yyy  message  (file.go:42)
//	2026-03-15 10:23:45.456  ERROR  [module]  user=xxx  sess=yyy  message  err=<detail>  (file.go:88)  245ms
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 级别优先级常量（数值越大越严重）
const (
	lvlDebug = iota
	lvlInfo
	lvlWarn
	lvlError
)

var (
	mu         sync.Mutex
	output     io.Writer = os.Stdout
	minLevel   int       = lvlInfo // 默认 INFO，Init 时根据 LOG_LEVEL 更新
	maxDataLen int       = 600     // DebugData 截断阈值，0 表示不截断
)

// Init 初始化日志输出目标与最低级别。
// logDir 和 logFile 均不为空时，同时写入 stdout 和文件；否则只写 stdout。
func Init(logDir, logFile, level string, dataMaxLen int) error {
	mu.Lock()
	minLevel = parseLevelPriority(level)
	maxDataLen = dataMaxLen
	mu.Unlock()

	if logDir == "" || logFile == "" {
		return nil
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create log dir %q: %w", logDir, err)
	}
	p := filepath.Join(logDir, logFile)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file %q: %w", p, err)
	}
	mu.Lock()
	output = io.MultiWriter(os.Stdout, f)
	mu.Unlock()
	return nil
}

func parseLevelPriority(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return lvlDebug
	case "warn", "warning":
		return lvlWarn
	case "error":
		return lvlError
	default: // "info" 及其他
		return lvlInfo
	}
}

const maxFieldBytes = 1000

func truncate(s string) string {
	if len(s) <= maxFieldBytes {
		return s
	}
	return s[:maxFieldBytes] + fmt.Sprintf("…[+%d bytes]", len(s)-maxFieldBytes)
}

// projectRoot 是工程根目录的绝对路径（末尾含 /），在 init 时通过 logger.go 自身路径推算。
// runtime.Caller 返回的文件路径形如 /abs/path/OTTClaw/internal/logger/logger.go，
// 去掉已知后缀即得工程根。
var projectRoot string

func init() {
	_, file, _, ok := runtime.Caller(0)
	if ok {
		const suffix = "internal/logger/logger.go"
		if idx := strings.LastIndex(file, suffix); idx >= 0 {
			projectRoot = file[:idx] // 保留末尾的 /
		}
	}
}

// fileRef 把绝对路径 + 行号格式化为相对工程根的 "path/to/file.go:行号"
func fileRef(file string, line int) string {
	if projectRoot != "" {
		file = strings.TrimPrefix(file, projectRoot)
	}
	return fmt.Sprintf("%s:%d", file, line)
}

func levelTag(priority int) string {
	switch priority {
	case lvlDebug:
		return "DEBUG"
	case lvlInfo:
		return "INFO "
	case lvlWarn:
		return "WARN "
	case lvlError:
		return "ERROR"
	default:
		return "INFO "
	}
}

func fmtCost(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d >= time.Second {
		return fmt.Sprintf("  %.2fs", d.Seconds())
	}
	return fmt.Sprintf("  %dms", d.Milliseconds())
}

// write 格式化并输出一条日志（线程安全）
func write(priority int, module, userID, sessionID, msg, errStr string, cost time.Duration, data []byte, caller string) {
	mu.Lock()
	ml := minLevel
	mu.Unlock()
	if priority < ml {
		return
	}

	msg = truncate(msg)
	if errStr != "" {
		errStr = truncate(errStr)
	}

	ts := time.Now().Format("2006-01-02 15:04:05.000")

	var b strings.Builder
	b.WriteString(ts)
	b.WriteString("  ")
	b.WriteString(levelTag(priority))
	b.WriteString("  ")
	b.WriteString(caller)
	b.WriteString("  [")
	b.WriteString(module)
	b.WriteString("]")
	if userID != "" {
		b.WriteString("  user=")
		b.WriteString(userID)
	}
	if sessionID != "" {
		b.WriteString("  sess=")
		b.WriteString(sessionID)
	}
	b.WriteString("  ")
	b.WriteString(msg)
	if errStr != "" {
		b.WriteString("  err=")
		b.WriteString(errStr)
	}
	if len(data) > 0 {
		mu.Lock()
		limit := maxDataLen
		mu.Unlock()
		var ds string
		if limit > 0 && len(data) > limit {
			half := limit / 2
			ds = string(data[:half]) + fmt.Sprintf("…[+%d bytes]…", len(data)-limit) + string(data[len(data)-half:])
		} else {
			ds = string(data)
		}
		b.WriteString("  data=")
		b.WriteString(ds)
	}
	b.WriteString(fmtCost(cost))
	b.WriteByte('\n')

	mu.Lock()
	output.Write([]byte(b.String())) //nolint:errcheck
	mu.Unlock()
}

// Info 记录 INFO 级别日志
//
//go:noinline
func Info(module, userID, sessionID, msg string, cost time.Duration) {
	_, f, l, _ := runtime.Caller(1)
	write(lvlInfo, module, userID, sessionID, msg, "", cost, nil, fileRef(f, l))
}

// Warn 记录 WARN 级别日志
//
//go:noinline
func Warn(module, userID, sessionID, msg string, cost time.Duration) {
	_, f, l, _ := runtime.Caller(1)
	write(lvlWarn, module, userID, sessionID, msg, "", cost, nil, fileRef(f, l))
}

// Error 记录 ERROR 级别日志（附带错误信息）
//
//go:noinline
func Error(module, userID, sessionID, msg string, err error, cost time.Duration) {
	_, f, l, _ := runtime.Caller(1)
	e := ""
	if err != nil {
		e = err.Error()
	}
	write(lvlError, module, userID, sessionID, msg, e, cost, nil, fileRef(f, l))
}

// Debug 记录 DEBUG 级别日志
//
//go:noinline
func Debug(module, userID, sessionID, msg string, cost time.Duration) {
	_, f, l, _ := runtime.Caller(1)
	write(lvlDebug, module, userID, sessionID, msg, "", cost, nil, fileRef(f, l))
}

// DebugData 记录 DEBUG 级别日志，附带大块结构化数据（如 LLM 完整输入/输出 JSON）。
// data 超过 maxDataLen 时自动截断首尾。
//
//go:noinline
func DebugData(module, userID, sessionID, msg string, data []byte, cost time.Duration) {
	_, f, l, _ := runtime.Caller(1)
	write(lvlDebug, module, userID, sessionID, msg, "", cost, data, fileRef(f, l))
}
