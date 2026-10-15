package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeBiz 记录被调用的次数与参数，用于验证 dispatch 的分发逻辑。
type fakeBiz struct {
	sendCalls int
	ackCalls  int
	readCalls int
	lastSend  SendData
	lastRead  ReadData
}

func (f *fakeBiz) OnSend(_ *Client, _ string, d SendData) { f.sendCalls++; f.lastSend = d }
func (f *fakeBiz) OnAck(_ *Client, _ AckData)             { f.ackCalls++ }
func (f *fakeBiz) OnRead(_ *Client, d ReadData)           { f.readCalls++; f.lastRead = d }

func newTestGateway(biz BusinessHandler) (*Gateway, *Hub) {
	h := NewHub(nil)
	return NewGateway(h, "test-secret-at-least-32-bytes-long!!", biz, nil), h
}

// ---------- dispatch 分发 ----------

func TestDispatchPingReturnsPong(t *testing.T) {
	g, h := newTestGateway(&fakeBiz{})
	c := testClient(h, 1, 8)
	h.register(c)

	g.dispatch(c, &Envelope{Type: TypePing})

	got := drainOne(c, 300*time.Millisecond)
	if got == nil {
		t.Fatal("ping 应当收到 pong")
	}
	if !strings.Contains(string(got), `"type":"pong"`) {
		t.Errorf("应当是 pong 消息，实际: %s", got)
	}
}

// TestDispatchUnknownTypeReturnsError 验证未知类型会明确报错。
//
// 为什么要报错而不是静默忽略：静默忽略会让客户端以为消息发出去了，
// 实际服务端根本没处理 —— 这类"看起来像网络问题"的 bug 极难排查。
func TestDispatchUnknownTypeReturnsError(t *testing.T) {
	g, h := newTestGateway(&fakeBiz{})
	c := testClient(h, 1, 8)
	h.register(c)

	g.dispatch(c, &Envelope{Type: "totally_unknown", ReqID: "r-9"})

	got := drainOne(c, 300*time.Millisecond)
	if got == nil {
		t.Fatal("未知类型应当收到错误信封")
	}
	s := string(got)
	if !strings.Contains(s, `"type":"error"`) {
		t.Errorf("应当是 error 消息，实际: %s", s)
	}
	if !strings.Contains(s, `"reqId":"r-9"`) {
		t.Errorf("错误信封应当回显 reqId，实际: %s", s)
	}
	if !strings.Contains(s, "未知的消息类型") {
		t.Errorf("错误信息应说明原因，实际: %s", s)
	}
}

func TestDispatchSendRequiresFields(t *testing.T) {
	biz := &fakeBiz{}
	g, h := newTestGateway(biz)
	c := testClient(h, 1, 8)
	h.register(c)

	// 缺 clientMsgId
	env, _ := Decode([]byte(`{"type":"send","reqId":"r1","data":{"conversationId":5,"content":"hi"}}`))
	g.dispatch(c, env)

	if biz.sendCalls != 0 {
		t.Error("参数不全时不应把请求转给业务层")
	}
	got := drainOne(c, 300*time.Millisecond)
	if got == nil || !strings.Contains(string(got), "clientMsgId") {
		t.Errorf("应回一个说明缺少 clientMsgId 的错误，实际: %s", got)
	}
}

func TestDispatchSendCallsBusiness(t *testing.T) {
	biz := &fakeBiz{}
	g, h := newTestGateway(biz)
	c := testClient(h, 1, 8)
	h.register(c)

	env, _ := Decode([]byte(`{
		"type":"send","reqId":"r1",
		"data":{"clientMsgId":"01JABC","conversationId":5,"contentType":1,"content":"你好"}
	}`))
	g.dispatch(c, env)

	if biz.sendCalls != 1 {
		t.Fatalf("业务层应被调用 1 次，实际 %d", biz.sendCalls)
	}
	if biz.lastSend.ClientMsgID != "01JABC" {
		t.Errorf("clientMsgId 传递错误: %q", biz.lastSend.ClientMsgID)
	}
	if biz.lastSend.ConversationID != 5 {
		t.Errorf("conversationId 传递错误: %d", biz.lastSend.ConversationID)
	}
	if biz.lastSend.Content != "你好" {
		t.Errorf("content 传递错误: %q", biz.lastSend.Content)
	}
}

func TestDispatchReadCallsBusiness(t *testing.T) {
	biz := &fakeBiz{}
	g, h := newTestGateway(biz)
	c := testClient(h, 1, 8)
	h.register(c)

	env, _ := Decode([]byte(`{"type":"read","data":{"conversationId":7,"readSeq":42}}`))
	g.dispatch(c, env)

	if biz.readCalls != 1 {
		t.Fatalf("read 应被调用 1 次，实际 %d", biz.readCalls)
	}
	if biz.lastRead.ReadSeq != 42 || biz.lastRead.ConversationID != 7 {
		t.Errorf("read 参数传递错误: %+v", biz.lastRead)
	}
}

func TestDispatchMalformedDataReturnsError(t *testing.T) {
	biz := &fakeBiz{}
	g, h := newTestGateway(biz)
	c := testClient(h, 1, 8)
	h.register(c)

	// data 的类型不对：conversationId 应是数字，给了字符串
	env, _ := Decode([]byte(`{"type":"read","data":{"conversationId":"abc"}}`))
	g.dispatch(c, env)

	if biz.readCalls != 0 {
		t.Error("data 格式错时不应转给业务层")
	}
	if got := drainOne(c, 300*time.Millisecond); got == nil {
		t.Error("data 格式错时应回错误信封")
	}
}

// TestNopBusinessHandlerRepliesNotImplemented 验证占位实现会明确回错。
//
// 静默不回会让客户端一直等 ack，看起来像网络问题。
func TestNopBusinessHandlerRepliesNotImplemented(t *testing.T) {
	g, h := newTestGateway(nil) // nil 会自动换成 NopBusinessHandler
	c := testClient(h, 1, 8)
	h.register(c)

	env, _ := Decode([]byte(`{"type":"send","reqId":"r1","data":{"clientMsgId":"x","conversationId":1}}`))
	g.dispatch(c, env)

	got := drainOne(c, 300*time.Millisecond)
	if got == nil {
		t.Fatal("占位实现应当回一个明确错误，而不是静默忽略")
	}
	if !strings.Contains(string(got), "尚未实现") {
		t.Errorf("应说明功能未实现，实际: %s", got)
	}
}

// ---------- Origin 校验 ----------

// TestCheckOriginStrictWhenConfigured 验证配了白名单就会拦跨站握手。
//
// ⚠️ 这条是安全项：WebSocket 不受浏览器同源策略保护，
// 不校验 Origin 的话，任何网站都能连上并冒充登录用户。
func TestCheckOriginStrictWhenConfigured(t *testing.T) {
	h := NewHub(nil)
	g := NewGateway(h, "s", nil, []string{"https://im.example.com"})

	mk := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}

	if !g.checkOrigin(mk("https://im.example.com")) {
		t.Error("白名单内的 Origin 应放行")
	}
	if g.checkOrigin(mk("https://evil.example.com")) {
		t.Error("白名单外的 Origin 必须拦截")
	}
	if !g.checkOrigin(mk("https://IM.EXAMPLE.COM")) {
		t.Error("Origin 比较应忽略大小写")
	}
	if !g.checkOrigin(mk("")) {
		t.Error("无 Origin（非浏览器客户端，如压测脚本）应放行")
	}
}

// TestCheckOriginPermissiveWhenNotConfigured 验证开发环境不校验。
func TestCheckOriginPermissiveWhenNotConfigured(t *testing.T) {
	h := NewHub(nil)
	g := NewGateway(h, "s", nil, nil)

	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	if !g.checkOrigin(r) {
		t.Error("未配白名单时应放行（开发环境）")
	}
}

// TestHandleRejectsMissingToken 验证无 token 的握手被拒绝。
func TestHandleRejectsMissingToken(t *testing.T) {
	h := NewHub(nil)
	g := NewGateway(h, "secret-at-least-32-bytes-long-aaaaaa", nil, nil)

	w := httptest.NewRecorder()
	c := newGinContext(w, "/ws")
	g.Handle(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("无 token 应返回 401，实际 %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "token") {
		t.Errorf("错误信息应提示需要 token，实际: %s", body)
	}
}

// TestHandleRejectsInvalidToken 验证无效 token 被拒绝。
func TestHandleRejectsInvalidToken(t *testing.T) {
	h := NewHub(nil)
	g := NewGateway(h, "secret-at-least-32-bytes-long-aaaaaa", nil, nil)

	w := httptest.NewRecorder()
	c := newGinContext(w, "/ws?token=garbage.token.value")
	g.Handle(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("无效 token 应返回 401，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "令牌无效") {
		t.Errorf("错误信息应说明令牌无效，实际: %s", w.Body.String())
	}
}

// ---------- 辅助 ----------

// newGinContext 造一个用于测试的 gin.Context。
//
// 用 gin.CreateTestContext 而不是真的起 HTTP 服务：
// 这样测试是纯内存的，毫秒级跑完，也不用占端口。
func newGinContext(w http.ResponseWriter, target string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c
}
