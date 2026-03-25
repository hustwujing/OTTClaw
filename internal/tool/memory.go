// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/memory.go — 长期记忆工具（Agent 笔记 + 用户人设的 CRUD）
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"OTTClaw/config"
	"OTTClaw/internal/llm"
	"OTTClaw/internal/logger"
	"OTTClaw/internal/storage"
)

// scanMemoryContent 在写入前对内容做安全扫描，拦截三类风险：
//  1. 不可见/零宽 Unicode 字符：可用于隐藏指令或编码外泄数据
//  2. 高置信度 prompt injection 短语：试图覆盖系统指令
//
// 返回非 nil 表示内容可疑，拒绝写入。
func scanMemoryContent(content string) error {
	// ---- 1. 不可见 Unicode 字符 ----
	// 检测范围：零宽字符、方向覆盖字符、私用区、BOM
	for i, r := range content {
		switch {
		case r == '\u200B', // ZERO WIDTH SPACE
			r == '\u200C', // ZERO WIDTH NON-JOINER
			r == '\u200D', // ZERO WIDTH JOINER
			r == '\uFEFF', // BOM / ZERO WIDTH NO-BREAK SPACE
			r == '\u00AD', // SOFT HYPHEN
			r == '\u2028', // LINE SEPARATOR
			r == '\u2029', // PARAGRAPH SEPARATOR
			r >= '\u202A' && r <= '\u202E', // DIRECTIONAL OVERRIDES
			r >= '\uE000' && r <= '\uF8FF', // PRIVATE USE AREA
			unicode.Is(unicode.Cf, r) && r != '\t' && r != '\n': // 其他格式控制字符（保留 tab/newline）
			return fmt.Errorf("content contains suspicious invisible character U+%04X at byte offset %d", r, i)
		}
	}

	// ---- 2. Prompt injection 短语（高置信度，避免误报）----
	lower := strings.ToLower(content)
	injectionPhrases := []string{
		"ignore previous instructions",
		"ignore all previous",
		"disregard all instructions",
		"disregard previous instructions",
		"forget your instructions",
		"forget your system prompt",
		"new system prompt",
		"override your instructions",
	}
	for _, phrase := range injectionPhrases {
		if strings.Contains(lower, phrase) {
			return fmt.Errorf("content contains potential prompt injection pattern: %q", phrase)
		}
	}

	return nil
}

// handleMemory 提供跨会话持久记忆的增删改操作。
// target=notes   : Agent 自身的环境笔记/约定备忘（§分隔条目）
// target=persona : 用户画像（整段自由文本）
func handleMemory(ctx context.Context, argsJSON string) (string, error) {
	userID := userIDFromCtx(ctx)
	if userID == "" {
		return "", fmt.Errorf("user_id not found in context")
	}

	var args struct {
		Action  string `json:"action"`
		Target  string `json:"target"`
		Content string `json:"content"`
		OldText string `json:"old_text"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return memoryError("parse memory args: " + err.Error()), nil
	}

	logger.Debug("tool", userID, "", fmt.Sprintf(
		"[memory] action=%s target=%s content_len=%d old_text_len=%d",
		args.Action, args.Target, len([]rune(args.Content)), len([]rune(args.OldText))), 0)

	switch args.Action {
	case "add", "replace", "remove":
	default:
		return memoryError("unknown action: " + args.Action + " (valid: add/replace/remove)"), nil
	}
	switch args.Target {
	case "notes", "persona":
	default:
		return memoryError("unknown target: " + args.Target + " (valid: notes/persona)"), nil
	}
	if (args.Action == "add" || args.Action == "replace") && args.Content == "" {
		return memoryError("content is required for action=" + args.Action), nil
	}
	if (args.Action == "replace" || args.Action == "remove") && args.OldText == "" {
		return memoryError("old_text is required for action=" + args.Action), nil
	}

	// 读当前值
	var current string
	var charLimit int
	if args.Target == "notes" {
		notes, err := storage.GetUserNotes(userID)
		if err != nil {
			return memoryError("read notes: " + err.Error()), nil
		}
		current = notes
		charLimit = config.Cfg.MemoryNotesCharLimit
		logger.Debug("tool", userID, "", fmt.Sprintf("[memory] read notes: %d chars (limit %d)", len([]rune(current)), charLimit), 0)
	} else {
		profile, err := storage.GetUserProfile(userID)
		if err != nil {
			return memoryError("read persona: " + err.Error()), nil
		}
		if profile != nil {
			current = profile.Persona
		}
		charLimit = config.Cfg.MemoryPersonaCharLimit
		logger.Debug("tool", userID, "", fmt.Sprintf("[memory] read persona: %d chars (limit %d)", len([]rune(current)), charLimit), 0)
	}

	// 应用操作
	var newValue string
	if args.Target == "notes" {
		// notes 用 § 分隔条目
		entries := splitNoteEntries(current)
		switch args.Action {
		case "add":
			entries = append(entries, args.Content)
		case "replace":
			found := false
			for i, e := range entries {
				if e == args.OldText {
					entries[i] = args.Content
					found = true
					break
				}
			}
			if !found {
				return memoryError("old_text not found in notes"), nil
			}
		case "remove":
			newEntries := entries[:0]
			found := false
			for _, e := range entries {
				if e == args.OldText {
					found = true
				} else {
					newEntries = append(newEntries, e)
				}
			}
			if !found {
				return memoryError("old_text not found in notes"), nil
			}
			entries = newEntries
		}
		newValue = strings.Join(entries, "§")
	} else {
		// persona 是单段文本
		switch args.Action {
		case "add":
			if current == "" {
				newValue = args.Content
			} else {
				newValue = current + "\n" + args.Content
			}
		case "replace":
			if !strings.Contains(current, args.OldText) {
				return memoryError("old_text not found in persona"), nil
			}
			newValue = strings.Replace(current, args.OldText, args.Content, 1)
		case "remove":
			if !strings.Contains(current, args.OldText) {
				return memoryError("old_text not found in persona"), nil
			}
			newValue = strings.TrimSpace(strings.Replace(current, args.OldText, "", 1))
		}
	}

	// 字符上限检查
	chars := len([]rune(newValue))
	if chars > charLimit {
		logger.Debug("tool", userID, "", fmt.Sprintf("[memory] %s limit exceeded: %d/%d chars", args.Target, chars, charLimit), 0)
		return memoryError(fmt.Sprintf("%s limit reached (%d/%d chars), remove stale entries first",
			args.Target, chars, charLimit)), nil
	}

	// 安全扫描：拦截不可见字符和 prompt injection（仅扫描新增/替换的内容）
	if args.Action == "add" || args.Action == "replace" {
		if err := scanMemoryContent(args.Content); err != nil {
			logger.Warn("tool", userID, "", fmt.Sprintf("[memory] security scan rejected %s: %v", args.Target, err), 0)
			return memoryError("content rejected by security scan: " + err.Error()), nil
		}
	}

	// 写入 DB
	var writeErr error
	if args.Target == "notes" {
		writeErr = storage.UpsertUserNotes(userID, newValue)
	} else {
		writeErr = storage.UpsertUserProfile(userID, newValue)
	}
	if writeErr != nil {
		return memoryError("write " + args.Target + ": " + writeErr.Error()), nil
	}
	logger.Debug("tool", userID, "", fmt.Sprintf("[memory] ✓ %s %s → %d/%d chars", args.Action, args.Target, chars, charLimit), 0)

	// Honcho 双写：persona add/replace 同步到 Honcho user peer（best-effort，不阻塞）
	if args.Target == "persona" && (args.Action == "add" || args.Action == "replace") {
		if hClient := honchoClientFromCtx(ctx); hClient != nil {
			sessionID := sessionIDFromCtx(ctx)
			observation := args.Content
			logger.Debug("tool", userID, sessionID, "[memory] dual-write persona→Honcho as [observation]", 0)
			go func() {
				hCtx := context.Background()
				_ = hClient.AddMessage(hCtx, userID, sessionID, true, "[observation] "+observation)
			}()
		}
	}

	b, _ := json.Marshal(map[string]any{
		"ok":          true,
		"target":      args.Target,
		"action":      args.Action,
		"chars_used":  chars,
		"chars_limit": charLimit,
	})
	return string(b), nil
}

// splitNoteEntries 按 § 分割笔记条目，过滤空条目
func splitNoteEntries(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, "§")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// memoryError 返回 memory 工具的错误 JSON
func memoryError(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return string(b)
}

// MemoryTool 返回 memory 工具的 LLM schema 定义，供 agent 在 flush/review 时单独传递
func MemoryTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "memory",
			Description: `Persist facts across sessions.
  notes  : your scratchpad — env facts, tool quirks, stable conventions (§-separated entries).
  persona: who the user is — name, role, preferences, style.
Save proactively: user corrections, env/tool discoveries, preferences, recurring patterns.
Skip: task progress, temp state, things easily re-discovered.
At char limit, replace/remove stale entries first.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":   map[string]any{"type": "string", "description": "add | replace | remove"},
					"target":   map[string]any{"type": "string", "description": "notes | persona"},
					"content":  map[string]any{"type": "string", "description": "New content (required for add/replace)"},
					"old_text": map[string]any{"type": "string", "description": "Exact entry text to replace or remove (required for replace/remove)"},
				},
				"required": []string{"action", "target"},
			},
		},
	}
}
