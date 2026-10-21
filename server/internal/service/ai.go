package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AI 侧车客户端（Sprint 6）。
//
// 三条设计原则（面试会问「为什么 AI 是独立服务」）：
//  1. **生态**：大模型 SDK 在 Python 里最全，所以侧车用 FastAPI
//  2. **故障隔离**：这里用**独立的 http.Client 与超时**，
//     AI 再慢也只占用这一个客户端的连接，不会拖住聊天主链路
//  3. **可替换**：换模型/加 RAG 只改 Python，Go 侧只看到这个 HTTP 接口

const (
	// aiTimeout 调用侧车的整体超时。
	//
	// 取 25 秒：比侧车内部调用大模型的超时（20 秒）略大，
	// 让侧车有机会返回它自己的错误（比如"超时"），
	// 而不是我们这边先断掉、连原因都拿不到。
	aiTimeout = 25 * time.Second

	// aiMaxBody 读取响应的上限，防止异常响应把内存打爆。
	aiMaxBody = 1 << 20 // 1 MB
)

// AIClient 调用 Python 侧车。
type AIClient struct {
	baseURL string
	hc      *http.Client
}

// NewAIClient 构造 AIClient。baseURL 为空时返回 nil（表示未配置 AI）。
func NewAIClient(baseURL string) *AIClient {
	if baseURL == "" {
		return nil
	}
	return &AIClient{
		baseURL: baseURL,
		hc:      &http.Client{Timeout: aiTimeout},
	}
}

// AIResult 侧车返回结果。
type AIResult struct {
	Text string
	// Mode 侧车报告的模式：real / mock / disabled。
	// mock 表示没有配置 LLM_API_KEY，返回的是**标记过的模拟内容**。
	Mode string
}

// aiResponse 侧车响应体。
type aiResponse struct {
	Summary string `json:"summary"`
	Reply   string `json:"reply"`
	Mode    string `json:"mode"`
}

// Summarize 请侧车总结一段聊天记录。
func (c *AIClient) Summarize(ctx context.Context, messages []string) (*AIResult, error) {
	return c.call(ctx, "/summarize", messages, "summary")
}

// Reply 请侧车生成一条回复建议。
func (c *AIClient) Reply(ctx context.Context, messages []string) (*AIResult, error) {
	return c.call(ctx, "/reply", messages, "reply")
}

// Health 探测侧车是否可用。
func (c *AIClient) Health(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("AI 侧车不可达: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI 侧车 /health 返回 %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, aiMaxBody))
	if err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
		Mode   string `json:"mode"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return fmt.Sprintf("status=%s mode=%s model=%s", out.Status, out.Mode, out.Model), nil
}

// call 是 Summarize / Reply 的共同实现。
func (c *AIClient) call(ctx context.Context, path string, messages []string, field string) (*AIResult, error) {
	payload, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 超时 / 连接拒绝都走这里。**不要把底层错误原样返回给用户**，
		// 调用方会把它降级成一句友好提示。
		return nil, fmt.Errorf("调用 AI 侧车失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, aiMaxBody))
	if err != nil {
		return nil, fmt.Errorf("读取 AI 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// ⚠️ 只带状态码，不透传响应体 —— 里面可能含配置细节
		return nil, fmt.Errorf("AI 侧车返回 %d", resp.StatusCode)
	}

	var out aiResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("AI 响应格式错误: %w", err)
	}

	text := out.Summary
	if field == "reply" {
		text = out.Reply
	}
	// ⚠️ 必须 TrimSpace 之后再判空：侧车可能返回 "   "（只有空白），
	// 直接比较 == "" 会把它当成有效内容，界面上就会出现一个空气泡。
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("AI 返回了空内容")
	}

	mode := out.Mode
	if mode == "" {
		mode = "unknown"
	}
	return &AIResult{Text: text, Mode: mode}, nil
}
