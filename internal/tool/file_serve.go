// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/file_serve.go — output_file(action=download) 实现
// 生成带 TTL 的一次性下载 token，前端通过 /download/:token 取文件。
package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"OTTClaw/config"
)

// serverBaseURL 缓存服务对外的 base URL（scheme://host），
// 由 HTTP 中间件在每次请求时写入，工具侧无需额外配置即可拼出完整链接。
var serverBaseURL atomic.Value

// SetServerBaseURL 供 HTTP 中间件调用，缓存服务的 base URL（如 "https://example.com"，不含末尾斜杠）。
func SetServerBaseURL(base string) { serverBaseURL.Store(base) }

// RegisterFileDownload 为已有文件生成临时下载 token，返回 downloadURL 和（图片在 output 目录时）webURL。
// 供 exec 自动文件检测逻辑调用，无需 LLM 主动调用 output_file。
func RegisterFileDownload(absPath string) (downloadURL, webURL string, err error) {
	startDLCleanup()

	info, statErr := os.Stat(absPath)
	if statErr != nil || info.IsDir() {
		return "", "", fmt.Errorf("file not found or is directory: %s", absPath)
	}

	token, tokenErr := newDLToken()
	if tokenErr != nil {
		return "", "", tokenErr
	}

	ttl := time.Duration(config.Cfg.DownloadTTLMin) * time.Minute
	dlMu.Lock()
	dlStore[token] = dlEntry{
		FilePath:  absPath,
		Filename:  filepath.Base(absPath),
		ExpiresAt: time.Now().Add(ttl),
	}
	dlMu.Unlock()

	base := ""
	if v := serverBaseURL.Load(); v != nil {
		base, _ = v.(string)
	}
	if base == "" {
		base = "http://localhost:" + config.Cfg.ServerPort
	}
	downloadURL = base + "/download/" + token

	// 图片且位于 output 目录下时，额外返回 webURL（供前端内联展示）
	if isImagePath(absPath) {
		outputAbs, err2 := filepath.Abs(config.Cfg.OutputDir)
		if err2 == nil && strings.HasPrefix(absPath, outputAbs) {
			rel := strings.TrimPrefix(absPath, outputAbs)
			rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
			webURL = "/" + config.Cfg.OutputDir + rel
		}
	}

	return downloadURL, webURL, nil
}

// isImagePath 判断路径是否为图片文件（按扩展名）
func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".avif":
		return true
	}
	return false
}

type dlEntry struct {
	FilePath  string
	Filename  string
	ExpiresAt time.Time
}

var (
	dlMu    sync.Mutex
	dlStore = map[string]dlEntry{}
	dlOnce  sync.Once
)

// startDLCleanup 启动后台定时清理过期 token（只初始化一次）
func startDLCleanup() {
	dlOnce.Do(func() {
		go func() {
			t := time.NewTicker(5 * time.Minute)
			defer t.Stop()
			for range t.C {
				now := time.Now()
				dlMu.Lock()
				for token, e := range dlStore {
					if now.After(e.ExpiresAt) {
						delete(dlStore, token)
					}
				}
				dlMu.Unlock()
			}
		}()
	})
}

// LookupDLToken 供 handler 包调用：校验 token 并返回文件路径和文件名。
// 过期或不存在时 ok=false。
func LookupDLToken(token string) (filePath, filename string, ok bool) {
	dlMu.Lock()
	defer dlMu.Unlock()
	e, exists := dlStore[token]
	if !exists {
		return "", "", false
	}
	if time.Now().After(e.ExpiresAt) {
		delete(dlStore, token)
		return "", "", false
	}
	return e.FilePath, e.Filename, true
}

func newDLToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleServeFileDownload 为服务器上的文件生成临时下载链接。
func handleServeFileDownload(_ context.Context, argsJSON string) (string, error) {
	startDLCleanup()

	var args struct {
		FilePath string `json:"file_path"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse output_file download args: %w", err)
	}
	if args.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	absPath, err := filepath.Abs(args.FilePath)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", args.FilePath)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file", args.FilePath)
	}

	filename := args.Filename
	if filename == "" {
		filename = filepath.Base(absPath)
	}

	token, err := newDLToken()
	if err != nil {
		return "", fmt.Errorf("generate download token: %w", err)
	}

	ttl := time.Duration(config.Cfg.DownloadTTLMin) * time.Minute
	dlMu.Lock()
	dlStore[token] = dlEntry{
		FilePath:  absPath,
		Filename:  filename,
		ExpiresAt: time.Now().Add(ttl),
	}
	dlMu.Unlock()

	base := ""
	if v := serverBaseURL.Load(); v != nil {
		base, _ = v.(string)
	}
	if base == "" {
		base = "http://localhost:" + config.Cfg.ServerPort
	}
	out := map[string]any{
		"download_url": base + "/download/" + token,
		"expires_in":   int(ttl.Seconds()),
	}

	// 若文件是图片且位于 output 目录下，额外返回 webUrl（直接访问路径）。
	// agent.go 的 extractWebURL 检测到 webUrl 后会自动调用 WriteImage，
	// 将图片推送到当前渠道（web 内联 / 飞书原生图片消息等）。
	if isImagePath(absPath) {
		outputAbs, err2 := filepath.Abs(config.Cfg.OutputDir)
		if err2 == nil && strings.HasPrefix(absPath, outputAbs) {
			rel := strings.TrimPrefix(absPath, outputAbs)
			rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
			out["webUrl"] = "/" + config.Cfg.OutputDir + rel
		}
	}

	result, _ := json.Marshal(out)
	return string(result), nil
}
