// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/read_pdf.go — 增强 PDF 分析工具
//
// 与 read_file 的区别：
//   - read_file：快速提取全文，适合文本型 PDF 的粗略阅读
//   - read_pdf ：按页结构化提取，支持页码选择，可渲染页面为图片（处理扫描件/图表）
package tool

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"OTTClaw/config"
	"OTTClaw/internal/logger"
)

const (
	pdfMaxTextPerPage = 8000  // 单页最大字符数
	pdfMaxTotalText   = 60000 // 总文本上限
)

// parsePDFPageRange 解析页码范围字符串，如 "1-5" "1,3,7-10"
// 返回排序去重后的页码列表（1-indexed），maxPage 为 PDF 总页数
func parsePDFPageRange(spec string, maxPage int) ([]int, error) {
	seen := map[int]bool{}
	parts := strings.Split(spec, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, "-"); idx >= 0 {
			startStr := strings.TrimSpace(part[:idx])
			endStr := strings.TrimSpace(part[idx+1:])
			start, err := strconv.Atoi(startStr)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q", startStr)
			}
			end, err := strconv.Atoi(endStr)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q", endStr)
			}
			if start < 1 {
				start = 1
			}
			if end > maxPage {
				end = maxPage
			}
			for i := start; i <= end; i++ {
				seen[i] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q", part)
			}
			if n >= 1 && n <= maxPage {
				seen[n] = true
			}
		}
	}
	pages := make([]int, 0, len(seen))
	for p := range seen {
		pages = append(pages, p)
	}
	sort.Ints(pages)
	return pages, nil
}

// renderPDFPageImages 尝试将 PDF 指定页渲染为图片，保存到 outputDir
// 返回 map[pageNum]imagePath；不可用或失败时返回空 map（不报错，降级为纯文本）
func renderPDFPageImages(pdfPath string, pages []int, outputDir string) map[int]string {
	result := make(map[int]string)

	// 1. 尝试 pdftoppm (poppler-utils)，最稳定
	if path, _ := exec.LookPath("pdftoppm"); path != "" {
		for _, p := range pages {
			outPrefix := filepath.Join(outputDir, fmt.Sprintf("page_%d", p))
			cmd := exec.Command("pdftoppm",
				"-png", "-r", "150", // 150 DPI
				"-f", strconv.Itoa(p), "-l", strconv.Itoa(p),
				pdfPath, outPrefix,
			)
			if err := cmd.Run(); err != nil {
				continue
			}
			// pdftoppm 输出命名：{prefix}-{pageNum}.png（pageNum 补零）
			candidates := []string{
				fmt.Sprintf("%s-%d.png", outPrefix, p),
				fmt.Sprintf("%s-%02d.png", outPrefix, p),
				fmt.Sprintf("%s-%03d.png", outPrefix, p),
				fmt.Sprintf("%s-1.png", outPrefix),
				fmt.Sprintf("%s-01.png", outPrefix),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					result[p] = c
					break
				}
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// 2. 尝试 magick (ImageMagick)，需要 Ghostscript
	if path, _ := exec.LookPath("magick"); path != "" {
		for _, p := range pages {
			outPath := filepath.Join(outputDir, fmt.Sprintf("page_%d.png", p))
			cmd := exec.Command("magick",
				"-density", "150",
				fmt.Sprintf("%s[%d]", pdfPath, p-1), // ImageMagick 页码 0-indexed
				"-background", "white", "-alpha", "remove",
				outPath,
			)
			if err := cmd.Run(); err != nil {
				continue
			}
			if _, err := os.Stat(outPath); err == nil {
				result[p] = outPath
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// 3. 尝试 qlmanage (macOS Quick Look)，仅能渲染第一页
	if path, _ := exec.LookPath("qlmanage"); path != "" && len(pages) > 0 {
		outPath := filepath.Join(outputDir, "page_preview.png")
		cmd := exec.Command("qlmanage", "-t", "-s", "1200", "-o", outputDir, pdfPath)
		if err := cmd.Run(); err == nil {
			// qlmanage 输出文件名：{原文件名}.png
			base := filepath.Base(pdfPath)
			qlOut := filepath.Join(outputDir, base+".png")
			if _, err := os.Stat(qlOut); err == nil {
				_ = os.Rename(qlOut, outPath)
				result[pages[0]] = outPath
			}
		}
	}

	return result
}

// pdfPageCount 使用 pdfinfo 读取 PDF 总页数（pdfinfo 与 pdftotext 同属 poppler-utils）。
func pdfPageCount(path string) (int, error) {
	out, err := exec.Command("pdfinfo", path).Output()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Pages:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				n, e := strconv.Atoi(strings.TrimSpace(parts[1]))
				if e == nil && n > 0 {
					return n, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("pdfinfo: could not parse page count")
}

func handleReadPDF(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Pages  string `json:"pages"`
		Render bool   `json:"render"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse read_pdf args: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("path is required")
	}

	// 安全校验：路径必须在 uploads/、output/ 或 /tmp 目录内
	absPath, err := filepath.Abs(args.Path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	uploadsAbs, _ := filepath.Abs(config.Cfg.UploadDir)
	outputAbs, _ := filepath.Abs(config.Cfg.OutputDir)
	tmpDir := os.TempDir()
	relUp, errUp := filepath.Rel(uploadsAbs, absPath)
	relOut, errOut := filepath.Rel(outputAbs, absPath)
	inUploads := errUp == nil && !strings.HasPrefix(relUp, "..")
	inOutput := errOut == nil && !strings.HasPrefix(relOut, "..")
	inTmp := absPath == tmpDir || strings.HasPrefix(absPath, tmpDir+string(os.PathSeparator))
	if !inUploads && !inOutput && !inTmp {
		return "", fmt.Errorf("path must be within uploads/, output/, or /tmp directory")
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", args.Path)
	}

	// 检查 pdftotext 可用性
	ptPath, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext not found: install poppler (brew install poppler)")
	}

	// 获取总页数
	totalPages, err := pdfPageCount(absPath)
	if err != nil {
		return "", fmt.Errorf("read pdf info: %w", err)
	}
	if totalPages == 0 {
		return "", fmt.Errorf("pdf has 0 pages")
	}

	// 确定要处理的页码
	var pageNums []int
	if args.Pages != "" {
		pageNums, err = parsePDFPageRange(args.Pages, totalPages)
		if err != nil {
			return "", fmt.Errorf("invalid pages parameter: %w", err)
		}
		if len(pageNums) == 0 {
			return "", fmt.Errorf("no valid pages in range %q (total pages: %d)", args.Pages, totalPages)
		}
	} else {
		for i := 1; i <= totalPages; i++ {
			pageNums = append(pageNums, i)
		}
	}

	// 一次调用 pdftotext 提取所需页码范围（-f minPage -l maxPage）
	minPage := pageNums[0]
	maxPage := pageNums[len(pageNums)-1]
	ptOut, ptErr := exec.Command(ptPath,
		"-enc", "UTF-8",
		"-f", strconv.Itoa(minPage),
		"-l", strconv.Itoa(maxPage),
		absPath, "-",
	).Output()

	// pdftotext 以换页符（\f）分隔页面；末尾的空条目去掉
	rawPageTexts := strings.Split(string(ptOut), "\f")
	for len(rawPageTexts) > 0 && strings.TrimSpace(rawPageTexts[len(rawPageTexts)-1]) == "" {
		rawPageTexts = rawPageTexts[:len(rawPageTexts)-1]
	}

	// 按页构建结果
	type pageResult struct {
		Page     int    `json:"page"`
		Text     string `json:"text"`
		Error    string `json:"error,omitempty"`    // 单页提取失败时填写，不混入 Text
		ImageURL string `json:"imageUrl,omitempty"` // render=true 时填写
	}

	var results []pageResult
	totalChars := 0
	truncatedGlobal := false

	for _, pn := range pageNums {
		if totalChars >= pdfMaxTotalText {
			truncatedGlobal = true
			break
		}
		if ptErr != nil {
			results = append(results, pageResult{Page: pn, Error: fmt.Sprintf("extraction failed: %v", ptErr)})
			continue
		}
		idx := pn - minPage
		text := ""
		if idx >= 0 && idx < len(rawPageTexts) {
			text = strings.TrimSpace(rawPageTexts[idx])
		}
		if len(text) > pdfMaxTextPerPage {
			text = text[:pdfMaxTextPerPage] + "\n...[页内容已截断]"
		}
		totalChars += len(text)
		results = append(results, pageResult{Page: pn, Text: text})
	}

	// 渲染页面为图片
	var renderNote string
	if args.Render {
		// 以 PDF 文件内容的 MD5 作为子目录，同一文件多次渲染复用同一目录
		md5hex := "unknown"
		if fh, err := os.Open(absPath); err == nil {
			h := md5.New()
			if _, err := io.Copy(h, fh); err == nil {
				md5hex = fmt.Sprintf("%x", h.Sum(nil))
			}
			fh.Close()
		}
		imgDir := filepath.Join(config.Cfg.OutputDir, "pdf-pages", md5hex)
		if err := os.MkdirAll(imgDir, 0o755); err != nil {
			renderNote = fmt.Sprintf("render failed: %v", err)
		} else {
			imageMap := renderPDFPageImages(absPath, pageNums, imgDir)
			if len(imageMap) == 0 {
				renderNote = "render unavailable: no supported renderer found (install poppler: brew install poppler)"
			} else {
				for i, pr := range results {
					if imgPath, ok := imageMap[pr.Page]; ok {
						// 转为相对于 output/ 的路径
						relPath, _ := filepath.Rel(config.Cfg.OutputDir, imgPath)
						results[i].ImageURL = "/output/" + relPath
					}
				}
				renderNote = fmt.Sprintf("rendered %d page(s) as images", len(imageMap))
			}
		}
		logger.Info("read_pdf", "", "", "render: "+renderNote, 0)
	}

	// 检测扫描件：全部页面文本为空
	emptyPages := 0
	for _, pr := range results {
		if pr.Text == "" || pr.Text == "(empty page)" {
			emptyPages++
		}
	}

	output := map[string]any{
		"totalPages":     totalPages,
		"extractedPages": len(results),
		"pages":          results,
	}
	if truncatedGlobal {
		output["truncated"] = true
	}
	if renderNote != "" {
		output["renderNote"] = renderNote
	}
	if emptyPages == len(results) && !args.Render {
		output["hint"] = "All pages have no extractable text. This may be a scanned PDF. " +
			"Try read_file with render=true to get page images, or use browser tool to open the PDF."
	}

	b, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
}
