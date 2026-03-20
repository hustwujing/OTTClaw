// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/web_fetch.go — 轻量级 HTTP 网页抓取工具
//
// 适用场景：静态页面、文档、API 响应；无需 JavaScript 执行时优先用此工具，比 browser 快得多。
//
// 抓取策略：
//  1. 直接 HTTP GET（Chrome User-Agent，限制重定向次数和响应大小）
//  2. 按 Content-Type 提取内容：HTML→Markdown、JSON 格式化、纯文本/Markdown 直接返回
//  3. 结果截断到 maxChars，并缓存 5 分钟（内存）
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"

	"OTTClaw/config"
)

// ========== 缓存 ==========

const (
	webFetchCacheTTL     = 5 * time.Minute
	webFetchDefaultChars = 20_000
	webFetchMaxChars     = 100_000
	webFetchMaxBodyBytes = 2_000_000 // 2 MB
	webFetchMaxRedirects = 5
	webFetchUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

type webFetchCacheEntry struct {
	payload   map[string]any
	expiresAt time.Time
}

var webFetchCache = struct {
	mu   sync.Mutex
	data map[string]*webFetchCacheEntry
}{data: make(map[string]*webFetchCacheEntry)}

func webFetchCacheGet(key string) (map[string]any, bool) {
	webFetchCache.mu.Lock()
	defer webFetchCache.mu.Unlock()
	e, ok := webFetchCache.data[key]
	if !ok || time.Now().After(e.expiresAt) {
		delete(webFetchCache.data, key)
		return nil, false
	}
	return e.payload, true
}

func webFetchCacheSet(key string, payload map[string]any) {
	webFetchCache.mu.Lock()
	defer webFetchCache.mu.Unlock()
	webFetchCache.data[key] = &webFetchCacheEntry{
		payload:   payload,
		expiresAt: time.Now().Add(webFetchCacheTTL),
	}
}

// ========== HTTP 客户端（限制重定向次数）==========

func newWebFetchClient() *http.Client {
	redirectCount := 0
	return &http.Client{
		Timeout: time.Duration(config.Cfg.ToolWebFetchTimeoutSec) * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			redirectCount++
			if redirectCount > webFetchMaxRedirects {
				return fmt.Errorf("too many redirects (max %d)", webFetchMaxRedirects)
			}
			_ = via
			return nil
		},
	}
}

// ========== HTML → Markdown 提取 ==========

// 跳过这些标签的全部内容（噪音节点）
var htmlSkipTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"nav": true, "header": true, "footer": true, "aside": true,
	"iframe": true, "svg": true, "canvas": true, "form": true,
}

// 块级标签：渲染前后加换行
var htmlBlockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"blockquote": true, "figure": true, "figcaption": true,
	"ul": true, "ol": true, "dl": true, "dt": true, "dd": true,
	"table": true, "tr": true, "thead": true, "tbody": true,
	"br": true, "hr": true,
}

type htmlExtractor struct {
	sb        strings.Builder
	title     string
	inSkip    int    // 嵌套在跳过标签中的深度
	listDepth int    // ul/ol 嵌套深度
	listIdx   []int  // 有序列表计数器栈
	inPre     int    // pre/code 块深度
}

func (e *htmlExtractor) extractTitle(n *html.Node) {
	if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
		e.title = strings.TrimSpace(n.FirstChild.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if e.title == "" {
			e.extractTitle(c)
		}
	}
}

func (e *htmlExtractor) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		tag := n.Data
		if htmlSkipTags[tag] {
			e.inSkip++
			// 仍然遍历子节点（让 inSkip 递减正常工作），但不输出
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				e.walk(c)
			}
			e.inSkip--
			return
		}
		if tag == "pre" || tag == "code" {
			e.inPre++
		}
		if tag == "ul" || tag == "ol" {
			e.listDepth++
			if tag == "ol" {
				e.listIdx = append(e.listIdx, 0)
			} else {
				e.listIdx = append(e.listIdx, -1) // -1 表示 ul
			}
		}

		// 块级标签：前加换行
		if htmlBlockTags[tag] && e.inSkip == 0 {
			e.ensureNewline()
		}
		// 标题
		switch tag {
		case "h1":
			e.ensureNewline()
			e.sb.WriteString("# ")
		case "h2":
			e.ensureNewline()
			e.sb.WriteString("## ")
		case "h3":
			e.ensureNewline()
			e.sb.WriteString("### ")
		case "h4":
			e.ensureNewline()
			e.sb.WriteString("#### ")
		case "h5", "h6":
			e.ensureNewline()
			e.sb.WriteString("##### ")
		case "li":
			e.ensureNewline()
			indent := strings.Repeat("  ", max(e.listDepth-1, 0))
			if len(e.listIdx) > 0 && e.listIdx[len(e.listIdx)-1] >= 0 {
				e.listIdx[len(e.listIdx)-1]++
				e.sb.WriteString(fmt.Sprintf("%s%d. ", indent, e.listIdx[len(e.listIdx)-1]))
			} else {
				e.sb.WriteString(indent + "- ")
			}
		case "strong", "b":
			if e.inSkip == 0 {
				e.sb.WriteString("**")
			}
		case "em", "i":
			if e.inSkip == 0 {
				e.sb.WriteString("_")
			}
		case "code":
			if e.inPre == 1 && e.inSkip == 0 { // 行内 code（外层没有 pre）
				e.sb.WriteString("`")
			}
		case "pre":
			if e.inSkip == 0 {
				e.ensureNewline()
				e.sb.WriteString("```\n")
			}
		case "blockquote":
			e.ensureNewline()
			e.sb.WriteString("> ")
		case "th", "td":
			if e.inSkip == 0 {
				e.sb.WriteString("| ")
			}
		case "hr":
			if e.inSkip == 0 {
				e.ensureNewline()
				e.sb.WriteString("---\n")
			}
		case "br":
			if e.inSkip == 0 {
				e.sb.WriteString("\n")
			}
		}

		// 递归子节点
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			e.walk(c)
		}

		// 关闭标签
		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			e.sb.WriteString("\n")
		case "strong", "b":
			if e.inSkip == 0 {
				e.sb.WriteString("**")
			}
		case "em", "i":
			if e.inSkip == 0 {
				e.sb.WriteString("_")
			}
		case "code":
			if e.inPre == 1 && e.inSkip == 0 {
				e.sb.WriteString("`")
			}
		case "pre":
			if e.inSkip == 0 {
				e.sb.WriteString("\n```\n")
			}
		case "a":
			// 追加链接 href（仅在非 pre 块中）
			if e.inSkip == 0 && e.inPre == 0 {
				for _, attr := range n.Attr {
					if attr.Key == "href" && strings.HasPrefix(attr.Val, "http") {
						e.sb.WriteString(fmt.Sprintf(" (%s)", attr.Val))
					}
				}
			}
		case "tr":
			if e.inSkip == 0 {
				e.sb.WriteString(" |\n")
			}
		}

		if tag == "pre" || tag == "code" {
			if e.inPre > 0 {
				e.inPre--
			}
		}
		if tag == "ul" || tag == "ol" {
			if e.listDepth > 0 {
				e.listDepth--
			}
			if len(e.listIdx) > 0 {
				e.listIdx = e.listIdx[:len(e.listIdx)-1]
			}
		}
		if htmlBlockTags[tag] && e.inSkip == 0 {
			e.ensureNewline()
		}
		return
	}

	// 文本节点
	if n.Type == html.TextNode && e.inSkip == 0 {
		text := n.Data
		if e.inPre > 0 {
			e.sb.WriteString(text)
		} else {
			// 合并连续空白
			normalized := strings.Join(strings.Fields(text), " ")
			if normalized != "" {
				e.sb.WriteString(normalized + " ")
			}
		}
	}

	// 其余类型：继续遍历
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		e.walk(c)
	}
}

func (e *htmlExtractor) ensureNewline() {
	s := e.sb.String()
	if len(s) > 0 && s[len(s)-1] != '\n' {
		e.sb.WriteByte('\n')
	}
}

// htmlToMarkdown 解析 HTML，返回 (title, markdownText)
func htmlToMarkdown(body string) (title, text string) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return "", body
	}
	ex := &htmlExtractor{}
	ex.extractTitle(doc)
	ex.walk(doc)
	raw := ex.sb.String()
	// 清理多余空行（连续 3 行以上空行 → 2 行）
	for strings.Contains(raw, "\n\n\n") {
		raw = strings.ReplaceAll(raw, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(ex.title), strings.TrimSpace(raw)
}

// ========== 截断 ==========

func truncateChars(s string, maxChars int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s, false
	}
	return string(runes[:maxChars]), true
}

// ========== 工具处理器 ==========

func handleWebFetch(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse web_fetch args: %w", err)
	}
	if strings.TrimSpace(args.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.MaxChars <= 0 {
		args.MaxChars = webFetchDefaultChars
	}
	if args.MaxChars > webFetchMaxChars {
		args.MaxChars = webFetchMaxChars
	}

	// 仅支持 http/https
	lower := strings.ToLower(args.URL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	cacheKey := fmt.Sprintf("%s|%d", args.URL, args.MaxChars)
	if cached, ok := webFetchCacheGet(cacheKey); ok {
		cached["cached"] = true
		b, _ := json.Marshal(cached)
		return string(b), nil
	}

	client := newWebFetchClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/markdown, text/html;q=0.9, application/json;q=0.8, */*;q=0.1")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", args.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch %s: HTTP %d", args.URL, resp.StatusCode)
	}

	// 读取响应体（限制大小）
	limited := io.LimitReader(resp.Body, webFetchMaxBodyBytes)
	rawBytes, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}
	bodyStr := string(rawBytes)
	tookMs := time.Since(start).Milliseconds()

	contentType := resp.Header.Get("Content-Type")
	ct := strings.ToLower(contentType)

	var title, text, extractor string

	switch {
	case strings.Contains(ct, "text/html"):
		title, text = htmlToMarkdown(bodyStr)
		extractor = "html2md"
	case strings.Contains(ct, "application/json"):
		var parsed any
		if err := json.Unmarshal(rawBytes, &parsed); err == nil {
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			text = string(pretty)
		} else {
			text = bodyStr
		}
		extractor = "json"
	default:
		// text/markdown, text/plain, 其他
		text = bodyStr
		extractor = "raw"
	}

	// 截断
	truncated, wasTruncated := truncateChars(text, args.MaxChars)

	payload := map[string]any{
		"url":         args.URL,
		"finalUrl":    resp.Request.URL.String(),
		"status":      resp.StatusCode,
		"contentType": contentType,
		"extractor":   extractor,
		"truncated":   wasTruncated,
		"length":      utf8.RuneCountInString(truncated),
		"tookMs":      tookMs,
		"text":        truncated,
	}
	if title != "" {
		payload["title"] = title
	}

	webFetchCacheSet(cacheKey, payload)

	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
}
