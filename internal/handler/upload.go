// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/handler/upload.go — POST /api/upload
// 接收文件上传，按 MD5 第二位字符（大写）分目录存储：
//   uploads/{MD5[1]}/{MD5}{ext}
package handler

import (
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/middleware"
	"OTTClaw/internal/storage"
)

// UploadResponse 上传成功响应
type UploadResponse struct {
	Filename string `json:"filename"` // 存储文件名：{md5}{ext}
	Dir      string `json:"dir"`      // 所在子目录字符，如 "4" / "A"
	Path     string `json:"path"`     // 完整相对路径：uploads/{dir}/{filename}
	Size     int64  `json:"size"`     // 文件字节数
	MD5      string `json:"md5"`      // 文件 MD5（完整 32 位十六进制）
}

// Upload 处理 POST /api/upload
func Upload(c *gin.Context) {
	start := time.Now()
	userID := middleware.GetUserID(c)
	sessionID := c.PostForm("session_id") // 可选，前端传当前会话 ID

	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field"})
		return
	}

	if max := config.Cfg.UploadMaxBytes; max > 0 && fh.Size > int64(max) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("file too large (%d MB, limit %d MB)", fh.Size/1024/1024, max/1024/1024),
		})
		return
	}

	// 图片文件额外限制：与 read_image 的处理上限保持一致（READ_IMAGE_MAX_BYTES）
	imageExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true,
		".gif": true, ".webp": true, ".bmp": true,
	}
	if imageExts[strings.ToLower(filepath.Ext(fh.Filename))] {
		if max := config.Cfg.ReadImageMaxBytes; max > 0 && fh.Size > int64(max) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("image too large (%d MB, limit %d MB)", fh.Size/1024/1024, max/1024/1024),
			})
			return
		}
	}

	src, err := fh.Open()
	if err != nil {
		logger.Error("upload", userID, "", "open multipart file", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file failed"})
		return
	}
	defer src.Close()

	// 计算 MD5
	h := md5.New()
	if _, err = io.Copy(h, src); err != nil {
		logger.Error("upload", userID, "", "calc md5", err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file failed"})
		return
	}
	md5hex := fmt.Sprintf("%x", h.Sum(nil)) // 32 位小写十六进制

	// 取第二位字符（index 1），转大写作为子目录名
	dirChar := strings.ToUpper(string([]rune(md5hex)[1]))
	if !isValidDirChar(rune(dirChar[0])) {
		// MD5 只含 [0-9a-f]，转大写后必为 [0-9A-F]，此分支理论上不可达
		dirChar = "0"
	}

	// 构造存储路径：uploads/{dirChar}/{md5}{ext}
	ext := filepath.Ext(fh.Filename)
	storeFilename := md5hex + ext
	subDir := filepath.Join(config.Cfg.UploadDir, dirChar)
	destPath := filepath.Join(subDir, storeFilename)

	// 确保目录存在
	if err = os.MkdirAll(subDir, 0o755); err != nil {
		logger.Error("upload", userID, "", "mkdir "+subDir, err, time.Since(start))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
		return
	}

	// 若文件已存在（相同 MD5 + ext）则直接复用，不重复写入
	if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
		// 回到文件头再写磁盘
		if _, err = src.Seek(0, io.SeekStart); err != nil {
			logger.Error("upload", userID, "", "seek file", err, time.Since(start))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
			return
		}
		dst, err := os.Create(destPath)
		if err != nil {
			logger.Error("upload", userID, "", "create file "+destPath, err, time.Since(start))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
			return
		}
		// 显式 Close 而非 defer：确保文件在 HTTP 响应发出前已完全写入并刷盘，
		// 避免客户端收到成功响应后立即读取时文件仍处于未关闭状态。
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		if copyErr != nil {
			logger.Error("upload", userID, "", "write file "+destPath, copyErr, time.Since(start))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
			return
		}
		if closeErr != nil {
			logger.Error("upload", userID, "", "close file "+destPath, closeErr, time.Since(start))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
			return
		}
	}

	info, _ := os.Stat(destPath)
	var size int64
	if info != nil {
		size = info.Size()
	}

	logger.Info("upload", userID, "", fmt.Sprintf("stored %s (%d bytes)", destPath, size), time.Since(start))

	// 写入用户可见历史：文件上传记录（content 为空，附件携带文件信息）
	webPath := "/" + filepath.ToSlash(destPath)
	attType := "file"
	if imageExts[strings.ToLower(ext)] {
		attType = "image"
	}
	go func() {
		_ = storage.AddOriginMessage(userID, sessionID, "user", "", []storage.Attachment{{
			Type:     attType,
			URL:      webPath,
			Filename: fh.Filename,
			Size:     size,
			MimeType: uploadMIMETypes[strings.ToLower(ext)],
		}})
	}()

	c.JSON(http.StatusOK, UploadResponse{
		Filename: storeFilename,
		Dir:      dirChar,
		Path:     filepath.ToSlash(destPath),
		Size:     size,
		MD5:      md5hex,
	})
}

// isValidDirChar 校验字符在 [0-9A-Z] 范围内
func isValidDirChar(r rune) bool {
	return unicode.IsDigit(r) || (r >= 'A' && r <= 'Z')
}

// uploadMIMETypes 常见扩展名 → MIME 类型，供写入附件记录时使用
var uploadMIMETypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp",
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".json": "application/json",
	".zip":  "application/zip",
}
