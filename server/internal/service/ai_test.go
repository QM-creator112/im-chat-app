package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Sprint 6：AI 侧车客户端与 @AI 触发的单元测试。
//
// 用 httptest 起一个假侧车，**不依赖真实的 Python 服务**，
// 这样能精确验证超时、错误映射、响应解析这些边界。

// newAIClientWithTimeout 测试用：可指定超时，便于快速验证超时路径。
func newAIClientWithTimeout(baseURL string, timeout time.Duration) *AIClient {
	return &AIClient{baseURL: baseURL, hc: &http.Client{Timeout: timeout}}
}

func TestNewAIClientNilWhenEmptyURL(t *testing.T) {
	if NewAIClient("") != nil {
		t.Error("baseURL 为空时应返回 nil，调用方据此走降级")
	}
	if NewAIClient("http://127.0.0.1:8090") == nil {
		t.Error("有 baseURL 时应返回非 nil")
	}
}

func TestAIClientSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summarize" {
			t.Errorf("路径应为 /summarize，实际 %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("方法应为 POST，实际 %s", r.Method)
		}
		var body struct {
			Messages []string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("请求体解析失败: %v", err)
		}
		if len(body.Messages) != 2 {
			t.Errorf("应收到 2 条消息，实际 %d", len(body.Messages))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"summary": "这是总结",
			"mode":    "mock",
		})
	}))
	defer srv.Close()

	c := NewAIClient(srv.URL)
	res, err := c.Summarize(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if res.Text != "这是总结" {
		t.Errorf("总结内容不对: %q", res.Text)
	}
	if res.Mode != "mock" {
		t.Errorf("mode 应为 mock，实际 %q", res.Mode)
	}
}

func TestAIClientReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"reply": "好的", "mode": "real"})
	}))
	defer srv.Close()

	res, err := NewAIClient(srv.URL).Reply(context.Background(), []string{"hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "好的" {
		t.Errorf("回复内容不对: %q", res.Text)
	}
}

// TestAIClientTimeout 超时必须被处理成错误，而不是无限等待。
//
// 这是「故障隔离」的关键：AI 卡住时，Go 侧必须能及时放弃，
// 否则 @AI 的 goroutine 会一直挂着。
func TestAIClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]string{"summary": "太慢了"})
	}))
	defer srv.Close()

	c := newAIClientWithTimeout(srv.URL, 100*time.Millisecond)
	start := time.Now()
	_, err := c.Summarize(context.Background(), []string{"a"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("超时应当返回错误")
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("应在 100ms 左右超时，实际耗时 %v", elapsed)
	}
}

// TestAIClientErrorStatusDoesNotLeakBody 侧车返回非 200 时，
// 错误信息里**不能带响应体**（可能含配置细节/额度信息）。
func TestAIClientErrorStatusDoesNotLeakBody(t *testing.T) {
	const secretBody = `{"detail":"未配置 LLM_API_KEY，sk-very-secret-12345"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(secretBody))
	}))
	defer srv.Close()

	_, err := NewAIClient(srv.URL).Summarize(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("503 应当返回错误")
	}
	if strings.Contains(err.Error(), "sk-very-secret") || strings.Contains(err.Error(), "detail") {
		t.Errorf("错误信息泄露了响应体内容: %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("错误信息应包含状态码，实际: %v", err)
	}
}

func TestAIClientEmptyContentIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"summary": "   ", "mode": "real"})
	}))
	defer srv.Close()

	if _, err := NewAIClient(srv.URL).Summarize(context.Background(), []string{"a"}); err == nil {
		t.Error("空内容应当返回错误，以便走降级")
	}
}

func TestAIClientMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("这不是 JSON"))
	}))
	defer srv.Close()

	if _, err := NewAIClient(srv.URL).Summarize(context.Background(), []string{"a"}); err == nil {
		t.Error("非法 JSON 应当返回错误")
	}
}

func TestAIClientUnreachable(t *testing.T) {
	// 用一个几乎不可能有服务的端口
	c := newAIClientWithTimeout("http://127.0.0.1:59998", 300*time.Millisecond)
	if _, err := c.Summarize(context.Background(), []string{"a"}); err == nil {
		t.Error("服务不可达时应当返回错误（调用方据此降级）")
	}
}

func TestAIClientHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok", "mode": "mock", "model": "deepseek-chat",
		})
	}))
	defer srv.Close()

	info, err := NewAIClient(srv.URL).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info, "mode=mock") {
		t.Errorf("健康信息应包含 mode，实际: %s", info)
	}
}

// ---------------------------------------------------------------------------
// @AI 触发解析
// ---------------------------------------------------------------------------

func TestParseAICommand(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantOk  bool
		want    AIKind
	}{
		{"默认总结", "@AI", true, AIKindSummary},
		{"总结关键字", "@AI 总结一下", true, AIKindSummary},
		{"概括也算总结", "@AI 概括这段对话", true, AIKindSummary},
		{"回复关键字", "@AI 帮我回复", true, AIKindReply},
		{"reply 英文", "@AI reply", true, AIKindReply},
		{"大小写不敏感", "@ai 总结", true, AIKindSummary},
		{"前面有空格", "   @AI 总结", true, AIKindSummary},
		{"正文里提到 @AI 不触发", "我昨天@AI了一下", false, ""},
		{"普通消息", "你好", false, ""},
		{"只有 @", "@", false, ""},
		{"@AI 开头但不是触发词也按总结", "@AI 随便说点什么", true, AIKindSummary},
		{"空内容", "", false, ""},
	}

	for _, c := range cases {
		kind, ok := parseAICommand(c.content)
		if ok != c.wantOk {
			t.Errorf("[%s] parseAICommand(%q) ok=%v，期望 %v", c.name, c.content, ok, c.wantOk)
			continue
		}
		if ok && kind != c.want {
			t.Errorf("[%s] kind=%v，期望 %v", c.name, kind, c.want)
		}
	}
}
