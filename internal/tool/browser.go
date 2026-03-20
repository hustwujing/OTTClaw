// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/browser.go — browser 工具处理器
//
// 通过单一工具名 "browser" + action 参数分发所有浏览器操作。
// 依赖 internal/browser 包的 HTTP 客户端与 Node.js Playwright sidecar 通信。
//
// 支持的 action：
//   launch, close, navigate, snapshot, screenshot,
//   click, type, select, scroll, drag, hover, press_key,
//   wait, evaluate, tabs, tab_open, tab_close,
//   solve_slider_captcha
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"OTTClaw/internal/browser"
)

// browserBlockedHint 检测页面是否疑似被反爬/WAF 拦截，是则返回提示文本，否则返回空字符串。
// 检测依据：HTTP 状态码（403/429/503）或页面标题含 Cloudflare/WAF 常见特征词。
func browserBlockedHint(title string, httpStatus *int) string {
	if httpStatus != nil {
		s := *httpStatus
		if s == 403 || s == 429 || s == 503 {
			return fmt.Sprintf("页面返回 HTTP %d，疑似被反爬/WAF 拦截。", s)
		}
	}
	t := strings.ToLower(title)
	for _, kw := range []string{
		"just a moment", "cloudflare", "access denied",
		"attention required", "security check", "ddos-guard",
		"please wait", "please stand by", "verifying you",
	} {
		if strings.Contains(t, kw) {
			return "页面标题含反爬特征（\"" + title + "\"），疑似 Cloudflare/WAF 验证页。"
		}
	}
	return ""
}

// browserArgs browser 工具的入参
type browserArgs struct {
	Action         string   `json:"action"`
	URL            string   `json:"url,omitempty"`
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
	TargetIdx      int      `json:"targetIdx,omitempty"`
	TimeoutMs      int      `json:"timeoutMs,omitempty"`
	Headless       *bool    `json:"headless,omitempty"`
	Visible        bool     `json:"visible,omitempty"`
}

func handleBrowser(ctx context.Context, argsJSON string) (string, error) {
	var args browserArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("browser: invalid args: %w", err)
	}

	userID := userIDFromCtx(ctx)
	if userID == "" {
		userID = "default"
	}

	if !browser.Default.IsRunning() {
		return "", fmt.Errorf("browser: server is not running. Please contact administrator.")
	}

	c := browser.NewClient(userID, ctx)

	switch args.Action {
	case "launch":
		headless := args.Headless
		if args.Visible {
			f := false
			headless = &f
		}
		if err := c.Launch(headless); err != nil {
			return "", err
		}
		return `{"status":"ok","message":"Browser launched"}`, nil

	case "close":
		if err := c.Close(); err != nil {
			return "", err
		}
		return `{"status":"ok","message":"Browser closed"}`, nil

	case "navigate":
		if args.URL == "" {
			return "", fmt.Errorf("browser navigate: url is required")
		}
		result, err := c.Navigate(args.URL, args.TimeoutMs)
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"url":        result.URL,
			"title":      result.Title,
			"httpStatus": result.HTTPStatus,
		}
		if hint := browserBlockedHint(result.Title, result.HTTPStatus); hint != "" {
			out["antiBot"] = hint
		}
		return jsonStr(out)

	case "snapshot":
		result, err := c.Snapshot()
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"url":      result.URL,
			"title":    result.Title,
			"snapshot": result.Snapshot,
			"refCount": result.RefCount,
		}
		if hint := browserBlockedHint(result.Title, nil); hint != "" {
			out["antiBot"] = hint
		}
		return jsonStr(out)

	case "screenshot":
		result, err := c.Screenshot(args.FullPage, args.Selector)
		if err != nil {
			return "", err
		}
		return jsonStr(map[string]any{
			"path":   result.Path,
			"webUrl": result.WebURL,
		})

	case "click":
		if args.Ref == "" {
			return "", fmt.Errorf("browser click: ref is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "click", Ref: args.Ref, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "type":
		if args.Ref == "" {
			return "", fmt.Errorf("browser type: ref is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "type", Ref: args.Ref, Text: args.Text, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "select":
		if args.Ref == "" {
			return "", fmt.Errorf("browser select: ref is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "select", Ref: args.Ref, Values: args.Values, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "hover":
		if args.Ref == "" {
			return "", fmt.Errorf("browser hover: ref is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "hover", Ref: args.Ref, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "scroll":
		result, err := c.Act(browser.ActRequest{
			Action: "scroll", Ref: args.Ref,
			DeltaX: args.DeltaX, DeltaY: args.DeltaY,
			TimeoutMs: args.TimeoutMs,
		})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "drag":
		result, err := c.Act(browser.ActRequest{
			Action: "drag", Ref: args.Ref, Selector: args.Selector,
			DeltaX: args.DeltaX, DeltaY: args.DeltaY,
			TimeoutMs: args.TimeoutMs,
		})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "solve_slider_captcha":
		result, err := c.Act(browser.ActRequest{
			Action:         "solve_slider_captcha",
			SliderSelector: args.SliderSelector,
		})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "press_key":
		if args.Key == "" {
			return "", fmt.Errorf("browser press_key: key is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "press_key", Ref: args.Ref, Key: args.Key, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "wait":
		result, err := c.Act(browser.ActRequest{Action: "wait", Selector: args.Selector, TimeoutMs: args.TimeoutMs})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "evaluate":
		if args.Script == "" {
			return "", fmt.Errorf("browser evaluate: script is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "evaluate", Script: args.Script})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "tabs":
		tabs, err := c.Tabs()
		if err != nil {
			return "", err
		}
		return jsonStr(map[string]any{"tabs": tabs})

	case "tab_open":
		tab, err := c.TabOpen(args.URL)
		if err != nil {
			return "", err
		}
		return jsonStr(tab)

	case "tab_close":
		if err := c.TabClose(args.TargetIdx); err != nil {
			return "", err
		}
		return `{"status":"ok"}`, nil

	case "save_cookies":
		if args.CookieName == "" {
			return "", fmt.Errorf("browser save_cookies: cookieName is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "save_cookies", CookieName: args.CookieName})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "load_cookies":
		if args.CookieName == "" {
			return "", fmt.Errorf("browser load_cookies: cookieName is required")
		}
		result, err := c.Act(browser.ActRequest{Action: "load_cookies", CookieName: args.CookieName})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	case "list_cookies":
		result, err := c.Act(browser.ActRequest{Action: "list_cookies"})
		if err != nil {
			return "", err
		}
		return jsonStr(result)

	default:
		return "", fmt.Errorf("browser: unknown action %q. Valid actions: launch, close, navigate, snapshot, screenshot, click, type, select, scroll, drag, hover, press_key, wait, evaluate, tabs, tab_open, tab_close, solve_slider_captcha", args.Action)
	}
}

func jsonStr(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
