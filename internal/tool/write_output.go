// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/write_output.go — output_file 工具实现（action=write/download）
// write: 将内容写入 output/{MD5第二位字符}/ 目录，自动生成下载 token，一次调用返回 path + download_url。
// download: 为服务器已有文件生成临时下载 token（30 分钟有效）。
package tool

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"OTTClaw/config"
)

// handleWriteOutputFile 将文本内容写入分桶输出目录。
// 分桶规则：取内容 MD5 的第二位十六进制字符（大写）作为子目录名。
func handleWriteOutputFile(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse output_file write args: %w", err)
	}
	if strings.TrimSpace(args.Filename) == "" {
		return "", fmt.Errorf("filename is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	// 去掉路径分隔符，防止路径穿越
	safeName := filepath.Base(args.Filename)
	if safeName == "." || safeName == string(filepath.Separator) {
		return "", fmt.Errorf("invalid filename: %s", args.Filename)
	}

	// 按文件扩展名将内容转为对应格式字节（.docx 等生成二进制；其他格式原样写入）
	data, err := docFormatBytes(safeName, args.Content)
	if err != nil {
		return "", fmt.Errorf("generate %s: %w", filepath.Ext(safeName), err)
	}

	// MD5 第二位（index 1），大写
	sum := md5.Sum([]byte(args.Content))
	hexHash := fmt.Sprintf("%x", sum)
	bucketDir := strings.ToUpper(string(hexHash[1]))

	// 加 userID 子目录，避免多用户同名文件互相覆盖
	userID := userIDFromCtx(ctx)
	if userID == "" {
		userID = "_shared"
	}
	dir := filepath.Join(config.Cfg.OutputDir, userID, bucketDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output directory %s: %w", dir, err)
	}

	filePath := filepath.Join(dir, safeName)
	if err = os.WriteFile(filePath, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}

	result, _ := json.Marshal(map[string]any{
		"path":     absPath,
		"rel_path": filePath,
	})
	return string(result), nil
}

// handleOutputFile 是 output_file 工具的统一入口（合并自 write_output_file + serve_file_download）。
// action=write: 写文件后自动生成下载 token，返回 {path, rel_path, download_url, expires_in}。
// action=download: 为已有服务器文件生成临时下载 token，返回 {download_url, expires_in}。
func handleOutputFile(ctx context.Context, argsJSON string) (string, error) {
	var base struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &base); err != nil {
		return "", fmt.Errorf("parse output_file action: %w", err)
	}
	switch base.Action {
	case "write":
		// Step 1: 写文件
		writeResult, err := handleWriteOutputFile(ctx, argsJSON)
		if err != nil {
			return writeResult, err
		}
		// Step 2: 解析写入路径，自动生成下载 token
		var written struct {
			Path    string `json:"path"`
			RelPath string `json:"rel_path"`
		}
		if err := json.Unmarshal([]byte(writeResult), &written); err != nil {
			return writeResult, nil // 降级：仅返回写入结果
		}
		dlArgs, _ := json.Marshal(map[string]any{"action": "download", "file_path": written.Path})
		dlResult, err := handleServeFileDownload(ctx, string(dlArgs))
		if err != nil {
			return writeResult, nil // 降级：仅返回写入结果
		}
		var dl struct {
			DownloadURL string `json:"download_url"`
			ExpiresIn   int    `json:"expires_in"`
			WebURL      string `json:"webUrl"`
		}
		if err := json.Unmarshal([]byte(dlResult), &dl); err != nil {
			return writeResult, nil
		}
		out := map[string]any{
			"path":         written.Path,
			"rel_path":     written.RelPath,
			"download_url": dl.DownloadURL,
			"expires_in":   dl.ExpiresIn,
		}
		// 仅图片文件返回 webUrl（由 file_serve.go 的 isImagePath 判断后设置）。
		// 非图片文件不返回 webUrl，避免 agent.go 误推送（有 render action 后 LLM 不再需要自行 navigate）。
		if dl.WebURL != "" {
			out["webUrl"] = dl.WebURL
		}
		result, _ := json.Marshal(out)
		return string(result), nil
	case "download":
		return handleServeFileDownload(ctx, argsJSON)
	default:
		return "", fmt.Errorf("unknown output_file action: %q (valid: write/download)", base.Action)
	}
}
