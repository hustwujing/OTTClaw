// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/honcho/client.go — Honcho AI-native memory platform HTTP client
//
// Honcho stores user history and uses dialectic reasoning to build per-user AI peer models.
// Each OTTClaw (userID, sessionID) pair maps to a Honcho (user, session) pair.
// IDs are cached in memory after first lookup to minimise API round-trips.
package honcho

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client wraps the Honcho REST API (v0).
type Client struct {
	baseURL string
	apiKey  string
	appName string

	http    *http.Client
	mu      sync.Mutex // protects appID first-init
	appID   string

	userIDs sync.Map // OTTClaw userID  → Honcho user  ID (string)
	sessIDs sync.Map // OTTClaw sessionID → Honcho session ID (string)
}

// NewClient creates a Honcho client.
// If appID is non-empty it is used directly (no get_or_create call needed).
func NewClient(baseURL, apiKey, appName, appID string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		appName: appName,
		appID:   appID, // may be "" — resolved lazily
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ---- App ----

// ensureApp resolves the Honcho App ID, initialising it once via get_or_create if needed.
func (c *Client) ensureApp(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appID != "" {
		return c.appID, nil
	}
	type req struct {
		Name string `json:"name"`
	}
	type resp struct {
		ID string `json:"id"`
	}
	var r resp
	if err := c.post(ctx, "/v0/apps/get_or_create", req{Name: c.appName}, &r); err != nil {
		return "", fmt.Errorf("honcho: get_or_create app %q: %w", c.appName, err)
	}
	c.appID = r.ID
	return c.appID, nil
}

// ---- User ----

// EnsureUser resolves the Honcho user ID for the given OTTClaw userID.
func (c *Client) EnsureUser(ctx context.Context, userID string) (string, error) {
	if v, ok := c.userIDs.Load(userID); ok {
		return v.(string), nil
	}
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return "", err
	}
	type req struct {
		Name string `json:"name"`
	}
	type resp struct {
		ID string `json:"id"`
	}
	var r resp
	if err := c.post(ctx, fmt.Sprintf("/v0/apps/%s/users/get_or_create", appID),
		req{Name: userID}, &r); err != nil {
		return "", fmt.Errorf("honcho: get_or_create user %q: %w", userID, err)
	}
	c.userIDs.Store(userID, r.ID)
	return r.ID, nil
}

// ---- Session ----

// EnsureSession resolves the Honcho session ID for the given OTTClaw sessionID.
// location_id is set to the OTTClaw sessionID so Honcho sessions map 1-to-1.
func (c *Client) EnsureSession(ctx context.Context, userID, sessionID string) (string, error) {
	if v, ok := c.sessIDs.Load(sessionID); ok {
		return v.(string), nil
	}
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return "", err
	}
	hUserID, err := c.EnsureUser(ctx, userID)
	if err != nil {
		return "", err
	}
	type req struct {
		LocationID string `json:"location_id"`
	}
	type resp struct {
		ID string `json:"id"`
	}
	var r resp
	if err := c.post(ctx,
		fmt.Sprintf("/v0/apps/%s/users/%s/sessions/get_or_create", appID, hUserID),
		req{LocationID: sessionID}, &r); err != nil {
		return "", fmt.Errorf("honcho: get_or_create session for user %q: %w", userID, err)
	}
	c.sessIDs.Store(sessionID, r.ID)
	return r.ID, nil
}

// ---- Messages ----

// AddMessage appends a message to the Honcho session.
// isUser=true for user messages, false for AI (assistant) messages.
func (c *Client) AddMessage(ctx context.Context, userID, sessionID string, isUser bool, content string) error {
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return err
	}
	hUserID, err := c.EnsureUser(ctx, userID)
	if err != nil {
		return err
	}
	hSessID, err := c.EnsureSession(ctx, userID, sessionID)
	if err != nil {
		return err
	}
	type req struct {
		IsUser  bool   `json:"is_user"`
		Content string `json:"content"`
	}
	return c.post(ctx,
		fmt.Sprintf("/v0/apps/%s/users/%s/sessions/%s/messages", appID, hUserID, hSessID),
		req{IsUser: isUser, Content: content}, nil)
}

// ---- Dialectic / Chat ----

// Chat sends a natural-language query to Honcho's dialectic reasoning engine for this user.
// Returns the AI-inferred response about the user.
func (c *Client) Chat(ctx context.Context, userID, sessionID, query string) (string, error) {
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return "", err
	}
	hUserID, err := c.EnsureUser(ctx, userID)
	if err != nil {
		return "", err
	}
	hSessID, err := c.EnsureSession(ctx, userID, sessionID)
	if err != nil {
		return "", err
	}
	type queryItem struct {
		Content string `json:"content"`
	}
	type req struct {
		Queries []queryItem `json:"queries"`
	}
	type resp struct {
		Content string `json:"content"`
	}
	var r resp
	if err := c.post(ctx,
		fmt.Sprintf("/v0/apps/%s/users/%s/sessions/%s/chat", appID, hUserID, hSessID),
		req{Queries: []queryItem{{Content: query}}}, &r); err != nil {
		return "", fmt.Errorf("honcho: chat: %w", err)
	}
	return r.Content, nil
}

// ---- No-LLM retrieval ----

// GetUserFacts retrieves stored observations about the user from Honcho metamessages.
// No LLM is invoked — this is a pure data retrieval operation.
func (c *Client) GetUserFacts(ctx context.Context, userID string) ([]string, error) {
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return nil, err
	}
	hUserID, err := c.EnsureUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/v0/apps/%s/users/%s/metamessages?filter[is_user]=true&reverse=true&page_size=20",
		appID, hUserID)
	type item struct {
		Content string `json:"content"`
	}
	type resp struct {
		Items []item `json:"items"`
	}
	var r resp
	if err := c.get(ctx, path, &r); err != nil {
		return nil, fmt.Errorf("honcho: get user facts: %w", err)
	}
	out := make([]string, 0, len(r.Items))
	for _, m := range r.Items {
		if m.Content != "" {
			out = append(out, m.Content)
		}
	}
	return out, nil
}

// SearchHistory retrieves messages from the user's session matching the given query.
// Uses Honcho's message list endpoint with a search filter — no LLM inference.
func (c *Client) SearchHistory(ctx context.Context, userID, sessionID, query string) ([]string, error) {
	appID, err := c.ensureApp(ctx)
	if err != nil {
		return nil, err
	}
	hUserID, err := c.EnsureUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	hSessID, err := c.EnsureSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/v0/apps/%s/users/%s/sessions/%s/messages?reverse=true&page_size=10&q=%s",
		appID, hUserID, hSessID, url.QueryEscape(query))
	type item struct {
		Content string `json:"content"`
	}
	type resp struct {
		Items []item `json:"items"`
	}
	var r resp
	if err := c.get(ctx, path, &r); err != nil {
		return nil, fmt.Errorf("honcho: search history: %w", err)
	}
	out := make([]string, 0, len(r.Items))
	for _, m := range r.Items {
		if m.Content != "" {
			out = append(out, m.Content)
		}
	}
	return out, nil
}

// ---- HTTP helper ----

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
