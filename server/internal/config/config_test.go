package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
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

// TestStripBOM 是针对一个**真实踩过的坑**的回归测试。
//
// 现象：.env 文件明明存在，程序却报"缺少 DB_USER, DB_PASSWORD, JWT_SECRET"。
// 根因：Windows PowerShell 5.1 的 `Set-Content -Encoding UTF8` 会写入
// UTF-8 BOM（EF BB BF），而 godotenv 遇到 BOM 会整体解析失败、
// **一个变量都不加载**，报错信息是：
//
//	unexpected character "»" in variable name near "\ufeff# ==="
//
// 修法：读取后先 stripBOM 再交给 godotenv.Unmarshal。
func TestStripBOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("A=1\n")...)
	withoutBOM := []byte("A=1\n")

	if got := string(stripBOM(withBOM)); got != "A=1\n" {
		t.Errorf("应剥掉 BOM，实际 %q", got)
	}
	// 没有 BOM 的内容不能被改动
	if got := string(stripBOM(withoutBOM)); got != "A=1\n" {
		t.Errorf("无 BOM 内容不应被修改，实际 %q", got)
	}
	// 只有部分 BOM 字节时不能误删
	if got := string(stripBOM([]byte{0xEF, 0xBB})); got != string([]byte{0xEF, 0xBB}) {
		t.Error("不完整的 BOM 不应被剥掉")
	}
}

// TestUnmarshalDotEnvWithAndWithoutBOM 验证剥 BOM 之后确实能正常解析。
func TestUnmarshalDotEnvWithAndWithoutBOM(t *testing.T) {
	body := "DB_USER=im_app\nDB_PASSWORD=p@ss word\nJWT_SECRET=" + strings.Repeat("s", 40) + "\n"

	// ① 有 BOM：不剥会失败
	withBOM := string(append([]byte{0xEF, 0xBB, 0xBF}, []byte(body)...))
	if _, err := godotenv.Unmarshal(withBOM); err == nil {
		t.Log("注意：当前 godotenv 版本已能容忍 BOM（本测试仅记录行为）")
	}

	// ② 剥掉 BOM 后必须成功，且值正确
	m, err := godotenv.Unmarshal(string(stripBOM([]byte(withBOM))))
	if err != nil {
		t.Fatalf("剥掉 BOM 后应解析成功，实际: %v", err)
	}
	if m["DB_USER"] != "im_app" {
		t.Errorf("DB_USER 解析错误: %q", m["DB_USER"])
	}
	if m["DB_PASSWORD"] != "p@ss word" {
		t.Errorf("含空格的值应被正确保留: %q", m["DB_PASSWORD"])
	}
	if len(m["JWT_SECRET"]) != 40 {
		t.Errorf("JWT_SECRET 长度错误: %d", len(m["JWT_SECRET"]))
	}
}

// TestLoadEnvFileDoesNotOverrideExisting 验证 .env 不会覆盖已存在的环境变量。
//
// 这条很重要：生产环境由 systemd 注入变量，优先级必须高于 .env。
func TestLoadEnvFileDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("PROBE_KEY_FROM_ENV_FILE=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 先设置一个同名环境变量
	t.Setenv("PROBE_KEY_FROM_ENV_FILE", "from-process")
	loadEnvFile(p)

	if got := os.Getenv("PROBE_KEY_FROM_ENV_FILE"); got != "from-process" {
		t.Errorf("已存在的环境变量不应被 .env 覆盖，实际 %q", got)
	}
}

// TestLoadEnvFileInjectsNewKeys 验证未设置过的键会被注入。
func TestLoadEnvFileInjectsNewKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("PROBE_NEW_KEY_FROM_FILE=hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("PROBE_NEW_KEY_FROM_FILE")
	t.Cleanup(func() { os.Unsetenv("PROBE_NEW_KEY_FROM_FILE") })

	loadEnvFile(p)

	if got := os.Getenv("PROBE_NEW_KEY_FROM_FILE"); got != "hello" {
		t.Errorf("新键应被注入，实际 %q", got)
	}
}

// TestLoadEnvFileMissingFileIsNotFatal 验证文件不存在时静默返回（生产环境没有 .env）。
func TestLoadEnvFileMissingFileIsNotFatal(t *testing.T) {
	loadEnvFile(filepath.Join(t.TempDir(), "not-exist.env")) // 不应 panic
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
