package store

import (
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestDuplicateIndex 验证能从 MySQL 1062 错误里正确识别冲突的索引名。
//
// 这个能力决定了**幂等命中**与**真 bug** 能不能被区分开：
//
//   - uk_sender_client 冲突 → 这条消息已经存在，属幂等，应当返回旧记录
//   - uk_conv_seq      冲突 → seq 重复，说明并发控制失效，是必须报警的 bug
//
// 如果把两者混为一谈（"撞了就当幂等成功"），真 bug 会被伪装成正常，
// 长期潜伏在生产里。
func TestDuplicateIndex(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "幂等键冲突",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '1-01JABC' for key 'messages.uk_sender_client'"},
			want: "uk_sender_client",
		},
		{
			name: "seq 冲突（真 bug 信号）",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '55-42' for key 'messages.uk_conv_seq'"},
			want: "uk_conv_seq",
		},
		{
			name: "单聊会话冲突",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '1:2' for key 'conversations.uk_dm_key'"},
			want: "uk_dm_key",
		},
		{
			name: "用户名冲突",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'test001' for key 'users.uk_username'"},
			want: "uk_username",
		},
		{
			name: "无索引名的 1062",
			err:  &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x'"},
			want: "",
		},
		{
			name: "其它 MySQL 错误",
			err:  &mysql.MySQLError{Number: 1146, Message: "Table 'im_chat.messages' doesn't exist"},
			want: "",
		},
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "普通 error",
			err:  errors.New("connection refused"),
			want: "",
		},
	}

	for _, c := range cases {
		if got := DuplicateIndex(c.err); got != c.want {
			t.Errorf("[%s] DuplicateIndex() = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestIsDuplicateKey 验证 1062 判定。
func TestIsDuplicateKey(t *testing.T) {
	if !IsDuplicateKey(&mysql.MySQLError{Number: 1062, Message: "dup"}) {
		t.Error("1062 应判定为唯一索引冲突")
	}
	if IsDuplicateKey(errors.New("其它错误")) {
		t.Error("普通错误不该判定为唯一索引冲突")
	}
	if IsDuplicateKey(nil) {
		t.Error("nil 不该判定为冲突")
	}
}
