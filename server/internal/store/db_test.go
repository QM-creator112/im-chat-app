package store

import (
	"testing"
	"time"
)

// 连接池参数的安全夹取。
//
// 这些用例来自一个真实教训：压测定位到的瓶颈就是 MaxOpenConns=25，
// 但把配置暴露成环境变量后，**配错的值比配小了更危险** ——
// database/sql 里 MaxOpenConns=0 表示"无限制"，
// 会把 MySQL 打到 max_connections 上限，比 25 还糟。
// 所以启动时必须把非法值夹回来，而不是照单全收。

func TestPoolOptionsDefaults(t *testing.T) {
	got := PoolOptions{}.withDefaults()

	if got.MaxOpenConns != DefaultMaxOpenConns {
		t.Errorf("MaxOpenConns 默认应为 %d，实际 %d", DefaultMaxOpenConns, got.MaxOpenConns)
	}
	if got.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("MaxIdleConns 默认应为 %d，实际 %d", DefaultMaxIdleConns, got.MaxIdleConns)
	}
	if got.ConnMaxLifetime != DefaultConnMaxLifetime {
		t.Errorf("ConnMaxLifetime 默认应为 %v，实际 %v", DefaultConnMaxLifetime, got.ConnMaxLifetime)
	}
	// 默认值必须和改造前完全一致，否则"没配环境变量"也算行为变更
	if DefaultMaxOpenConns != 25 || DefaultMaxIdleConns != 10 {
		t.Errorf("默认值被改动了：%d/%d（应为 25/10）", DefaultMaxOpenConns, DefaultMaxIdleConns)
	}
}

func TestPoolOptionsZeroIsNotUnlimited(t *testing.T) {
	// 0 在 database/sql 里是"无限制"，绝对不能透传下去
	got := PoolOptions{MaxOpenConns: 0, MaxIdleConns: 0, ConnMaxLifetime: 0}.withDefaults()
	if got.MaxOpenConns <= 0 {
		t.Errorf("MaxOpenConns 不能是 0（database/sql 会当成无限制），实际 %d", got.MaxOpenConns)
	}
}

func TestPoolOptionsNegativeClamped(t *testing.T) {
	got := PoolOptions{MaxOpenConns: -5, MaxIdleConns: -3, ConnMaxLifetime: -time.Minute}.withDefaults()
	if got.MaxOpenConns != DefaultMaxOpenConns {
		t.Errorf("负数应回落默认值，实际 %d", got.MaxOpenConns)
	}
	if got.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("负数应回落默认值，实际 %d", got.MaxIdleConns)
	}
}

func TestPoolOptionsIdleNotGreaterThanOpen(t *testing.T) {
	// 空闲连接比总连接还多是无意义的，database/sql 会静默压低，
	// 配置看着生效了其实没有
	got := PoolOptions{MaxOpenConns: 10, MaxIdleConns: 50}.withDefaults()
	if got.MaxIdleConns > got.MaxOpenConns {
		t.Errorf("MaxIdleConns(%d) 不应大于 MaxOpenConns(%d)", got.MaxIdleConns, got.MaxOpenConns)
	}
	if got.MaxIdleConns != 10 {
		t.Errorf("应被压到 10，实际 %d", got.MaxIdleConns)
	}
}

func TestPoolOptionsCappedAt1000(t *testing.T) {
	// 防手滑：DB_MAX_OPEN=100000 会直接打死 MySQL
	got := PoolOptions{MaxOpenConns: 100000}.withDefaults()
	if got.MaxOpenConns > 1000 {
		t.Errorf("上限应为 1000，实际 %d", got.MaxOpenConns)
	}
}

func TestPoolOptionsExplicitValueKept(t *testing.T) {
	got := PoolOptions{MaxOpenConns: 150, MaxIdleConns: 50, ConnMaxLifetime: 5 * time.Minute}.withDefaults()
	if got.MaxOpenConns != 150 || got.MaxIdleConns != 50 || got.ConnMaxLifetime != 5*time.Minute {
		t.Errorf("合法的显式值不应被改动，实际 %+v", got)
	}
}

func TestSuggestMaxOpenConnsInRange(t *testing.T) {
	n := SuggestMaxOpenConns()
	if n < 25 || n > 200 {
		t.Errorf("建议值应在 [25,200] 区间，实际 %d", n)
	}
}

func TestStatNilDBIsSafe(t *testing.T) {
	// /healthz 在 db 为 nil 时也要能响应（健康检查不能自己崩）
	if got := Stat(nil); got != (PoolStatus{}) {
		t.Errorf("nil db 应返回零值，实际 %+v", got)
	}
}
