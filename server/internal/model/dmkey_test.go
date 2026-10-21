package model

import "testing"

// TestBuildDMKeyOrderIndependent 是单聊会话唯一性的核心保证。
//
// 如果 A 发起是 "1:2"、B 发起是 "2:1"，唯一索引就拦不住，
// 库里会出现两条指向同一对用户的单聊会话 —— 消息被分到两个会话里，
// 用户看到的是"聊天记录一半一半"。
func TestBuildDMKeyOrderIndependent(t *testing.T) {
	cases := []struct {
		a, b uint64
		want string
	}{
		{1, 2, "1:2"},
		{2, 1, "1:2"}, // 反过来必须得到同一个键
		{10, 3, "3:10"},
		{3, 10, "3:10"},
		{100, 100, "100:100"}, // 同一个 id（正常业务会拦住，但函数本身不该崩）
		{1, 18446744073709551615, "1:18446744073709551615"}, // uint64 上界
	}

	for _, c := range cases {
		if got := BuildDMKey(c.a, c.b); got != c.want {
			t.Errorf("BuildDMKey(%d, %d) = %q，期望 %q", c.a, c.b, got, c.want)
		}
	}
}

// TestBuildDMKeySymmetric 显式验证对称性：两个方向结果必须一致。
func TestBuildDMKeySymmetric(t *testing.T) {
	pairs := [][2]uint64{{1, 2}, {7, 3}, {999, 1000}, {5, 5}}
	for _, p := range pairs {
		fwd := BuildDMKey(p[0], p[1])
		rev := BuildDMKey(p[1], p[0])
		if fwd != rev {
			t.Errorf("非对称！BuildDMKey(%d,%d)=%q 但 (%d,%d)=%q",
				p[0], p[1], fwd, p[1], p[0], rev)
		}
	}
}
