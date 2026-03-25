// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/tool/honcho.go — Honcho memory tools (Layer 2)
//
// 4 tools:
//   honcho_profile  — fast structured facts about the user (no LLM)
//   honcho_search   — semantic search over user history (no LLM)
//   honcho_context  — dialectic natural-language reasoning (uses LLM, higher cost)
//   honcho_conclude — write a conclusion about the user into Honcho
//
// The Honcho client is injected via context (WithHonchoClient).
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"OTTClaw/internal/honcho"
	"OTTClaw/internal/llm"
	"OTTClaw/internal/logger"
)

// ========== Context 注入：Honcho Client ==========

type honchoClientCtxKey struct{}

// WithHonchoClient injects the Honcho client into the context.
func WithHonchoClient(ctx context.Context, c *honcho.Client) context.Context {
	return context.WithValue(ctx, honchoClientCtxKey{}, c)
}

func honchoClientFromCtx(ctx context.Context) *honcho.Client {
	c, _ := ctx.Value(honchoClientCtxKey{}).(*honcho.Client)
	return c
}

// ========== Tool handlers ==========

// handleHonchoProfile retrieves stored observations about the user from Honcho metamessages.
// No LLM is invoked on the Honcho side — pure data retrieval.
func handleHonchoProfile(ctx context.Context, _ string) (string, error) {
	hClient := honchoClientFromCtx(ctx)
	if hClient == nil {
		return honchoToolError("honcho not configured"), nil
	}
	userID := userIDFromCtx(ctx)
	sessionID := sessionIDFromCtx(ctx)
	logger.Debug("tool", userID, sessionID, "[honcho-profile] fetching user facts (no LLM)", 0)
	facts, err := hClient.GetUserFacts(ctx, userID)
	if err != nil {
		logger.Warn("tool", userID, sessionID, "[honcho-profile] error: "+err.Error(), 0)
		return honchoToolError(err.Error()), nil
	}
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-profile] ✓ facts=%d", len(facts)), 0)
	b, _ := json.Marshal(map[string]any{"profile": facts})
	return string(b), nil
}

// handleHonchoSearch searches the user's session history for specific information.
// Uses Honcho's message list endpoint with a query filter — no LLM inference.
func handleHonchoSearch(ctx context.Context, argsJSON string) (string, error) {
	hClient := honchoClientFromCtx(ctx)
	if hClient == nil {
		return honchoToolError("honcho not configured"), nil
	}
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return honchoToolError("parse args: " + err.Error()), nil
	}
	if args.Query == "" {
		return honchoToolError("query is required"), nil
	}
	userID := userIDFromCtx(ctx)
	sessionID := sessionIDFromCtx(ctx)
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-search] query=%q (no LLM)", args.Query), 0)
	results, err := hClient.SearchHistory(ctx, userID, sessionID, args.Query)
	if err != nil {
		logger.Warn("tool", userID, sessionID, "[honcho-search] error: "+err.Error(), 0)
		return honchoToolError(err.Error()), nil
	}
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-search] ✓ results=%d", len(results)), 0)
	b, _ := json.Marshal(map[string]any{"results": results})
	return string(b), nil
}

// handleHonchoContext runs full dialectic reasoning about the user.
func handleHonchoContext(ctx context.Context, argsJSON string) (string, error) {
	hClient := honchoClientFromCtx(ctx)
	if hClient == nil {
		return honchoToolError("honcho not configured"), nil
	}
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return honchoToolError("parse args: " + err.Error()), nil
	}
	if args.Query == "" {
		return honchoToolError("query is required"), nil
	}
	userID := userIDFromCtx(ctx)
	sessionID := sessionIDFromCtx(ctx)
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-context] dialectic query=%q", args.Query), 0)
	content, err := hClient.Chat(ctx, userID, sessionID, args.Query)
	if err != nil {
		logger.Warn("tool", userID, sessionID, "[honcho-context] chat error: "+err.Error(), 0)
		return honchoToolError(err.Error()), nil
	}
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-context] ✓ result_len=%d", len(content)), 0)
	b, _ := json.Marshal(map[string]any{"context": content})
	return string(b), nil
}

// handleHonchoConclude writes a conclusion about the user into Honcho as an AI peer message.
func handleHonchoConclude(ctx context.Context, argsJSON string) (string, error) {
	hClient := honchoClientFromCtx(ctx)
	if hClient == nil {
		return honchoToolError("honcho not configured"), nil
	}
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return honchoToolError("parse args: " + err.Error()), nil
	}
	if args.Content == "" {
		return honchoToolError("content is required"), nil
	}
	userID := userIDFromCtx(ctx)
	sessionID := sessionIDFromCtx(ctx)
	logger.Debug("tool", userID, sessionID, fmt.Sprintf("[honcho-conclude] writing conclusion content_len=%d", len([]rune(args.Content))), 0)
	if err := hClient.AddMessage(ctx, userID, sessionID, false, "[conclusion] "+args.Content); err != nil {
		logger.Warn("tool", userID, sessionID, "[honcho-conclude] error: "+err.Error(), 0)
		return honchoToolError(err.Error()), nil
	}
	logger.Debug("tool", userID, sessionID, "[honcho-conclude] ✓ conclusion saved", 0)
	b, _ := json.Marshal(map[string]any{"ok": true})
	return string(b), nil
}

func honchoToolError(msg string) string {
	b, _ := json.Marshal(map[string]any{"error": msg})
	return string(b)
}

// HonchoTools returns the 4 Honcho tool schemas for use in LLM calls.
// Exported so agent.go can use them in flush/review calls.
func HonchoTools() []llm.Tool {
	return []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "honcho_profile",
				Description: "Structured facts about the user (name, role, preferences, style). Only call if '# Honcho Memory' is absent from the current context.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "honcho_search",
				Description: "Search user's past history for a specific fact. Use when the user references a previous session (\"last time...\", \"you mentioned...\") and the answer isn't in the current context.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "What to search for in the user's history"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "honcho_context",
				Description: "AI reasoning about the user across their full history — infers motivations, patterns, and preferences. Use when profile/search aren't enough. More expensive; use sparingly.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "Natural language question about the user"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "honcho_conclude",
				Description: "Save a conclusion about the user to Honcho. Use when you've inferred something meaningful about their preferences, habits, or context.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{"type": "string", "description": "The conclusion to save"},
					},
					"required": []string{"content"},
				},
			},
		},
	}
}
