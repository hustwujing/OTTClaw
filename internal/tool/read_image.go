// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/read_image.go — 按需读取图片，返回多模态 ContentPart 供 LLM 视觉分析
//
// 设计思路：
//
//	会话历史中图片始终保留为占位符 [文件: path]，不自动展开 base64。
//	LLM 需要查看图片时，通过 read_file 路由到此处，本轮 in-memory 注入 base64；
//	工具结果不写入 DB（DB 只存文字摘要），下轮对话自动消失，避免历史 token 累积。
package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	_ "golang.org/x/image/webp"
	"math"
	"os"
	"path/filepath"
	"strings"

	"OTTClaw/config"
	"OTTClaw/internal/llm"
	xdraw "golang.org/x/image/draw"
)

// imgMediaTypes 支持的图片扩展名 → MIME 类型映射
var imgMediaTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}


// ── PartsResult：工具多模态返回格式 ──────────────────────────────────────────
//
// 当工具需要返回图片（或其他多模态内容）时，将结果序列化为此 JSON 格式。
// agent 检测到此格式后：
//   - Parts      → 用于本轮 in-memory ChatMessage（LLM 可见图片）
//   - TextSummary → 用于写入 DB（保持历史为纯文本，token 不累积）

type partsResult struct {
	Parts  []llm.ContentPart `json:"__cp"`
	Text   string            `json:"__ts"`
	WebURL string            `json:"__url,omitempty"` // 供前端内联展示的 Web 路径
}

// NewPartsResult 将多模态内容打包为工具返回字符串，供 agent 解析。
// webURL 非空时前端可直接用该路径内联展示图片（如 /output/3/abc.png）。
func NewPartsResult(textSummary string, parts []llm.ContentPart, webURL string) string {
	b, _ := json.Marshal(partsResult{Parts: parts, Text: textSummary, WebURL: webURL})
	return string(b)
}

// DecodePartsResult 尝试解析多模态工具结果。
// 返回 (parts, textSummary, webURL, true) 表示解析成功；否则 ok=false。
// webURL 非空时表示该图片可通过此 Web 路径直接访问（供前端内联展示）。
func DecodePartsResult(s string) (parts []llm.ContentPart, textSummary, webURL string, ok bool) {
	if len(s) < 7 || s[:6] != `{"__cp` {
		return nil, "", "", false
	}
	var r partsResult
	if err := json.Unmarshal([]byte(s), &r); err != nil || len(r.Parts) == 0 {
		return nil, "", "", false
	}
	return r.Parts, r.Text, r.WebURL, true
}

// ── handleReadImage ───────────────────────────────────────────────────────────

func handleReadImage(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_image args: %w", err)
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	// 兼容 LLM 使用 URL 格式传路径（如 /uploads/A/abc.jpg → uploads/A/abc.jpg）
	path = strings.TrimPrefix(path, "/")

	// 安全校验：路径必须在项目目录内，且不能是敏感文件
	// 允许 uploads/（用户上传）和 output/（工具生成）等项目内任意图片
	if err := checkInProject(path); err != nil {
		return "", err
	}
	if err := checkSensitivePath(path); err != nil {
		return "", err
	}

	absPath, _ := filepath.Abs(path)

	// 类型校验
	mediaType, supported := imgMediaTypes[strings.ToLower(filepath.Ext(absPath))]
	if !supported {
		return "", fmt.Errorf("unsupported image format %q (supported: jpg/png/gif/webp/bmp)",
			filepath.Ext(absPath))
	}

	// 大小校验 & 自动缩放
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("image not found: %s (resolved to %s)", path, absPath)
	}

	var data []byte
	origKB := info.Size() / 1024
	shrunkNote := ""

	maxBytes := config.Cfg.ReadImageMaxBytes
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		// 自动缩放：按面积比例降采样，重新编码为 JPEG
		data, err = shrinkImage(absPath, info.Size(), maxBytes)
		if err != nil {
			return "", fmt.Errorf("image too large (%d KB, limit %d MB), auto-resize failed: %w",
				origKB, maxBytes/1024/1024, err)
		}
		mediaType = "image/jpeg"
		shrunkNote = fmt.Sprintf("（原 %d KB，已自动压缩至 %d KB）", origKB, len(data)/1024)
	} else {
		data, err = os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("read image: %w", err)
		}
	}

	name := filepath.Base(absPath)
	parts := []llm.ContentPart{
		{Type: "text", Text: fmt.Sprintf("图片文件：%s%s", name, shrunkNote)},
		{Type: "image", MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data)},
	}
	return NewPartsResult(
		fmt.Sprintf("[已读取图片 %s%s]", name, shrunkNote),
		parts,
		"/"+path, // Web 路径，供前端内联展示
	), nil
}

// shrinkImage 将超大图片按面积比例缩放，重编码为 JPEG 返回。
// 目标：压缩后字节数 ≤ maxBytes。
func shrinkImage(absPath string, origSize int64, maxBytes int) ([]byte, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	// 按面积比例算缩放系数，留 20% 余量
	ratio := math.Sqrt(float64(maxBytes) * 0.8 / float64(origSize))
	if ratio >= 1.0 {
		ratio = 0.8
	}
	bounds := src.Bounds()
	newW := int(math.Max(1, math.Round(float64(bounds.Dx())*ratio)))
	newH := int(math.Max(1, math.Round(float64(bounds.Dy())*ratio)))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)

	// 先尝试质量 85，超限则降至 60
	for _, quality := range []int{85, 60} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("jpeg encode: %w", err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("unable to shrink image below %d MB even at low quality", maxBytes/1024/1024)
}
