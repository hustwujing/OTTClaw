// Author:    维杰（邬晶）
// Email:     wujing03@bilibili.com
// Date:      2026
// Copyright: Copyright (c) 2026 维杰（邬晶）

// internal/browser/client.go — Go HTTP 客户端，封装对 browser-server 的所有请求
package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// ── Request / Response types ───────────────────────────────────────────────────

// BrowserStatus 返回用户浏览器上下文状态
type BrowserStatus struct {
	Launched       bool   `json:"launched"`
	PageCount      int    `json:"pageCount"`
	ActivePageIdx  int    `json:"activePageIdx"`
	CurrentURL     string `json:"currentUrl"`
	BrowserRunning bool   `json:"browserRunning"`
}

// NavigateResult 导航结果
type NavigateResult struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	HTTPStatus *int   `json:"httpStatus"`
}

// SnapshotResult 快照结果（带 ref 标注）
type SnapshotResult struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Snapshot string `json:"snapshot"`
	RefCount int    `json:"refCount"`
}

// ActRequest 执行操作请求
type ActRequest struct {
	Action         string   `json:"action"`
	Ref            string   `json:"ref,omitempty"`
	Text           string   `json:"text,omitempty"`
	Key            string   `json:"key,omitempty"`
	Values         []string `json:"values,omitempty"`
	Selector       string   `json:"selector,omitempty"`
	Script         string   `json:"script,omitempty"`
	DeltaX         float64  `json:"deltaX,omitempty"`
	DeltaY         float64  `json:"deltaY,omitempty"`
	FullPage       bool     `json:"fullPage,omitempty"`
	CookieName     string   `json:"cookieName,omitempty"`
	SliderSelector string   `json:"sliderSelector,omitempty"`
	TimeoutMs      int      `json:"timeoutMs,omitempty"`
}

// ActResult 操作结果
type ActResult struct {
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
	Result any    `json:"result,omitempty"`
}

// ScreenshotResult 截图结果
type ScreenshotResult struct {
	Path         string `json:"path"`
	AbsolutePath string `json:"absolutePath"`
	WebURL       string `json:"webUrl"` // 可直接访问的 Web 路径，如 /output/x/screenshot_123.png
}

// TabInfo 单个标签页信息
type TabInfo struct {
	Index  int    `json:"index"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Active bool   `json:"active"`
}

// ── Client ─────────────────────────────────────────────────────────────────────

// Client 与 browser-server 通信的 HTTP 客户端
type Client struct {
	baseURL string
	userID  string
	ctx     context.Context
}

// NewClient 创建一个绑定了 userID 的客户端，ctx 用于取消正在进行的 HTTP 请求
func NewClient(userID string, ctx context.Context) *Client {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Client{baseURL: Default.BaseURL(), userID: userID, ctx: ctx}
}

func (c *Client) header() http.Header {
	h := http.Header{}
	h.Set("x-user-id", c.userID)
	h.Set("Content-Type", "application/json")
	return h
}

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header = c.header()
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("browser-server GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	return decodeResp(resp, out)
}

func (c *Client) post(path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header = c.header()
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("browser-server POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return decodeResp(resp, out)
}

func decodeResp(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("browser-server HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// ── API methods ────────────────────────────────────────────────────────────────

// Status 获取用户浏览器上下文状态
func (c *Client) Status() (*BrowserStatus, error) {
	var out BrowserStatus
	return &out, c.get("/status", &out)
}

// Launch 为用户创建 BrowserContext。headless 非 nil 时覆盖服务端默认值（false=有头模式，用于本地登录）。
func (c *Client) Launch(headless *bool) error {
	var body any
	if headless != nil {
		body = map[string]any{"headless": *headless}
	}
	return c.post("/launch", body, nil)
}

// Close 关闭用户的 BrowserContext
func (c *Client) Close() error {
	return c.post("/close", nil, nil)
}

// Navigate 导航到指定 URL
func (c *Client) Navigate(url string, timeoutMs int) (*NavigateResult, error) {
	body := map[string]any{"url": url}
	if timeoutMs > 0 {
		body["timeoutMs"] = timeoutMs
	}
	var out NavigateResult
	return &out, c.post("/navigate", body, &out)
}

// Snapshot 获取页面 aria snapshot（带 ref 标注）
func (c *Client) Snapshot() (*SnapshotResult, error) {
	var out SnapshotResult
	return &out, c.get("/snapshot", &out)
}

// Act 执行一个 browser 操作
func (c *Client) Act(req ActRequest) (*ActResult, error) {
	var out ActResult
	return &out, c.post("/act", req, &out)
}

// Screenshot 截图，返回文件路径。
// selector 非空时只截取匹配元素（忽略 fullPage）；为空则截取全页或可视区。
func (c *Client) Screenshot(fullPage bool, selector string) (*ScreenshotResult, error) {
	var out ScreenshotResult
	body := map[string]any{"fullPage": fullPage}
	if selector != "" {
		body["selector"] = selector
	}
	return &out, c.post("/screenshot", body, &out)
}

// Tabs 列出所有标签页
func (c *Client) Tabs() ([]TabInfo, error) {
	var out struct {
		Tabs []TabInfo `json:"tabs"`
	}
	if err := c.get("/tabs", &out); err != nil {
		return nil, err
	}
	return out.Tabs, nil
}

// TabOpen 新开标签页
func (c *Client) TabOpen(url string) (*TabInfo, error) {
	body := map[string]any{}
	if url != "" {
		body["url"] = url
	}
	var out TabInfo
	return &out, c.post("/tab/open", body, &out)
}

// TabClose 关闭标签页
func (c *Client) TabClose(index int) error {
	return c.post("/tab/close", map[string]any{"index": index}, nil)
}
