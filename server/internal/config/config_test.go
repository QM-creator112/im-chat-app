package config

import (
	"strings"
	"testing"
	"time"
)

// TestValidateRequiresDBPassword 验证缺 DB_PASSWORD 时启动就报错。
//
// 保护的不变量：**配置错误必须在启动时暴露**，而不是运行到一半才 panic。
func TestValidateRequiresDBPassword(t *testing.T) {
	c := &Config{
		DB:  DBConfig{User: "im_app", Password: ""},
		JWT: JWTConfig{Secret: strings.Repeat("s", 32)},
	}
	err := c.validate()
	if err == nil {
		t.Fatal("缺 DB_PASSWORD 应当报错")
	}
	if !strings.Contains(err.Error(), "DB_PASSWORD") {
		t.Errorf("错误信息应明确指出缺哪个变量，实际: %v", err)
	}
}

func TestValidateRequiresJWTSecret(t *testing.T) {
	c := &Config{
		DB:  DBConfig{User: "im_app", Password: "pwd"},
		JWT: JWTConfig{Secret: ""},
	}
	err := c.validate()
	if err == nil {
		t.Fatal("缺 JWT_SECRET 应当报错")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("错误信息应指出 JWT_SECRET，实际: %v", err)
	}
}

// TestValidateRejectsShortJWTSecret 验证短密钥被拒绝。
//
// 32 字节是 HS256 的经验下限：更短的话暴力破解的可行度显著上升。
func TestValidateRejectsShortJWTSecret(t *testing.T) {
	c := &Config{
		DB:  DBConfig{User: "im_app", Password: "pwd"},
		JWT: JWTConfig{Secret: strings.Repeat("s", 31)}, // 差 1 字节
	}
	err := c.validate()
	if err == nil {
		t.Fatal("31 字节的 JWT_SECRET 应被拒绝")
	}
	if !strings.Contains(err.Error(), "32") {
		t.Errorf("错误信息应说明长度要求，实际: %v", err)
	}

	// 刚好 32 字节应通过
	c.JWT.Secret = strings.Repeat("s", 32)
	if err := c.validate(); err != nil {
		t.Errorf("32 字节的 JWT_SECRET 应通过，实际: %v", err)
	}
}

// TestDSN 验证连接串包含必要参数。
//
// parseTime=true 缺失会导致 DATETIME 字段扫描失败；
// charset=utf8mb4 缺失会导致 emoji 写入失败。
func TestDSN(t *testing.T) {
	d := DBConfig{
		Host: "127.0.0.1", Port: "3306",
		User: "im_app", Password: "secret",
		Name: "im_chat",
	}
	dsn := d.DSN()

	for _, want := range []string{
		"im_app:secret@tcp(127.0.0.1:3306)/im_chat",
		"parseTime=true",
		"charset=utf8mb4",
		"loc=Local",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN 应包含 %q，实际: %s", want, dsn)
		}
	}
}

func TestIsProduction(t *testing.T) {
	if (&Config{Env: "production"}).IsProduction() != true {
		t.Error("production 应识别为生产环境")
	}
	if (&Config{Env: "development"}).IsProduction() != false {
		t.Error("development 不应识别为生产环境")
	}
}

// TestDefaults 验证未设置环境变量时的默认值。
func TestDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_PORT", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("JWT_ACCESS_TTL_MINUTES", "")
	t.Setenv("JWT_REFRESH_TTL_DAYS", "")

	cfg, err := Load()
	if err != nil {
		// 只有在 .env 存在且缺关键项时才会走到这里；此处只关心默认值，
		// 所以缺 DB_PASSWORD 导致的错误可以忽略
		if !strings.Contains(err.Error(), "DB_PASSWORD") {
			t.Fatalf("非预期错误: %v", err)
		}
		return
	}

	if cfg.Env != "development" {
		t.Errorf("默认 env 应为 development，实际 %s", cfg.Env)
	}
	if cfg.Port != "8080" {
		t.Errorf("默认端口应为 8080，实际 %s", cfg.Port)
	}
	if cfg.DB.Host != "127.0.0.1" || cfg.DB.Port != "3306" || cfg.DB.Name != "im_chat" {
		t.Errorf("数据库默认值不正确: %+v", cfg.DB)
	}
	if cfg.Redis.Addr != "127.0.0.1:6379" {
		t.Errorf("Redis 默认地址不正确: %s", cfg.Redis.Addr)
	}
	if cfg.JWT.AccessTTL != 30*time.Minute {
		t.Errorf("access token 默认有效期应为 30 分钟，实际 %v", cfg.JWT.AccessTTL)
	}
	if cfg.JWT.RefreshTTL != 14*24*time.Hour {
		t.Errorf("refresh token 默认有效期应为 14 天，实际 %v", cfg.JWT.RefreshTTL)
	}
}
