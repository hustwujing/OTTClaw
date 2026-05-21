// Author:    Vijay
// Email:     hustwujing@163.com
// Date:      2026
// Copyright: Copyright (c) 2026 Vijay

// internal/langfuse/client.go — 异步批量 Langfuse HTTP 客户端
//
// 设计原则：
//   - Enqueue 非阻塞：queue 满时直接丢弃，不阻塞主路径
//   - 后台 goroutine 每 2 秒或积满 20 条时批量上报
//   - Flush() 同步等待当前 queue 全部上报完成（服务关闭时调用）
//   - Basic Auth：base64(publicKey + ":" + secretKey)
package langfuse

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	batchSize    = 20
	queueSize    = 512
	flushTick    = 2 * time.Second
	httpTimeout  = 10 * time.Second
	ingestPath   = "/api/public/ingestion"
)

// Client 向 Langfuse 异步批量上报 events。
type Client struct {
	baseURL   string
	authHeader string // "Basic <base64>"
	http      *http.Client

	queue    chan Event
	stopCh   chan struct{}
	flushReq chan chan struct{}
}

// NewClient 创建 Client 并启动后台上报 goroutine。
func NewClient(baseURL, publicKey, secretKey string) *Client {
	creds := base64.StdEncoding.EncodeToString([]byte(publicKey + ":" + secretKey))
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: "Basic " + creds,
		http:       &http.Client{Timeout: httpTimeout},
		queue:      make(chan Event, queueSize),
		stopCh:     make(chan struct{}),
		flushReq:   make(chan chan struct{}, 4),
	}
	go c.run()
	return c
}

// Enqueue 将 events 加入发送队列，若队列已满则静默丢弃（不阻塞调用方）。
func (c *Client) Enqueue(events ...Event) {
	for _, ev := range events {
		select {
		case c.queue <- ev:
		default:
			// 队列满：丢弃，不影响主路径
		}
	}
}

// Flush 同步等待队列清空并上报完成，适合在进程关闭时调用。
func (c *Client) Flush() {
	done := make(chan struct{})
	select {
	case c.flushReq <- done:
		<-done
	case <-time.After(5 * time.Second):
	}
}

// run 是后台 goroutine，负责定时或批量触发上报，以及响应同步 Flush 请求。
func (c *Client) run() {
	ticker := time.NewTicker(flushTick)
	defer ticker.Stop()

	buf := make([]Event, 0, batchSize)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		c.send(buf)
		buf = buf[:0]
	}

	for {
		select {
		case ev := <-c.queue:
			buf = append(buf, ev)
			if len(buf) >= batchSize {
				flush()
			}

		case <-ticker.C:
			flush()

		case done := <-c.flushReq:
			// 先排空 queue
		drain:
			for {
				select {
				case ev := <-c.queue:
					buf = append(buf, ev)
				default:
					break drain
				}
			}
			flush()
			close(done)

		case <-c.stopCh:
		drainStop:
			for {
				select {
				case ev := <-c.queue:
					buf = append(buf, ev)
				default:
					break drainStop
				}
			}
			flush()
			return
		}
	}
}

// send POST 一批 events 到 Langfuse /api/public/ingestion。
// 错误仅打印日志，不向调用方传播（观测性组件不应影响主流程）。
func (c *Client) send(events []Event) {
	body, err := json.Marshal(map[string]any{"batch": events})
	if err != nil {
		log.Printf("[langfuse] marshal error: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+ingestPath, bytes.NewReader(body))
	if err != nil {
		log.Printf("[langfuse] create request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[langfuse] send error: %v", err)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		log.Printf("[langfuse] ingest status=%d batch=%d body=%s", resp.StatusCode, len(events), string(respBody))
	} else {
		// 207 Multi-Status：检查 body 里是否有单条 event 的错误
		if bytes.Contains(respBody, []byte(`"errors"`)) {
			log.Printf("[langfuse] ingest partial error status=%d body=%s", resp.StatusCode, string(respBody))
		}
	}
}
