package ws

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload := PushData{
		ConversationID: 55,
		Seq:            42,
		SenderID:       1,
		SenderName:     "邱漫",
		ContentType:    1,
		Content:        "明天三点见 😀",
		CreatedAt:      1791197597369,
	}

	raw, err := Encode(TypePush, "req-1", payload)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}

	env, err := Decode(raw)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if env.Type != TypePush {
		t.Errorf("type 应为 %s，实际 %s", TypePush, env.Type)
	}
	if env.ReqID != "req-1" {
		t.Errorf("reqId 应为 req-1，实际 %s", env.ReqID)
	}

	got, err := DecodeData[PushData](env)
	if err != nil {
		t.Fatalf("解析 data 失败: %v", err)
	}
	if got != payload {
		t.Errorf("往返后数据不一致:\n  发出: %+v\n  收到: %+v", payload, got)
	}
}

// TestEncodePreservesUnicodeAndEmoji 验证中文与 emoji 不被转义坏掉。
//
// 这条很实际：聊天里一定有 emoji，如果编码环节把非 ASCII 弄坏，
// 用户会看到乱码。
func TestEncodePreservesUnicodeAndEmoji(t *testing.T) {
	raw, err := Encode(TypePush, "", PushData{Content: "测试😀中文"})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := Decode(raw)
	got, _ := DecodeData[PushData](env)
	if got.Content != "测试😀中文" {
		t.Errorf("内容应原样保留，实际 %q", got.Content)
	}
}

// TestErrorPayloadIsValidJSON 验证错误信封永远是合法 JSON。
//
// 为什么重要：错误信封用于"报告错误"，如果它自己就格式错，
// 客户端会因为 JSON.parse 失败而彻底失去线索 —— 连错误原因都看不到。
func TestErrorPayloadIsValidJSON(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		message string
		reqID   string
	}{
		{"普通", 1001, "参数错误", "r-1"},
		{"空 reqId", 5000, "服务器内部错误", ""},
		{"含引号", 1001, `他说"你好"`, "r-2"},
		{"含换行", 1001, "第一行\n第二行", "r-3"},
		{"含 emoji", 1001, "出错了😱", "r-4"},
	}

	for _, c := range cases {
		raw := ErrorPayload(c.code, c.message, c.reqID)

		var parsed Envelope
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Errorf("[%s] 错误信封不是合法 JSON: %v (%s)", c.name, err, raw)
			continue
		}
		if parsed.Type != TypeError {
			t.Errorf("[%s] type 应为 error，实际 %s", c.name, parsed.Type)
		}

		var data ErrorData
		if err := json.Unmarshal(parsed.Data, &data); err != nil {
			t.Errorf("[%s] data 解析失败: %v", c.name, err)
			continue
		}
		if data.Code != c.code {
			t.Errorf("[%s] code 应为 %d，实际 %d", c.name, c.code, data.Code)
		}
		if data.Message != c.message {
			t.Errorf("[%s] message 应原样保留，实际 %q", c.name, data.Message)
		}
	}
}

func TestPongPayload(t *testing.T) {
	raw := PongPayload()
	env, err := Decode(raw)
	if err != nil {
		t.Fatalf("pong 不是合法 JSON: %v", err)
	}
	if env.Type != TypePong {
		t.Errorf("type 应为 pong，实际 %s", env.Type)
	}
}

// TestMustEncodeNeverReturnsInvalidJSON 验证 MustEncode 的兜底。
func TestMustEncodeNeverReturnsInvalidJSON(t *testing.T) {
	// channel 无法被 json 编码 —— 模拟"编码失败"
	raw := MustEncode(TypePush, "req-x", make(chan int))

	var parsed Envelope
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("MustEncode 在编码失败时应返回合法的 error 信封，实际: %v (%s)", err, raw)
	}
	if parsed.Type != TypeError {
		t.Errorf("编码失败时应退化成 error 信封，实际 type=%s", parsed.Type)
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	for _, s := range []string{"", "not json", "{}", `{"type":}`, strings.Repeat("x", 100)} {
		// "{}" 是合法 JSON（空信封），不应报错
		_, err := Decode([]byte(s))
		if s == "{}" && err != nil {
			t.Errorf("空对象应是合法的空信封，实际报错: %v", err)
		}
		if s != "{}" && err == nil {
			t.Errorf("非法输入 %q 应当报错", s)
		}
	}
}

// TestDecodeDataEmptyPayload 验证 data 缺失时返回零值而不报错。
//
// 像 ping/pong 这类消息本来就没有 data，不能因为缺 data 就报错。
func TestDecodeDataEmptyPayload(t *testing.T) {
	env := &Envelope{Type: TypePing}
	got, err := DecodeData[SendData](env)
	if err != nil {
		t.Errorf("缺 data 不应报错，实际: %v", err)
	}
	if got.ConversationID != 0 || got.ClientMsgID != "" {
		t.Errorf("应返回零值，实际 %+v", got)
	}
}
