// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/tool/file_reader.go — 上传文件文本提取（.docx / .pdf / .pptx）
package tool

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"OTTClaw/config"
)


// readUploadedFile 读取上传文件的文本内容，path 为 upload API 返回的相对路径
// （如 "uploads/A/abc123.docx"）。
func readUploadedFile(path string) (string, error) {
	// 安全校验：路径必须在 uploads 目录内，防止目录穿越
	uploadsAbs, err := filepath.Abs(config.Cfg.UploadDir)
	if err != nil {
		return "", fmt.Errorf("resolve uploads dir: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(uploadsAbs, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path is outside uploads directory")
	}

	if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
		return "", fmt.Errorf("file not found: %s", path)
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".doc":
		return extractDoc(absPath)
	case ".docx":
		return extractDocx(absPath)
	case ".pdf":
		return extractPDF(absPath)
	case ".pptx":
		return extractPptx(absPath)
	case ".xlsx":
		return extractXlsx(absPath)
	default:
		return "", fmt.Errorf("unsupported format %q, supported: .doc / .docx / .pdf / .pptx / .xlsx", ext)
	}
}

// oleSignature OLE2 Compound Document 的文件头魔数（8 字节）
var oleSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// extractDoc 从旧版二进制 .doc 文件（OLE2 Compound Document）提取纯文本。
// 采用启发式扫描：优先识别 UTF-16LE 编码文本（含 CJK），回退到 ANSI 文本。
// 不依赖外部工具，文本提取质量接近实用水平，偶有少量乱码属正常现象。
func extractDoc(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read doc: %w", err)
	}
	if len(data) < 8 || !bytes.Equal(data[:8], oleSignature) {
		return "", fmt.Errorf("not a valid .doc file: OLE2 signature not found")
	}

	u16 := docScanUTF16(data)
	ansi := docScanAnsi(data)

	// 取字符数更多的结果
	result := u16
	if strings.Count(ansi, "") > strings.Count(u16, "") {
		result = ansi
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return "(从 .doc 文件未能提取到可读文本，建议在 Word 中另存为 .docx 后重试)", nil
	}

	if max := config.Cfg.ReadFileMaxBytes; max > 0 && len(result) > max {
		result = result[:max] + "\n...[内容已截断]"
	}
	return result, nil
}

// docScanUTF16 扫描 UTF-16LE 编码的文本序列（每字符 2 字节，低字节在前）。
// 支持 ASCII、CJK 及常见 Unicode 区间；连续 ≥5 个可打印字符才视为有效文本段。
func docScanUTF16(data []byte) string {
	const minRun = 5
	var sb strings.Builder
	var run []uint16

	flush := func() {
		if len(run) >= minRun {
			runes := utf16.Decode(run)
			sb.WriteString(string(runes))
			sb.WriteByte('\n')
		}
		run = run[:0]
	}

	for i := 0; i+1 < len(data); i += 2 {
		cp := uint16(data[i]) | uint16(data[i+1])<<8
		r := rune(cp)
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			run = append(run, cp)
		case r >= 0x0020 && r <= 0x007E: // ASCII printable
			run = append(run, cp)
		case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一汉字
			run = append(run, cp)
		case r >= 0x3000 && r <= 0x303F: // CJK 符号和标点
			run = append(run, cp)
		case r >= 0xFF00 && r <= 0xFFEF: // 全角字符
			run = append(run, cp)
		case r >= 0x0080 && r <= 0x00FF: // Latin-1 补充
			run = append(run, cp)
		case r >= 0x2000 && r <= 0x206F: // 通用标点
			run = append(run, cp)
		default:
			flush()
		}
	}
	flush()
	return sb.String()
}

// docScanAnsi 扫描 ANSI/ASCII 文本序列（单字节）。
// 连续 ≥8 个可打印字节才视为有效文本段，减少乱码干扰。
func docScanAnsi(data []byte) string {
	const minRun = 8
	var sb strings.Builder
	var run []byte

	for _, b := range data {
		switch {
		case b == '\t' || b == '\n' || b == '\r':
			run = append(run, b)
		case b >= 0x20 && b <= 0x7E: // ASCII printable
			run = append(run, b)
		default:
			if len(run) >= minRun {
				sb.Write(run)
				sb.WriteByte('\n')
			}
			run = run[:0]
		}
	}
	if len(run) >= minRun {
		sb.Write(run)
	}
	return sb.String()
}

// extractDocx 从 Word .docx 文件中提取纯文本。
// .docx 本质上是 ZIP，文本位于 word/document.xml 的 <w:t> 元素中。
func extractDocx(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("open document.xml: %w", err)
			}
			defer rc.Close()
			return xmlExtractText(rc)
		}
	}
	return "", fmt.Errorf("word/document.xml not found in docx")
}

// extractPptx 从 PowerPoint .pptx 文件中按幻灯片顺序提取文本。
// .pptx 本质上是 ZIP，每张幻灯片位于 ppt/slides/slideN.xml。
func extractPptx(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	var slideFiles []*zip.File
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles = append(slideFiles, f)
		}
	}
	sort.Slice(slideFiles, func(i, j int) bool { return slideFiles[i].Name < slideFiles[j].Name })

	var sb strings.Builder
	for i, f := range slideFiles {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		text, err := xmlExtractText(rc)
		rc.Close()
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		fmt.Fprintf(&sb, "--- 幻灯片 %d ---\n%s\n\n", i+1, text)
		if max := config.Cfg.ReadFileMaxBytes; max > 0 && sb.Len() >= max {
			sb.WriteString("...[内容已截断]")
			break
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// extractPDF 从 PDF 文件中提取纯文本，使用 pdftotext（Poppler）。
// pdftotext 支持几乎所有 PDF 变体，包括复杂 CJK 字体和多种流过滤器。
func extractPDF(path string) (string, error) {
	ptPath, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext not found: install poppler (brew install poppler)")
	}
	out, err := exec.Command(ptPath, "-enc", "UTF-8", path, "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	// 将换页符（pdftotext 页面分隔符）替换为段落空行
	text := strings.ReplaceAll(string(out), "\f", "\n\n")
	text = strings.TrimSpace(text)
	if max := config.Cfg.ReadFileMaxBytes; max > 0 && len(text) > max {
		text = text[:max] + "\n...[内容已截断]"
	}
	return text, nil
}

// ── Excel (.xlsx) ────────────────────────────────────────────────────────────

// extractXlsx 从 Excel .xlsx 文件中按工作表提取表格文本（Tab 分隔列）。
// .xlsx 本质上是 ZIP，表格数据位于 xl/worksheets/sheetN.xml。
func extractXlsx(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer r.Close()

	shared, err := xlsxReadSharedStrings(r)
	if err != nil {
		return "", fmt.Errorf("read shared strings: %w", err)
	}
	names := xlsxReadSheetNames(r)

	// 收集 worksheet 文件并按数字编号排序
	var sheetFiles []*zip.File
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	sort.Slice(sheetFiles, func(i, j int) bool {
		return xlsxSheetNum(sheetFiles[i].Name) < xlsxSheetNum(sheetFiles[j].Name)
	})

	var sb strings.Builder
	for idx, f := range sheetFiles {
		name := fmt.Sprintf("Sheet%d", idx+1)
		if idx < len(names) && names[idx] != "" {
			name = names[idx]
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		grid, err := xlsxParseSheet(rc, shared)
		rc.Close()
		if err != nil || len(grid) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "--- %s ---\n", name)
		for _, row := range grid {
			sb.WriteString(strings.Join(row, "\t"))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
		if max := config.Cfg.ReadFileMaxBytes; max > 0 && sb.Len() >= max {
			sb.WriteString("...[内容已截断]")
			break
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// xlsxReadSharedStrings 解析 xl/sharedStrings.xml，返回共享字符串切片。
// 大多数字符串值以索引形式存储在单元格中，指向这张表。
func xlsxReadSharedStrings(r *zip.ReadCloser) ([]string, error) {
	for _, f := range r.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		var result []string
		var cur strings.Builder
		var inSI, inT bool
		dec := xml.NewDecoder(rc)
		dec.Strict = false
		dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "si":
					inSI = true
					cur.Reset()
				case "t":
					if inSI {
						inT = true
					}
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "si":
					result = append(result, cur.String())
					inSI = false
					inT = false
				case "t":
					inT = false
				}
			case xml.CharData:
				if inT {
					cur.Write(t)
				}
			}
		}
		return result, nil
	}
	return nil, nil // 文件不存在时视为无共享字符串（纯数字表格）
}

// xlsxReadSheetNames 解析 xl/workbook.xml，按 <sheet> 出现顺序返回工作表名称。
func xlsxReadSheetNames(r *zip.ReadCloser) []string {
	for _, f := range r.File {
		if f.Name != "xl/workbook.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()

		var names []string
		dec := xml.NewDecoder(rc)
		dec.Strict = false
		dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
				for _, a := range se.Attr {
					if a.Name.Local == "name" {
						names = append(names, a.Value)
						break
					}
				}
			}
		}
		return names
	}
	return nil
}

// xlsxParseSheet 解析单张 worksheet XML，返回二维字符串网格（保留行列位置，空单元格填空串）。
func xlsxParseSheet(rc io.Reader, shared []string) ([][]string, error) {
	type cellVal struct{ row, col int; val string }
	var cells []cellVal
	maxRow, maxCol := 0, 0

	dec := xml.NewDecoder(rc)
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var curRow, curCol int
	var curType string
	var inV, inT bool
	var buf strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						n, _ := strconv.Atoi(a.Value)
						curRow = n - 1
					}
				}
			case "c":
				curType = ""
				buf.Reset()
				inV, inT = false, false
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						curRow, curCol = xlsxCellRef(a.Value)
					case "t":
						curType = a.Value
					}
				}
			case "v":
				inV = true
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				raw := buf.String()
				val := raw
				switch curType {
				case "s": // 共享字符串索引
					if idx, err := strconv.Atoi(raw); err == nil && idx >= 0 && idx < len(shared) {
						val = shared[idx]
					}
				case "b": // 布尔
					if raw == "1" {
						val = "TRUE"
					} else {
						val = "FALSE"
					}
				}
				if val != "" {
					cells = append(cells, cellVal{curRow, curCol, val})
					if curRow > maxRow {
						maxRow = curRow
					}
					if curCol > maxCol {
						maxCol = curCol
					}
				}
			}
		case xml.CharData:
			if inV || inT {
				buf.Write(t)
			}
		}
	}

	if len(cells) == 0 {
		return nil, nil
	}

	// 构建二维网格
	grid := make([][]string, maxRow+1)
	for i := range grid {
		grid[i] = make([]string, maxCol+1)
	}
	for _, c := range cells {
		grid[c.row][c.col] = c.val
	}
	// 去掉末尾全空行
	for len(grid) > 0 {
		empty := true
		for _, v := range grid[len(grid)-1] {
			if v != "" {
				empty = false
				break
			}
		}
		if empty {
			grid = grid[:len(grid)-1]
		} else {
			break
		}
	}
	return grid, nil
}

// xlsxCellRef 将单元格引用（如 "A1"、"BC12"）解析为 (row, col)，均为 0-indexed。
func xlsxCellRef(ref string) (row, col int) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
	}
	col-- // 转 0-indexed
	n, _ := strconv.Atoi(ref[i:])
	row = n - 1
	return
}

// xlsxSheetNum 从路径（如 "xl/worksheets/sheet3.xml"）提取数字编号，用于排序。
func xlsxSheetNum(name string) int {
	base := strings.TrimSuffix(filepath.Base(name), ".xml")
	base = strings.TrimPrefix(base, "sheet")
	n, _ := strconv.Atoi(base)
	return n
}

// xmlExtractText 解析 XML 流，收集所有 <t> 元素内的文本，遇到 <p> / <br> 时换行。
// 适用于 DOCX（w:t / w:p）和 PPTX（a:t / a:p），local name 相同。
func xmlExtractText(r io.Reader) (string, error) {
	var sb strings.Builder
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var inText bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break // io.EOF 或 XML 错误均停止，已收集的文本仍有效
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p", "br":
				// 段落/换行：补一个换行符（避免重复换行）
				if sb.Len() > 0 && sb.String()[sb.Len()-1] != '\n' {
					sb.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
				if max := config.Cfg.ReadFileMaxBytes; max > 0 && sb.Len() >= max {
					sb.WriteString("\n...[内容已截断]")
					return sb.String(), nil
				}
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
