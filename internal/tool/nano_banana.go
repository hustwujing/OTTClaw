// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/nano_banana.go — nano-banana 图像生成工具
// 支持文生图（txt2img）、图生图（img2img）、修图（edit）三种模式
// 底层调用 Bilibili LLM API（OpenAI 兼容），模型 ppio/nano-banana-pro
package tool

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"OTTClaw/config"
)

// nanoBananaRequest 请求体结构（OpenAI chat completions + image_config 扩展字段）
type nanoBananaRequest struct {
	Model       string               `json:"model"`
	Messages    []nanoBananaMessage  `json:"messages"`
	Stream      bool                 `json:"stream"`
	ImageConfig nanoBananaImgConfig  `json:"image_config"`
}

type nanoBananaMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"` // []nanoBananaTextPart | []nanoBananaImagePart 混合
}

type nanoBananaTextPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type nanoBananaImageURLRef struct {
	URL string `json:"url"`
}

type nanoBananaImagePart struct {
	Type     string                `json:"type"`      // "image_url"
	ImageURL nanoBananaImageURLRef `json:"image_url"`
}

type nanoBananaImgConfig struct {
	AspectRatio string `json:"aspect_ratio"`
	ImageSize   string `json:"image_size"`
}

// nanoBananaResponse 响应结构
type nanoBananaResponse struct {
	Choices []struct {
		Message struct {
			Images []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// handleNanoBanana 处理 nano_banana 工具调用
func handleNanoBanana(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Action      string   `json:"action"`       // txt2img | img2img | edit
		Prompt      string   `json:"prompt"`       // 提示词（必填）
		ImageURLs   []string `json:"image_urls"`   // 参考图片 URL 或本地路径（img2img/edit 必填）
		AspectRatio string   `json:"aspect_ratio"` // 宽高比，默认 16:9
		Size        string   `json:"size"`         // 图片尺寸，默认 2K
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse nano_banana args: %w", err)
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if args.Action == "" {
		args.Action = "txt2img"
	}
	if args.AspectRatio == "" {
		args.AspectRatio = "16:9"
	}
	if args.Size == "" {
		args.Size = "2K"
	}

	cfg := config.Cfg
	if cfg.NanoBananaAPIKey == "" {
		return "", fmt.Errorf("NANO_BANANA_API_KEY not configured")
	}

	// img2img / edit 需要参考图
	switch args.Action {
	case "img2img", "edit":
		if len(args.ImageURLs) == 0 {
			return "", fmt.Errorf("action %q requires at least one image_url", args.Action)
		}
	case "txt2img":
		// 无需图片
	default:
		return "", fmt.Errorf("unknown action %q, use txt2img | img2img | edit", args.Action)
	}

	// 构造 content 数组
	content := []any{
		nanoBananaTextPart{Type: "text", Text: args.Prompt},
	}
	for _, imgRef := range args.ImageURLs {
		resolved, err := resolveNanoBananaImage(imgRef)
		if err != nil {
			return "", fmt.Errorf("resolve image %q: %w", imgRef, err)
		}
		content = append(content, nanoBananaImagePart{
			Type:     "image_url",
			ImageURL: nanoBananaImageURLRef{URL: resolved},
		})
	}

	// 发起 API 请求
	imageURL, err := callNanoBananaAPI(cfg, content, args.AspectRatio, args.Size)
	if err != nil {
		return "", err
	}

	// 下载生成图片并保存到 output/
	localPath, webPath, err := downloadNanoBananaImage(imageURL, cfg.OutputDir)
	if err != nil {
		// 下载失败时降级：只返回错误信息，不回传 imageURL（可能是巨型 base64，会污染消息上下文）
		return "", fmt.Errorf("保存图片失败：%w", err)
	}

	b, _ := json.Marshal(map[string]any{
		"path":   localPath,
		"webUrl": "/" + filepath.ToSlash(webPath),
	})
	return string(b), nil
}

// callNanoBananaAPI 发起 OpenAI 兼容的图像生成请求
func callNanoBananaAPI(cfg *config.AppConfig, content []any, aspectRatio, size string) (string, error) {
	reqBody := nanoBananaRequest{
		Model: cfg.NanoBananaModel,
		Messages: []nanoBananaMessage{
			{Role: "user", Content: content},
		},
		Stream:      false,
		ImageConfig: nanoBananaImgConfig{AspectRatio: aspectRatio, ImageSize: size},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	apiURL := strings.TrimRight(cfg.NanoBananaBaseURL, "/") + "/chat/completions"
	log.Printf("[nano_banana] calling API: url=%s model=%s aspect_ratio=%s size=%s", apiURL, cfg.NanoBananaModel, aspectRatio, size)

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.NanoBananaAPIKey)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api call failed: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[nano_banana] API error: status=%d body=%s", resp.StatusCode, string(rawBody))
		return "", fmt.Errorf("api returned %d: %s", resp.StatusCode, string(rawBody))
	}

	var result nanoBananaResponse
	if err := json.Unmarshal(rawBody, &result); err != nil {
		log.Printf("[nano_banana] parse error: %v body=%s", err, string(rawBody))
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.Error != nil {
		log.Printf("[nano_banana] API returned error: code=%v msg=%s", result.Error.Code, result.Error.Message)
		return "", fmt.Errorf("api error: %v - %s", result.Error.Code, result.Error.Message)
	}
	if len(result.Choices) == 0 || len(result.Choices[0].Message.Images) == 0 {
		log.Printf("[nano_banana] no image in response: %s", string(rawBody))
		return "", fmt.Errorf("no image in response: %s", string(rawBody))
	}

	imageURL := result.Choices[0].Message.Images[0].ImageURL.URL
	log.Printf("[nano_banana] success: image_url=%s", imageURL)
	return imageURL, nil
}

// resolveNanaBananaImage 将本地路径转换为 base64 data URL；HTTP URL 直接透传
func resolveNanaBananaImage(ref string) (string, error) {
	return resolveNanoBananaImage(ref)
}

func resolveNanoBananaImage(ref string) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	// 本地文件 → base64 data URL
	data, err := os.ReadFile(ref)
	if err != nil {
		return "", fmt.Errorf("read local image: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(ref))
	mimeType := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".png": "image/png", ".gif": "image/gif",
		".webp": "image/webp",
	}[ext]
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)), nil
}

// downloadNanaBananaImage 下载远端图片，保存到 output/{bucket}/{filename}
// 返回 (本地绝对路径, web 相对路径, error)
func downloadNanaBananaImage(imageURL, outputDir string) (string, string, error) {
	return downloadNanoBananaImage(imageURL, outputDir)
}

func downloadNanoBananaImage(imageURL, outputDir string) (string, string, error) {
	var imgData []byte
	var ext string

	if strings.HasPrefix(imageURL, "data:") {
		// API 直接返回 base64 data URL，解码后保存
		commaIdx := strings.Index(imageURL, ",")
		if commaIdx < 0 {
			return "", "", fmt.Errorf("invalid data URL: missing comma")
		}
		header := imageURL[:commaIdx]
		decoded, err := base64.StdEncoding.DecodeString(imageURL[commaIdx+1:])
		if err != nil {
			return "", "", fmt.Errorf("decode base64 image: %w", err)
		}
		imgData = decoded
		switch {
		case strings.Contains(header, "png"):
			ext = ".png"
		case strings.Contains(header, "webp"):
			ext = ".webp"
		case strings.Contains(header, "gif"):
			ext = ".gif"
		default:
			ext = ".jpg"
		}
	} else {
		// 普通 HTTP URL，下载（最多重试 3 次，间隔 1s/2s）
		client := &http.Client{Timeout: 2 * time.Minute}
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * time.Second)
				log.Printf("[nano_banana] retry download attempt=%d url=%s", attempt+1, imageURL)
			}
			resp, err := client.Get(imageURL)
			if err != nil {
				lastErr = fmt.Errorf("download image: %w", err)
				continue
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				lastErr = fmt.Errorf("read image data: %w", err)
				continue
			}
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("download image: HTTP %d", resp.StatusCode)
				continue
			}
			imgData = data
			ct := resp.Header.Get("Content-Type")
			switch {
			case strings.Contains(ct, "png"):
				ext = ".png"
			case strings.Contains(ct, "webp"):
				ext = ".webp"
			case strings.Contains(ct, "gif"):
				ext = ".gif"
			default:
				ext = ".jpg"
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return "", "", lastErr
		}
	}

	// 按 MD5 第二位分桶，与 write_output_file 保持一致
	sum := md5.Sum(imgData)
	hexHash := fmt.Sprintf("%x", sum)
	bucketDir := strings.ToUpper(string(hexHash[1]))

	filename := fmt.Sprintf("nb_%d%s", time.Now().UnixMilli(), ext)
	dir := filepath.Join(outputDir, bucketDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create output dir: %w", err)
	}

	absPath := filepath.Join(dir, filename)
	if err := os.WriteFile(absPath, imgData, 0o644); err != nil {
		return "", "", fmt.Errorf("write image file: %w", err)
	}

	webPath := filepath.Join(outputDir, bucketDir, filename)
	return absPath, webPath, nil
}
