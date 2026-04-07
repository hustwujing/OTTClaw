// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/desktop.go — 跨平台桌面控制工具（截图/鼠标/键盘）
//
// 设计原则：
//   - 纯 shell-out 实现，无 CGO 依赖
//   - macOS：screencapture + cliclick + osascript
//   - Linux：scrot + xdotool
//   - Windows：PowerShell 内置（无需额外安装）
//
// 截图复用现有 PartsResult 机制：
//   - shrinkImage()       (read_image.go) 大图自动压缩
//   - NewPartsResult()    (read_image.go) 多模态工具结果包装
//   - config.Cfg.ReadImageMaxBytes 共用图片大小限制
package tool

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"OTTClaw/config"
	"OTTClaw/internal/llm"
)

// ── 参数结构 ─────────────────────────────────────────────────────────────────

type desktopArgs struct {
	Action    string `json:"action"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Text      string `json:"text"`
	Key       string `json:"key"`
	Direction string `json:"direction"`
	Amount    int    `json:"amount"`
	StartX    int    `json:"start_x"`
	StartY    int    `json:"start_y"`
	EndX      int    `json:"end_x"`
	EndY      int    `json:"end_y"`
}

// ── 入口分发 ─────────────────────────────────────────────────────────────────

func handleDesktop(ctx context.Context, argsJSON string) (string, error) {
	var args desktopArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse desktop args: %w", err)
	}
	switch args.Action {
	case "screenshot":
		return desktopScreenshot(ctx)
	case "get_screen_size":
		return desktopGetScreenSize()
	case "mouse_move":
		return desktopMouseMove(args.X, args.Y)
	case "left_click":
		return desktopClick(args.X, args.Y, "left", 1)
	case "right_click":
		return desktopClick(args.X, args.Y, "right", 1)
	case "double_click":
		return desktopClick(args.X, args.Y, "left", 2)
	case "type":
		return desktopType(args.Text)
	case "key":
		return desktopKey(args.Key)
	case "scroll":
		amount := args.Amount
		if amount <= 0 {
			amount = 3
		}
		return desktopScroll(args.X, args.Y, args.Direction, amount)
	case "drag":
		return desktopDrag(args.StartX, args.StartY, args.EndX, args.EndY)
	default:
		return "", fmt.Errorf("unknown desktop action: %q", args.Action)
	}
}

// ── 截图 ──────────────────────────────────────────────────────────────────────

func desktopScreenshot(ctx context.Context) (string, error) {
	tmpFile := filepath.Join(os.TempDir(),
		fmt.Sprintf("ottclaw_screen_%d.jpg", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	if err := takeScreenshot(tmpFile); err != nil {
		return "", fmt.Errorf("screenshot failed: %w", err)
	}

	data, mediaType, err := desktopLoadImage(tmpFile)
	if err != nil {
		return "", fmt.Errorf("load screenshot: %w", err)
	}

	// 持久化到 output 目录，生成 web 可访问 URL，供前端内联展示
	webURL, saveErr := saveScreenshotToOutput(ctx, data)
	if saveErr != nil {
		webURL = "" // 保存失败不影响 LLM 分析（图片仍在 Parts 里）
	}

	parts := []llm.ContentPart{
		{Type: "text", Text: "当前屏幕截图"},
		{Type: "image", MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data)},
	}
	return NewPartsResult("[已截取屏幕]", parts, webURL), nil
}

// saveScreenshotToOutput 将截图 bytes 写入 output 目录，返回 web 路径（如 /output/1/A/screen_xxx.jpg）。
func saveScreenshotToOutput(ctx context.Context, data []byte) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		userID = "_shared"
	}
	sum := md5.Sum(data)
	bucket := strings.ToUpper(string(fmt.Sprintf("%x", sum)[1]))
	filename := fmt.Sprintf("screen_%d.jpg", time.Now().UnixNano())
	dir := filepath.Join(config.Cfg.OutputDir, userID, bucket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(dir, filename)
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", err
	}
	webPath := filepath.ToSlash(filepath.Join(config.Cfg.OutputDir, userID, bucket, filename))
	return "/" + webPath, nil
}

// takeScreenshot 截取全屏并保存为 JPEG 到 destPath。
func takeScreenshot(destPath string) error {
	switch runtime.GOOS {
	case "darwin":
		return runCmd("screencapture", "-x", "-t", "jpg", destPath)
	case "linux":
		return runCmd("scrot", "-q", "75", destPath)
	case "windows":
		script := fmt.Sprintf(winScreenshotScript, destPath)
		return runPowerShell(script)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// desktopLoadImage 读取截图文件，若超限则自动压缩（复用 shrinkImage）。
func desktopLoadImage(path string) (data []byte, mediaType string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	mediaType = "image/jpeg"
	maxBytes := config.Cfg.ReadImageMaxBytes
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		data, err = shrinkImage(path, info.Size(), maxBytes)
		if err != nil {
			return nil, "", err
		}
		return data, mediaType, nil
	}
	data, err = os.ReadFile(path)
	return data, mediaType, err
}

// ── 屏幕尺寸 ──────────────────────────────────────────────────────────────────

func desktopGetScreenSize() (string, error) {
	var out string
	var err error
	switch runtime.GOOS {
	case "darwin":
		out, err = runCmdOutput("python3", "-c",
			`import subprocess,re;r=subprocess.run(['system_profiler','SPDisplaysDataType'],capture_output=True,text=True);`+
				`m=re.search(r'Resolution: (\d+) x (\d+)',r.stdout);print(m.group(1)+'x'+m.group(2) if m else 'unknown')`)
	case "linux":
		out, err = runCmdOutput("bash", "-c",
			`xrandr | grep ' connected' | grep -oP '\d+x\d+' | head -1`)
	case "windows":
		out, err = runPowerShellOutput(`
Add-Type -AssemblyName System.Windows.Forms
$s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
Write-Output "$($s.Width)x$($s.Height)"`)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ── 鼠标移动 ──────────────────────────────────────────────────────────────────

func desktopMouseMove(x, y int) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "", runCmd("cliclick", fmt.Sprintf("m:%d,%d", x, y))
	case "linux":
		return "", runCmd("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	case "windows":
		return "", runPowerShell(fmt.Sprintf(winMouseMoveScript, x, y))
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 鼠标点击 ──────────────────────────────────────────────────────────────────

// desktopClick 执行单击或双击。button: "left" | "right"，times: 1 | 2。
func desktopClick(x, y int, button string, times int) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		var action string
		switch {
		case button == "left" && times == 2:
			action = "dc"
		case button == "right":
			action = "rc"
		default:
			action = "c"
		}
		return "", runCmd("cliclick", fmt.Sprintf("%s:%d,%d", action, x, y))
	case "linux":
		btn := "1"
		if button == "right" {
			btn = "3"
		}
		if times == 2 {
			return "", runCmd("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
				"click", "--repeat", "2", btn)
		}
		return "", runCmd("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
			"click", btn)
	case "windows":
		var script string
		switch {
		case button == "right":
			script = fmt.Sprintf(winClickScript, x, y, "RIGHTDOWN", "RIGHTUP")
		case times == 2:
			script = fmt.Sprintf(winDoubleClickScript, x, y)
		default:
			script = fmt.Sprintf(winClickScript, x, y, "LEFTDOWN", "LEFTUP")
		}
		return "", runPowerShell(script)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 文本输入 ──────────────────────────────────────────────────────────────────

func desktopType(text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	switch runtime.GOOS {
	case "darwin":
		// cliclick t: 输入文本（特殊字符可能需要转义）
		return "", runCmd("cliclick", "t:"+text)
	case "linux":
		return "", runCmd("xdotool", "type", "--clearmodifiers", text)
	case "windows":
		escaped := winEscapeSendKeys(text)
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait(%q)`, escaped)
		return "", runPowerShell(script)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 按键 ──────────────────────────────────────────────────────────────────────

func desktopKey(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	switch runtime.GOOS {
	case "darwin":
		keyExpr := buildMacKey(key)
		script := fmt.Sprintf(`tell application "System Events" to %s`, keyExpr)
		return "", runCmd("osascript", "-e", script)
	case "linux":
		// xdotool key 接受 ctrl+c、Return、Escape 等标准名称
		xkey := buildXdotoolKey(key)
		return "", runCmd("xdotool", "key", xkey)
	case "windows":
		sendKey := buildWinSendKey(key)
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait(%q)`, sendKey)
		return "", runPowerShell(script)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 滚动 ──────────────────────────────────────────────────────────────────────

func desktopScroll(x, y int, direction string, amount int) (string, error) {
	if direction == "" {
		direction = "down"
	}
	switch runtime.GOOS {
	case "darwin":
		var action string
		switch direction {
		case "up":
			action = "su"
		case "down":
			action = "sd"
		case "left":
			action = "sl"
		default: // right
			action = "sr"
		}
		// 重复 action 达到 amount 次滚动
		args := []string{}
		for i := 0; i < amount; i++ {
			args = append(args, fmt.Sprintf("%s:%d,%d", action, x, y))
		}
		return "", runCmd("cliclick", args...)
	case "linux":
		// xdotool: button 4=up, 5=down, 6=left, 7=right
		btn := "5"
		switch direction {
		case "up":
			btn = "4"
		case "left":
			btn = "6"
		case "right":
			btn = "7"
		}
		return "", runCmd("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
			"click", "--repeat", fmt.Sprintf("%d", amount), btn)
	case "windows":
		wheelDelta := 120 * amount
		if direction == "down" {
			wheelDelta = -wheelDelta
		}
		var script string
		if direction == "left" || direction == "right" {
			if direction == "left" {
				wheelDelta = -120 * amount
			} else {
				wheelDelta = 120 * amount
			}
			script = fmt.Sprintf(winScrollHScript, x, y, wheelDelta)
		} else {
			script = fmt.Sprintf(winScrollVScript, x, y, wheelDelta)
		}
		return "", runPowerShell(script)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 拖拽 ──────────────────────────────────────────────────────────────────────

func desktopDrag(startX, startY, endX, endY int) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "", runCmd("cliclick", "-w", "50",
			fmt.Sprintf("dd:%d,%d", startX, startY),
			fmt.Sprintf("m:%d,%d", endX, endY),
			fmt.Sprintf("du:%d,%d", endX, endY))
	case "linux":
		return "", runCmd("xdotool",
			"mousemove", fmt.Sprintf("%d", startX), fmt.Sprintf("%d", startY),
			"mousedown", "1",
			"mousemove", fmt.Sprintf("%d", endX), fmt.Sprintf("%d", endY),
			"mouseup", "1")
	case "windows":
		script := fmt.Sprintf(winDragScript, startX, startY, endX, endY)
		return "", runPowerShell(script)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s failed: %w\noutput: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runCmdOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w\noutput: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func runPowerShell(script string) error {
	_, err := runPowerShellOutput(script)
	return err
}

func runPowerShellOutput(script string) (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("powershell failed: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ── macOS 按键映射 ────────────────────────────────────────────────────────────

// buildMacKey 将 key combo 字符串转为 osascript tell System Events 表达式。
// 示例：ctrl+c → keystroke "c" using {control down}
//
//	Return → key code 36
func buildMacKey(key string) string {
	lower := strings.ToLower(key)
	parts := strings.Split(lower, "+")
	mainKey := parts[len(parts)-1]
	modifiers := parts[:len(parts)-1]

	keyCodes := map[string]int{
		"return": 36, "enter": 36,
		"escape": 53, "esc": 53,
		"tab": 48,
		"backspace": 51, "delete": 51,
		"forwarddelete": 117,
		"space": 49,
		"left": 123, "right": 124, "down": 125, "up": 126,
		"home": 115, "end": 119,
		"pageup": 116, "pagedown": 121,
		"f1": 122, "f2": 120, "f3": 99, "f4": 118,
		"f5": 96, "f6": 97, "f7": 98, "f8": 100,
		"f9": 101, "f10": 109, "f11": 103, "f12": 111,
	}

	modMap := map[string]string{
		"ctrl": "control", "control": "control",
		"alt": "option", "option": "option",
		"shift": "shift",
		"cmd": "command", "command": "command", "super": "command",
	}

	var modParts []string
	for _, m := range modifiers {
		if mapped, ok := modMap[m]; ok {
			modParts = append(modParts, mapped+" down")
		}
	}
	using := ""
	if len(modParts) > 0 {
		using = fmt.Sprintf(" using {%s}", strings.Join(modParts, ", "))
	}

	if code, ok := keyCodes[mainKey]; ok {
		return fmt.Sprintf("key code %d%s", code, using)
	}
	// 单字符按键
	return fmt.Sprintf(`keystroke "%s"%s`, mainKey, using)
}

// ── Linux xdotool 按键映射 ────────────────────────────────────────────────────

// buildXdotoolKey 规范化 key 字符串为 xdotool key 格式。
// xdotool 使用 X11 keysym 名称：ctrl+c → ctrl+c，Return，Escape 等。
func buildXdotoolKey(key string) string {
	lower := strings.ToLower(key)
	// 规范化常见别名
	replacer := strings.NewReplacer(
		"escape", "Escape",
		"return", "Return",
		"enter", "Return",
		"backspace", "BackSpace",
		"delete", "Delete",
		"tab", "Tab",
		"space", "space",
		"left", "Left",
		"right", "Right",
		"up", "Up",
		"down", "Down",
		"home", "Home",
		"end", "End",
		"pageup", "Prior",
		"pagedown", "Next",
		"ctrl", "ctrl",
		"alt", "alt",
		"shift", "shift",
	)
	return replacer.Replace(lower)
}

// ── Windows SendKeys 按键映射 ─────────────────────────────────────────────────

// buildWinSendKey 将 key combo 字符串转为 SendKeys.SendWait 格式字符串。
// 示例：ctrl+c → ^c，Return → {ENTER}，escape → {ESC}
func buildWinSendKey(key string) string {
	lower := strings.ToLower(key)
	parts := strings.Split(lower, "+")
	mainKey := parts[len(parts)-1]
	modifiers := parts[:len(parts)-1]

	specialKeys := map[string]string{
		"return": "{ENTER}", "enter": "{ENTER}",
		"escape": "{ESC}", "esc": "{ESC}",
		"tab": "{TAB}",
		"backspace": "{BACKSPACE}",
		"delete": "{DELETE}",
		"space": " ",
		"left": "{LEFT}", "right": "{RIGHT}",
		"up": "{UP}", "down": "{DOWN}",
		"home": "{HOME}", "end": "{END}",
		"pageup": "{PGUP}", "pagedown": "{PGDN}",
		"f1": "{F1}", "f2": "{F2}", "f3": "{F3}", "f4": "{F4}",
		"f5": "{F5}", "f6": "{F6}", "f7": "{F7}", "f8": "{F8}",
		"f9": "{F9}", "f10": "{F10}", "f11": "{F11}", "f12": "{F12}",
	}

	modMap := map[string]string{
		"ctrl": "^", "control": "^",
		"alt": "%",
		"shift": "+",
	}

	var prefix strings.Builder
	for _, m := range modifiers {
		if p, ok := modMap[m]; ok {
			prefix.WriteString(p)
		}
	}

	var k string
	if sk, ok := specialKeys[mainKey]; ok {
		k = sk
	} else if len(mainKey) == 1 {
		k = mainKey
	} else {
		k = "{" + strings.ToUpper(mainKey) + "}"
	}

	p := prefix.String()
	if p != "" {
		// 对于字母键：^c，对于特殊键：^{F4} 或 ^({ENTER})
		if strings.HasPrefix(k, "{") {
			return p + "(" + k + ")"
		}
		return p + k
	}
	return k
}

// winEscapeSendKeys 转义 SendKeys 特殊字符（+、^、%、~、{、}、[、]、(、)）。
func winEscapeSendKeys(text string) string {
	var b strings.Builder
	for _, c := range text {
		switch c {
		case '+', '^', '%', '~', '{', '}', '[', ']', '(', ')':
			b.WriteRune('{')
			b.WriteRune(c)
			b.WriteRune('}')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// ── Windows PowerShell 脚本模板 ───────────────────────────────────────────────

// winMouseBase 包含 WinAPI mouse 控制所需的 C# Add-Type 定义（单引号 PS heredoc 兼容）
const winMouseBase = `Add-Type -TypeDefinition '
using System;
using System.Runtime.InteropServices;
public class WM {
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, int dwExtraInfo);
    public const uint LEFTDOWN  = 0x0002;
    public const uint LEFTUP    = 0x0004;
    public const uint RIGHTDOWN = 0x0008;
    public const uint RIGHTUP   = 0x0010;
    public const uint WHEEL     = 0x0800;
    public const uint HWHEEL    = 0x1000;
}
' -Language CSharp -ErrorAction SilentlyContinue
`

const winScreenshotScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bitmap = New-Object System.Drawing.Bitmap($screen.Width, $screen.Height)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen($screen.Location, [System.Drawing.Point]::Empty, $screen.Size)
$bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Jpeg)
$graphics.Dispose()
$bitmap.Dispose()
`

const winMouseMoveScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
`

// winClickScript: %d %d x,y; %s DOWN flag; %s UP flag
const winClickScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
[WM]::mouse_event([WM]::%s, 0, 0, 0, 0)
[WM]::mouse_event([WM]::%s, 0, 0, 0, 0)
`

const winDoubleClickScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
[WM]::mouse_event([WM]::LEFTDOWN, 0, 0, 0, 0)
[WM]::mouse_event([WM]::LEFTUP, 0, 0, 0, 0)
Start-Sleep -Milliseconds 50
[WM]::mouse_event([WM]::LEFTDOWN, 0, 0, 0, 0)
[WM]::mouse_event([WM]::LEFTUP, 0, 0, 0, 0)
`

const winScrollVScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
[WM]::mouse_event([WM]::WHEEL, 0, 0, [uint](%d), 0)
`

const winScrollHScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
[WM]::mouse_event([WM]::HWHEEL, 0, 0, [uint](%d), 0)
`

const winDragScript = winMouseBase + `
[WM]::SetCursorPos(%d, %d) | Out-Null
[WM]::mouse_event([WM]::LEFTDOWN, 0, 0, 0, 0)
Start-Sleep -Milliseconds 50
[WM]::SetCursorPos(%d, %d) | Out-Null
Start-Sleep -Milliseconds 50
[WM]::mouse_event([WM]::LEFTUP, 0, 0, 0, 0)
`
