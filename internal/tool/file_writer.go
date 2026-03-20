// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/file_writer.go — Office 文档生成（.docx / .xlsx / .pdf）
// .docx / .xlsx 均为 ZIP+XML（OOXML），无需外部依赖。
// .pdf 通过 browser-server /pdf 端点（Playwright）渲染 HTML → PDF。
// 入口：docFormatBytes(filename, markdownContent) ([]byte, error)
package tool

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"OTTClaw/internal/browser"
)

// docFormatBytes 根据文件扩展名将内容字符串转换为文件字节：
// .docx → Markdown 转 OOXML Word 文档
// 其他扩展名 → 原样返回 []byte(content)
func docFormatBytes(filename, content string) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".doc":
		return writeDoc(content)
	case ".docx":
		return writeDocx(content)
	case ".xlsx":
		return writeXlsx(content)
	case ".pptx":
		return writePptx(content)
	case ".pdf":
		return writePDF(content)
	default:
		return []byte(content), nil
	}
}

// ── PDF ───────────────────────────────────────────────────────────────────────

// writePDF 将 Markdown 文本转换为 .pdf 字节数组。
// 通过 browser-server /pdf 端点（Playwright page.pdf()）渲染 HTML → PDF，
// 浏览器内置 CJK 字体，无需额外字体文件。
func writePDF(markdown string) ([]byte, error) {
	html := markdownToHTML(markdown)
	baseURL := browser.Default.BaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("browser-server not configured or not running")
	}

	body, _ := json.Marshal(map[string]string{"html": html})
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(baseURL+"/pdf", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pdf: call browser-server: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pdf: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("pdf: browser-server: %s", e.Error)
		}
		return nil, fmt.Errorf("pdf: browser-server HTTP %d", resp.StatusCode)
	}

	var result struct {
		PDF string `json:"pdf"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("pdf: decode response: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(result.PDF)
	if err != nil {
		return nil, fmt.Errorf("pdf: decode base64: %w", err)
	}
	return data, nil
}

// markdownToHTML 将 Markdown 转换为带样式的 HTML，供 Playwright 渲染为 PDF。
// 支持标题、粗体、斜体、代码、列表、引用、分割线、代码块。
func markdownToHTML(markdown string) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8">`)
	sb.WriteString(`<style>
body { font-family: "PingFang SC","Microsoft YaHei",Arial,sans-serif; font-size:14px; line-height:1.8; color:#222; margin:0; padding:0; }
h1 { font-size:2em; margin:0.8em 0 0.4em; border-bottom:2px solid #ddd; padding-bottom:4px; }
h2 { font-size:1.5em; margin:0.7em 0 0.3em; border-bottom:1px solid #eee; padding-bottom:3px; }
h3 { font-size:1.2em; margin:0.6em 0 0.3em; }
h4,h5,h6 { font-size:1em; font-weight:bold; margin:0.5em 0 0.2em; }
p  { margin:0.4em 0; }
ul,ol { padding-left:1.8em; margin:0.4em 0; }
li { margin:0.2em 0; }
blockquote { border-left:4px solid #ccc; margin:0.5em 0; padding:0.2em 1em; color:#555; background:#f9f9f9; }
pre { background:#f4f4f4; border:1px solid #ddd; border-radius:4px; padding:0.8em 1em; overflow-x:auto; font-size:12px; }
code { font-family:"Courier New",Courier,monospace; background:#f4f4f4; padding:0 3px; border-radius:3px; font-size:12px; }
pre code { background:none; padding:0; }
hr { border:none; border-top:1px solid #ccc; margin:1em 0; }
</style></head><body>`)

	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	inCode := false
	inList := false  // unordered
	inOList := false // ordered
	inBlockquote := false

	flushList := func() {
		if inList {
			sb.WriteString("</ul>")
			inList = false
		}
		if inOList {
			sb.WriteString("</ol>")
			inOList = false
		}
	}
	flushBQ := func() {
		if inBlockquote {
			sb.WriteString("</blockquote>")
			inBlockquote = false
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			flushList()
			flushBQ()
			if !inCode {
				sb.WriteString("<pre><code>")
				inCode = true
			} else {
				sb.WriteString("</code></pre>")
				inCode = false
			}
			continue
		}
		if inCode {
			sb.WriteString(htmlEsc(line) + "\n")
			continue
		}

		// Heading
		if lvl, text := docxHeadingLevel(line); lvl > 0 {
			flushList()
			flushBQ()
			tag := fmt.Sprintf("h%d", lvl)
			sb.WriteString(fmt.Sprintf("<%s>%s</%s>", tag, htmlInline(text), tag))
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Blockquote
		if strings.HasPrefix(line, "> ") {
			flushList()
			if !inBlockquote {
				sb.WriteString("<blockquote>")
				inBlockquote = true
			}
			sb.WriteString("<p>" + htmlInline(line[2:]) + "</p>")
			continue
		}
		flushBQ()

		// HR
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			flushList()
			sb.WriteString("<hr>")
			continue
		}

		// Blank line
		if trimmed == "" {
			flushList()
			sb.WriteString("<p></p>")
			continue
		}

		// Unordered list
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			if inOList {
				sb.WriteString("</ol>")
				inOList = false
			}
			if !inList {
				sb.WriteString("<ul>")
				inList = true
			}
			sb.WriteString("<li>" + htmlInline(line[2:]) + "</li>")
			continue
		}

		// Ordered list
		if num, text, ok := docxParseNumbered(line); ok {
			if inList {
				sb.WriteString("</ul>")
				inList = false
			}
			if !inOList {
				sb.WriteString("<ol>")
				inOList = true
			}
			_ = num
			sb.WriteString("<li>" + htmlInline(text) + "</li>")
			continue
		}

		// Paragraph
		flushList()
		sb.WriteString("<p>" + htmlInline(line) + "</p>")
	}

	flushList()
	flushBQ()
	if inCode {
		sb.WriteString("</code></pre>")
	}

	sb.WriteString("</body></html>")
	return sb.String()
}

// htmlEsc 转义 HTML 特殊字符（用于代码块纯文本）。
func htmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// htmlInline 处理行内 Markdown（**粗体** *斜体* `代码`）并转义 HTML。
func htmlInline(text string) string {
	var sb strings.Builder
	t := text
	for len(t) > 0 {
		switch {
		case t[0] == '`':
			if end := strings.IndexByte(t[1:], '`'); end >= 0 {
				sb.WriteString("<code>" + htmlEsc(t[1:1+end]) + "</code>")
				t = t[2+end:]
			} else {
				sb.WriteString(htmlEsc(string(t[0])))
				t = t[1:]
			}
		case len(t) >= 3 && t[:3] == "***":
			sb.WriteString("<strong><em>")
			t = t[3:]
			if end := strings.Index(t, "***"); end >= 0 {
				sb.WriteString(htmlEsc(t[:end]))
				sb.WriteString("</em></strong>")
				t = t[end+3:]
			}
		case len(t) >= 2 && t[:2] == "**":
			t = t[2:]
			if end := strings.Index(t, "**"); end >= 0 {
				sb.WriteString("<strong>" + htmlEsc(t[:end]) + "</strong>")
				t = t[end+2:]
			} else {
				sb.WriteString("**")
			}
		case t[0] == '*':
			t = t[1:]
			if end := strings.IndexByte(t, '*'); end >= 0 {
				sb.WriteString("<em>" + htmlEsc(t[:end]) + "</em>")
				t = t[end+1:]
			} else {
				sb.WriteString("*")
			}
		default:
			sb.WriteString(htmlEsc(string(t[0])))
			t = t[1:]
		}
	}
	return sb.String()
}

// ── PPTX ─────────────────────────────────────────────────────────────────────

// pptxSlide 表示一张幻灯片。
type pptxSlide struct {
	title string
	items []pptxLine
}

// pptxLine 表示正文中的一行。
type pptxLine struct {
	text   string
	bullet bool // true = 无序项目符号，false = 普通段落
}

// writePptx 将 Markdown 文本转换为 .pptx 字节数组。
// 幻灯片分隔符：单独一行的 "---"。
// 每张幻灯片第一个标题（# ## ###）= 标题；其余行 = 正文。
// "- " / "* " 开头 = 项目符号；其他非空行 = 普通段落。
func writePptx(content string) ([]byte, error) {
	slides := pptxParseSlides(content)
	if len(slides) == 0 {
		slides = []pptxSlide{{title: "Slide 1"}}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	static := []struct{ name, body string }{
		{"[Content_Types].xml", pptxBuildContentTypes(len(slides))},
		{"_rels/.rels", pptxTopRels},
		{"ppt/presentation.xml", pptxBuildPresentation(len(slides))},
		{"ppt/_rels/presentation.xml.rels", pptxBuildPresentationRels(len(slides))},
		{"ppt/slideMasters/slideMaster1.xml", pptxSlideMaster},
		{"ppt/slideMasters/_rels/slideMaster1.xml.rels", pptxSlideMasterRels},
		{"ppt/slideLayouts/slideLayout1.xml", pptxSlideLayout},
		{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", pptxSlideLayoutRels},
		{"ppt/theme/theme1.xml", pptxTheme},
	}
	for _, e := range static {
		f, err := zw.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("pptx: create %s: %w", e.name, err)
		}
		if _, err = f.Write([]byte(e.body)); err != nil {
			return nil, fmt.Errorf("pptx: write %s: %w", e.name, err)
		}
	}
	for i, sl := range slides {
		for _, e := range []struct{ name, body string }{
			{fmt.Sprintf("ppt/slides/slide%d.xml", i+1), pptxBuildSlide(sl)},
			{fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), pptxSlideRels},
		} {
			f, err := zw.Create(e.name)
			if err != nil {
				return nil, fmt.Errorf("pptx: create %s: %w", e.name, err)
			}
			if _, err = f.Write([]byte(e.body)); err != nil {
				return nil, fmt.Errorf("pptx: write %s: %w", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("pptx: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// pptxParseSlides 以单独一行的 "---" 为分隔符将内容切分为幻灯片列表。
func pptxParseSlides(content string) []pptxSlide {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var chunks [][]string
	cur := []string{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			chunks = append(chunks, cur)
			cur = []string{}
		} else {
			cur = append(cur, line)
		}
	}
	chunks = append(chunks, cur)

	var slides []pptxSlide
	for _, chunk := range chunks {
		sl := pptxParseSlide(chunk)
		// 过滤掉完全空白的幻灯片
		if sl.title == "" && len(sl.items) == 0 {
			continue
		}
		slides = append(slides, sl)
	}
	return slides
}

// pptxParseSlide 解析单张幻灯片的行列表：提取标题和正文。
func pptxParseSlide(lines []string) pptxSlide {
	var sl pptxSlide
	titleDone := false
	for _, line := range lines {
		if !titleDone {
			if lvl, text := docxHeadingLevel(line); lvl > 0 {
				sl.title = text
				titleDone = true
				continue
			}
			if t := strings.TrimSpace(line); t != "" {
				sl.title = t
				titleDone = true
				continue
			}
			continue // 跳过标题前的空行
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 正文内的小标题 → 加粗普通段落（前缀 **…** 处理留给 pptxRuns）
		if _, text := docxHeadingLevel(line); text != "" {
			sl.items = append(sl.items, pptxLine{text: "**" + text + "**", bullet: false})
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			sl.items = append(sl.items, pptxLine{text: line[2:], bullet: true})
		} else if num, text, ok := docxParseNumbered(line); ok {
			sl.items = append(sl.items, pptxLine{text: num + ". " + text, bullet: true})
		} else {
			sl.items = append(sl.items, pptxLine{text: line, bullet: false})
		}
	}
	return sl
}

// pptxBuildSlide 生成单张幻灯片的 XML。
func pptxBuildSlide(sl pptxSlide) string {
	const ns = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<p:sld ` + ns + `><p:cSld><p:spTree>`)
	sb.WriteString(`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`)
	sb.WriteString(`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>`)
	sb.WriteString(`<a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)

	// 标题占位符
	sb.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/>`)
	sb.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`)
	sb.WriteString(`<p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr/>`)
	sb.WriteString(`<p:txBody><a:bodyPr/><a:lstStyle/>`)
	sb.WriteString(`<a:p>` + pptxRuns(sl.title) + `</a:p>`)
	sb.WriteString(`</p:txBody></p:sp>`)

	// 正文占位符
	sb.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content Placeholder 2"/>`)
	sb.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`)
	sb.WriteString(`<p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>`)
	sb.WriteString(`<p:txBody><a:bodyPr/><a:lstStyle/>`)
	for _, item := range sl.items {
		sb.WriteString(`<a:p>`)
		if item.bullet {
			sb.WriteString(`<a:pPr><a:buChar char="•"/></a:pPr>`)
		} else {
			sb.WriteString(`<a:pPr><a:buNone/></a:pPr>`)
		}
		sb.WriteString(pptxRuns(item.text))
		sb.WriteString(`</a:p>`)
	}
	if len(sl.items) == 0 {
		sb.WriteString(`<a:p/>`)
	}
	sb.WriteString(`</p:txBody></p:sp>`)
	sb.WriteString(`</p:spTree></p:cSld></p:sld>`)
	return sb.String()
}

// pptxRuns 解析行内 **粗体** *斜体* 并生成 <a:r> 序列（DrawingML 文本片段）。
func pptxRuns(text string) string {
	type span struct {
		s    string
		b, i bool
	}
	var spans []span
	bold, italic := false, false
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			spans = append(spans, span{buf.String(), bold, italic})
			buf.Reset()
		}
	}
	t := text
	for len(t) > 0 {
		switch {
		case len(t) >= 3 && t[:3] == "***":
			flush(); bold = !bold; italic = !italic; t = t[3:]
		case len(t) >= 2 && t[:2] == "**":
			flush(); bold = !bold; t = t[2:]
		case t[0] == '*':
			flush(); italic = !italic; t = t[1:]
		default:
			buf.WriteByte(t[0]); t = t[1:]
		}
	}
	flush()

	var sb strings.Builder
	for _, sp := range spans {
		sb.WriteString(`<a:r><a:rPr lang="zh-CN" altLang="en-US"`)
		if sp.b {
			sb.WriteString(` b="1"`)
		}
		if sp.i {
			sb.WriteString(` i="1"`)
		}
		sb.WriteString(`/><a:t>` + docxEsc(sp.s) + `</a:t></a:r>`)
	}
	return sb.String()
}

// pptxBuildContentTypes 动态生成 Content_Types.xml。
func pptxBuildContentTypes(n int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	sb.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	sb.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	sb.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	sb.WriteString(`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`)
	sb.WriteString(`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`)
	sb.WriteString(`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`)
	for i := 1; i <= n; i++ {
		sb.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i))
	}
	sb.WriteString(`</Types>`)
	return sb.String()
}

// pptxBuildPresentation 生成 ppt/presentation.xml。
func pptxBuildPresentation(n int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" `)
	sb.WriteString(`xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" `)
	sb.WriteString(`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	sb.WriteString(`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	sb.WriteString(`<p:sldIdLst>`)
	for i := 1; i <= n; i++ {
		sb.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+i, 1+i))
	}
	sb.WriteString(`</p:sldIdLst>`)
	sb.WriteString(`<p:sldSz cx="9144000" cy="6858000"/>`)
	sb.WriteString(`<p:notesSz cx="6858000" cy="9144000"/>`)
	sb.WriteString(`</p:presentation>`)
	return sb.String()
}

// pptxBuildPresentationRels 生成 ppt/_rels/presentation.xml.rels。
func pptxBuildPresentationRels(n int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	sb.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i := 1; i <= n; i++ {
		sb.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i+1, i))
	}
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

// ── PPTX 静态模板 ─────────────────────────────────────────────────────────────

const pptxTopRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
</Relationships>`

const pptxSlideRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
</Relationships>`

const pptxSlideMasterRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>
</Relationships>`

const pptxSlideLayoutRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>
</Relationships>`

const pptxSlideMaster = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
             xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
             xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg>
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>
    </p:spTree>
  </p:cSld>
  <p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>
  <p:sldLayoutIdLst>
    <p:sldLayoutId id="2147483649" r:id="rId1"/>
  </p:sldLayoutIdLst>
  <p:txStyles>
    <p:titleStyle>
      <a:lvl1pPr algn="ctr"><a:defRPr lang="zh-CN" b="1"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr>
    </p:titleStyle>
    <p:bodyStyle>
      <a:lvl1pPr><a:defRPr lang="zh-CN"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr>
    </p:bodyStyle>
    <p:otherStyle>
      <a:lvl1pPr><a:defRPr lang="zh-CN"/></a:lvl1pPr>
    </p:otherStyle>
  </p:txStyles>
</p:sldMaster>`

const pptxSlideLayout = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
             xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
             xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
             type="obj">
  <p:cSld name="Title and Content">
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="2" name="Title 1"/>
          <p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>
          <p:nvPr><p:ph type="title"/></p:nvPr>
        </p:nvSpPr>
        <p:spPr><a:xfrm><a:off x="457200" y="274638"/><a:ext cx="8229600" cy="1143000"/></a:xfrm></p:spPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody>
      </p:sp>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="3" name="Content Placeholder 2"/>
          <p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>
          <p:nvPr><p:ph idx="1"/></p:nvPr>
        </p:nvSpPr>
        <p:spPr><a:xfrm><a:off x="457200" y="1600200"/><a:ext cx="8229600" cy="4525963"/></a:xfrm></p:spPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sldLayout>`

const pptxTheme = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme">
  <a:themeElements>
    <a:clrScheme name="Office">
      <a:dk1><a:sysClr lastClr="000000" val="windowText"/></a:dk1>
      <a:lt1><a:sysClr lastClr="FFFFFF" val="window"/></a:lt1>
      <a:dk2><a:srgbClr val="44546A"/></a:dk2>
      <a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>
      <a:accent1><a:srgbClr val="4472C4"/></a:accent1>
      <a:accent2><a:srgbClr val="ED7D31"/></a:accent2>
      <a:accent3><a:srgbClr val="A9D18E"/></a:accent3>
      <a:accent4><a:srgbClr val="FFC000"/></a:accent4>
      <a:accent5><a:srgbClr val="5B9BD5"/></a:accent5>
      <a:accent6><a:srgbClr val="70AD47"/></a:accent6>
      <a:hlink><a:srgbClr val="0563C1"/></a:hlink>
      <a:folHlink><a:srgbClr val="954F72"/></a:folHlink>
    </a:clrScheme>
    <a:fontScheme name="Office">
      <a:majorFont>
        <a:latin typeface="Calibri Light"/>
        <a:ea typeface=""/>
        <a:cs typeface=""/>
        <a:font script="Hans" typeface="Microsoft YaHei"/>
      </a:majorFont>
      <a:minorFont>
        <a:latin typeface="Calibri"/>
        <a:ea typeface=""/>
        <a:cs typeface=""/>
        <a:font script="Hans" typeface="Microsoft YaHei"/>
      </a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="Office">
      <a:fillStyleLst>
        <a:solidFill><a:schemeClr val="phClr"/></a:solidFill>
        <a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:tint val="67000"/></a:schemeClr></a:gs><a:gs pos="100000"><a:schemeClr val="phClr"><a:shade val="67000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill>
        <a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:tint val="94000"/></a:schemeClr></a:gs><a:gs pos="100000"><a:schemeClr val="phClr"><a:shade val="78000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill>
      </a:fillStyleLst>
      <a:lnStyleLst>
        <a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>
        <a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>
        <a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>
      </a:lnStyleLst>
      <a:effectStyleLst>
        <a:effectStyle><a:effectLst/></a:effectStyle>
        <a:effectStyle><a:effectLst/></a:effectStyle>
        <a:effectStyle><a:effectLst><a:outerShdw blurRad="57150" dist="19050" dir="5400000" algn="ctr" rotWithShape="0"><a:srgbClr val="000000"><a:alpha val="63000"/></a:srgbClr></a:outerShdw></a:effectLst></a:effectStyle>
      </a:effectStyleLst>
      <a:bgFillStyleLst>
        <a:solidFill><a:schemeClr val="phClr"/></a:solidFill>
        <a:solidFill><a:schemeClr val="phClr"><a:tint val="95000"/></a:schemeClr></a:solidFill>
        <a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"><a:tint val="93000"/></a:schemeClr></a:gs><a:gs pos="100000"><a:schemeClr val="phClr"><a:shade val="90000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="16200000" scaled="0"/></a:gradFill>
      </a:bgFillStyleLst>
    </a:fmtScheme>
  </a:themeElements>
</a:theme>`

// ── DOC（RTF）────────────────────────────────────────────────────────────────

// writeDoc 将 Markdown 文本转换为 RTF 格式的 .doc 字节数组。
// 旧版 OLE2 二进制 .doc 格式需要专用库，RTF 是纯文本替代方案：
// Word、WPS、LibreOffice 均可直接打开 RTF 内容的 .doc 文件。
// 支持：标题、**粗体**、*斜体*、`代码`、列表、引用、分割线、代码块。
func writeDoc(markdown string) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("{\\rtf1\\ansi\\ansicpg936\\deff0\\nouicompat\r\n")
	sb.WriteString("{\\fonttbl{\\f0\\fswiss\\fcharset134 Microsoft YaHei;}{\\f1\\fmodern\\fcharset0 Courier New;}}\r\n")
	sb.WriteString("{\\*\\generator OTTClaw}\\f0\\fs24\r\n")

	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	inCode := false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			sb.WriteString("\\pard\\li720\\f1\\fs20 ")
			sb.WriteString(rtfEsc(line))
			sb.WriteString("\\par\r\n")
			continue
		}

		if lvl, text := docxHeadingLevel(line); lvl > 0 {
			sizes := map[int]int{1: 40, 2: 32, 3: 28, 4: 26}
			sz := sizes[lvl]
			if sz == 0 {
				sz = 24
			}
			sb.WriteString(fmt.Sprintf("\\pard\\sb200\\sa80\\f0\\b\\fs%d ", sz))
			sb.WriteString(rtfInline(text))
			sb.WriteString("\\b0\\fs24\\par\r\n")
			continue
		}

		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			sb.WriteString("\\pard\\par\r\n")
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			// 段落底部边框模拟分割线
			sb.WriteString("\\pard\\brdrb\\brdrs\\brdrw6\\brsp20\\par\r\n")
		case strings.HasPrefix(line, "> "):
			sb.WriteString("\\pard\\li720\\f0\\fs24\\i ")
			sb.WriteString(rtfInline(line[2:]))
			sb.WriteString("\\i0\\par\r\n")
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			sb.WriteString("\\pard\\li720\\f0\\fs24 \\bullet  ")
			sb.WriteString(rtfInline(line[2:]))
			sb.WriteString("\\par\r\n")
		default:
			if num, text, ok := docxParseNumbered(line); ok {
				sb.WriteString("\\pard\\li720\\f0\\fs24 ")
				sb.WriteString(rtfEsc(num + ". "))
				sb.WriteString(rtfInline(text))
				sb.WriteString("\\par\r\n")
			} else {
				sb.WriteString("\\pard\\f0\\fs24 ")
				sb.WriteString(rtfInline(line))
				sb.WriteString("\\par\r\n")
			}
		}
	}

	sb.WriteString("}\r\n")
	return []byte(sb.String()), nil
}

// rtfEsc 转义 RTF 保留字符，并将非 ASCII 字符编码为 \uN? 形式的 Unicode 转义。
func rtfEsc(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString("\\\\")
		case '{':
			sb.WriteString("\\{")
		case '}':
			sb.WriteString("\\}")
		case '\n', '\r':
			sb.WriteString("\\line ")
		default:
			if r < 0x80 {
				sb.WriteRune(r)
			} else {
				// RTF Unicode 转义：有符号 16 位十进制，? 为降级回退字符
				sb.WriteString(fmt.Sprintf("\\u%d?", int16(r)))
			}
		}
	}
	return sb.String()
}

// rtfInline 解析行内 Markdown（**粗体** *斜体* `代码`），生成 RTF 控制字序列。
func rtfInline(text string) string {
	var sb strings.Builder
	bold, italic := false, false
	t := text
	for len(t) > 0 {
		switch {
		case len(t) >= 3 && t[:3] == "***":
			if bold && italic {
				sb.WriteString("\\b0\\i0 ")
				bold, italic = false, false
			} else {
				sb.WriteString("\\b\\i ")
				bold, italic = true, true
			}
			t = t[3:]
		case len(t) >= 2 && t[:2] == "**":
			if bold {
				sb.WriteString("\\b0 ")
				bold = false
			} else {
				sb.WriteString("\\b ")
				bold = true
			}
			t = t[2:]
		case t[0] == '*':
			if italic {
				sb.WriteString("\\i0 ")
				italic = false
			} else {
				sb.WriteString("\\i ")
				italic = true
			}
			t = t[1:]
		case t[0] == '`':
			if end := strings.IndexByte(t[1:], '`'); end >= 0 {
				sb.WriteString("\\f1 ")
				sb.WriteString(rtfEsc(t[1 : 1+end]))
				sb.WriteString("\\f0 ")
				t = t[2+end:]
			} else {
				sb.WriteString(rtfEsc(string(t[0])))
				t = t[1:]
			}
		default:
			sb.WriteString(rtfEsc(string(t[0])))
			t = t[1:]
		}
	}
	return sb.String()
}

// ── DOCX ─────────────────────────────────────────────────────────────────────

// writeDocx 将 Markdown 文本转换为 .docx 字节数组。
// 支持：H1-H4+、**粗体**、*斜体*、`代码`、- 无序列表、1. 有序列表、
// > 引用、--- 分割线、```代码块```。
func writeDocx(markdown string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	entries := []struct{ name, body string }{
		{"[Content_Types].xml", docxContentTypes},
		{"_rels/.rels", docxTopRels},
		{"word/_rels/document.xml.rels", docxDocRels},
		{"word/styles.xml", docxStyles},
		{"word/document.xml", docxBuildDocument(markdown)},
	}
	for _, e := range entries {
		f, err := zw.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("docx: create %s: %w", e.name, err)
		}
		if _, err = f.Write([]byte(e.body)); err != nil {
			return nil, fmt.Errorf("docx: write %s: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("docx: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// docxBuildDocument 将 Markdown 解析为 word/document.xml 内容。
func docxBuildDocument(markdown string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	sb.WriteString(`<w:body>`)

	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	inCode := false
	for _, line := range lines {
		// 代码块边界
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Code"/></w:pPr>`)
			sb.WriteString(docxSingleRun(line, false, false, true))
			sb.WriteString(`</w:p>`)
			continue
		}

		// 标题（支持任意层级，4+级统一用 Heading3）
		if lvl, text := docxHeadingLevel(line); lvl > 0 {
			styleID := map[int]string{1: "Heading1", 2: "Heading2", 3: "Heading3"}
			sid := styleID[lvl]
			if sid == "" {
				sid = "Heading3"
			}
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + sid + `"/></w:pPr>`)
			sb.WriteString(docxRuns(text))
			sb.WriteString(`</w:p>`)
			continue
		}

		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "> "): // 引用
			sb.WriteString(`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr>`)
			sb.WriteString(docxRuns(line[2:]))
			sb.WriteString(`</w:p>`)

		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "): // 无序列表
			sb.WriteString(`<w:p><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>`)
			sb.WriteString(`<w:r><w:t xml:space="preserve">• </w:t></w:r>`)
			sb.WriteString(docxRuns(line[2:]))
			sb.WriteString(`</w:p>`)

		case trimmed == "---" || trimmed == "***" || trimmed == "___": // 分割线
			sb.WriteString(`<w:p><w:pPr><w:pBdr>` +
				`<w:bottom w:val="single" w:sz="6" w:space="1" w:color="auto"/>` +
				`</w:pBdr></w:pPr></w:p>`)

		case trimmed == "": // 空行
			sb.WriteString(`<w:p/>`)

		default:
			// 有序列表：1. 2. 3. …
			if num, text, ok := docxParseNumbered(line); ok {
				sb.WriteString(`<w:p><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>`)
				sb.WriteString(`<w:r><w:t xml:space="preserve">` + docxEsc(num+". ") + `</w:t></w:r>`)
				sb.WriteString(docxRuns(text))
				sb.WriteString(`</w:p>`)
			} else {
				sb.WriteString(`<w:p>`)
				sb.WriteString(docxRuns(line))
				sb.WriteString(`</w:p>`)
			}
		}
	}

	// A4 页面尺寸
	sb.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr>`)
	sb.WriteString(`</w:body></w:document>`)
	return sb.String()
}

// docxHeadingLevel 检测行首 # 数量，返回 (level, text)；非标题返回 (0, "")。
func docxHeadingLevel(line string) (level int, text string) {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i > 0 && i < len(line) && line[i] == ' ' {
		return i, line[i+1:]
	}
	return 0, ""
}

// docxParseNumbered 匹配 "1. text" "12. text" 格式。
func docxParseNumbered(line string) (num, text string, ok bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
		return line[:i], line[i+2:], true
	}
	return
}

// docxRuns 解析行内 Markdown（**粗体** *斜体* `代码`），生成 <w:r> 序列。
func docxRuns(text string) string {
	type span struct {
		s    string
		b, i, c bool
	}
	var spans []span
	bold, italic := false, false
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			spans = append(spans, span{buf.String(), bold, italic, false})
			buf.Reset()
		}
	}
	t := text
	for len(t) > 0 {
		switch {
		case t[0] == '`':
			flush()
			if end := strings.IndexByte(t[1:], '`'); end >= 0 {
				spans = append(spans, span{t[1 : 1+end], false, false, true})
				t = t[2+end:]
			} else {
				buf.WriteByte(t[0])
				t = t[1:]
			}
		case len(t) >= 3 && t[:3] == "***":
			flush(); bold = !bold; italic = !italic; t = t[3:]
		case len(t) >= 2 && t[:2] == "**":
			flush(); bold = !bold; t = t[2:]
		case t[0] == '*':
			flush(); italic = !italic; t = t[1:]
		default:
			buf.WriteByte(t[0])
			t = t[1:]
		}
	}
	flush()

	var sb strings.Builder
	for _, sp := range spans {
		sb.WriteString(docxSingleRun(sp.s, sp.b, sp.i, sp.c))
	}
	return sb.String()
}

// docxSingleRun 生成一个 <w:r> 元素。
func docxSingleRun(text string, bold, italic, code bool) string {
	var sb strings.Builder
	sb.WriteString("<w:r>")
	if bold || italic || code {
		sb.WriteString("<w:rPr>")
		if bold {
			sb.WriteString("<w:b/><w:bCs/>")
		}
		if italic {
			sb.WriteString("<w:i/><w:iCs/>")
		}
		if code {
			sb.WriteString(`<w:rFonts w:ascii="Courier New" w:hAnsi="Courier New"/>`)
			sb.WriteString(`<w:sz w:val="20"/>`)
		}
		sb.WriteString("</w:rPr>")
	}
	sb.WriteString(`<w:t xml:space="preserve">`)
	sb.WriteString(docxEsc(text))
	sb.WriteString(`</w:t></w:r>`)
	return sb.String()
}

// docxEsc 转义 XML 特殊字符。
func docxEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ── DOCX 静态模板 ─────────────────────────────────────────────────────────────

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`

const docxTopRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const docxDocRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults>
    <w:rPrDefault><w:rPr>
      <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="宋体" w:cs="Times New Roman"/>
      <w:sz w:val="24"/><w:szCs w:val="24"/>
    </w:rPr></w:rPrDefault>
  </w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal">
    <w:name w:val="Normal"/>
    <w:pPr><w:spacing w:line="360" w:lineRule="auto"/></w:pPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="0"/><w:spacing w:before="240" w:after="80" w:line="360" w:lineRule="auto"/></w:pPr>
    <w:rPr><w:b/><w:bCs/><w:sz w:val="52"/><w:szCs w:val="52"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading2">
    <w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="1"/><w:spacing w:before="200" w:after="60" w:line="360" w:lineRule="auto"/></w:pPr>
    <w:rPr><w:b/><w:bCs/><w:sz w:val="40"/><w:szCs w:val="40"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading3">
    <w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="2"/><w:spacing w:before="160" w:after="40" w:line="360" w:lineRule="auto"/></w:pPr>
    <w:rPr><w:b/><w:bCs/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Quote">
    <w:name w:val="Quote"/><w:basedOn w:val="Normal"/>
    <w:pPr><w:ind w:left="720"/></w:pPr>
    <w:rPr><w:i/><w:iCs/><w:color w:val="666666"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Code">
    <w:name w:val="Code"/><w:basedOn w:val="Normal"/>
    <w:pPr><w:ind w:left="720"/></w:pPr>
    <w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr>
  </w:style>
</w:styles>`

// ── XLSX ─────────────────────────────────────────────────────────────────────

// writeXlsx 将 TSV/CSV 内容转换为 .xlsx 字节数组。
// 输入格式：
//   - 多工作表：每张表以 "--- 表名 ---" 开头，后接 Tab 分隔行
//   - 单工作表：直接写 Tab 或逗号分隔行（无需表头行）
//
// 数值自动识别为数字单元格；其余写为 inlineStr。
func writeXlsx(content string) ([]byte, error) {
	sheets := xlsxParseContent(content)
	if len(sheets) == 0 {
		sheets = []xlsxSheet{{name: "Sheet1"}}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	entries := []struct{ name, body string }{
		{"[Content_Types].xml", xlsxBuildContentTypes(len(sheets))},
		{"_rels/.rels", xlsxTopRels},
		{"xl/workbook.xml", xlsxBuildWorkbook(sheets)},
		{"xl/_rels/workbook.xml.rels", xlsxBuildWorkbookRels(len(sheets))},
		{"xl/styles.xml", xlsxStyles},
	}
	for _, e := range entries {
		f, err := zw.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("xlsx: create %s: %w", e.name, err)
		}
		if _, err = f.Write([]byte(e.body)); err != nil {
			return nil, fmt.Errorf("xlsx: write %s: %w", e.name, err)
		}
	}
	for i, sh := range sheets {
		name := fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		f, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("xlsx: create %s: %w", name, err)
		}
		if _, err = f.Write([]byte(xlsxBuildSheet(sh.rows))); err != nil {
			return nil, fmt.Errorf("xlsx: write %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("xlsx: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// xlsxSheet 表示单张工作表。
type xlsxSheet struct {
	name string
	rows [][]string
}

// xlsxParseContent 解析输入文本为多张工作表。
// "--- 表名 ---" 行作为新工作表的起始标记；无此标记时视为单张 Sheet1。
// 列分隔符：优先 Tab，无 Tab 时用逗号。
func xlsxParseContent(content string) []xlsxSheet {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var sheets []xlsxSheet
	cur := -1

	for _, line := range lines {
		if strings.HasPrefix(line, "--- ") || line == "---" {
			// 解析表名：去掉前后 "--- " / " ---"
			name := strings.TrimPrefix(line, "---")
			name = strings.TrimSuffix(strings.TrimSpace(name), "---")
			name = strings.TrimSpace(name)
			if name == "" {
				name = fmt.Sprintf("Sheet%d", len(sheets)+1)
			}
			sheets = append(sheets, xlsxSheet{name: name})
			cur = len(sheets) - 1
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if cur == -1 {
			sheets = append(sheets, xlsxSheet{name: "Sheet1"})
			cur = 0
		}
		var cells []string
		if strings.Contains(line, "\t") {
			cells = strings.Split(line, "\t")
		} else {
			cells = strings.Split(line, ",")
		}
		// 去掉行尾空单元格
		for len(cells) > 0 && cells[len(cells)-1] == "" {
			cells = cells[:len(cells)-1]
		}
		if len(cells) > 0 {
			sheets[cur].rows = append(sheets[cur].rows, cells)
		}
	}
	return sheets
}

// xlsxBuildSheet 将二维字符串网格生成 worksheet XML。
func xlsxBuildSheet(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	sb.WriteString(`<sheetData>`)
	for ri, row := range rows {
		rowNum := strconv.Itoa(ri + 1)
		sb.WriteString(`<row r="` + rowNum + `">`)
		for ci, val := range row {
			if val == "" {
				continue // 空单元格直接跳过
			}
			ref := xlsxMakeCellRef(ri, ci)
			if _, err := strconv.ParseFloat(val, 64); err == nil {
				// 数值单元格
				sb.WriteString(`<c r="` + ref + `"><v>` + docxEsc(val) + `</v></c>`)
			} else {
				// 字符串：inlineStr，无需 sharedStrings.xml
				sb.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t>` + docxEsc(val) + `</t></is></c>`)
			}
		}
		sb.WriteString(`</row>`)
	}
	sb.WriteString(`</sheetData></worksheet>`)
	return sb.String()
}

// xlsxBuildContentTypes 动态生成 Content_Types.xml（按工作表数量）。
func xlsxBuildContentTypes(sheetCount int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	sb.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	sb.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	sb.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := 1; i <= sheetCount; i++ {
		sb.WriteString(fmt.Sprintf(
			`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i))
	}
	sb.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	sb.WriteString(`</Types>`)
	return sb.String()
}

// xlsxBuildWorkbook 生成 xl/workbook.xml。
func xlsxBuildWorkbook(sheets []xlsxSheet) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `)
	sb.WriteString(`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	sb.WriteString(`<sheets>`)
	for i, sh := range sheets {
		sb.WriteString(fmt.Sprintf(
			`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`,
			docxEsc(sh.name), i+1, i+1))
	}
	sb.WriteString(`</sheets></workbook>`)
	return sb.String()
}

// xlsxBuildWorkbookRels 生成 xl/_rels/workbook.xml.rels。
func xlsxBuildWorkbookRels(sheetCount int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheetCount; i++ {
		sb.WriteString(fmt.Sprintf(
			`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`,
			i, i))
	}
	sb.WriteString(fmt.Sprintf(
		`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`,
		sheetCount+1))
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

// xlsxMakeCellRef 将 (row, col) 0-indexed 转换为单元格引用，如 (0,0)→"A1"，(0,26)→"AA1"。
func xlsxMakeCellRef(row, col int) string {
	return xlsxColLetter(col) + strconv.Itoa(row+1)
}

// xlsxColLetter 将 0-indexed 列号转换为字母，如 0→"A"，25→"Z"，26→"AA"。
func xlsxColLetter(n int) string {
	result := ""
	for {
		result = string(rune('A'+n%26)) + result
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return result
}

// ── XLSX 静态模板 ─────────────────────────────────────────────────────────────

const xlsxTopRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const xlsxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>
  <fills count="2">
    <fill><patternFill patternType="none"/></fill>
    <fill><patternFill patternType="gray125"/></fill>
  </fills>
  <borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
  <cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
  <cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs>
</styleSheet>`
