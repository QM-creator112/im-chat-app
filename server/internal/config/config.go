// Package config 从环境变量加载配置。
//
// 设计要点：代码只认环境变量，不认配置文件路径。
//   - 开发时由 godotenv 从 .env 加载
//   - 生产时由 systemd 的 EnvironmentFile 注入
//     → 同一份代码，两个环境都不用改
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config 是应用的全部配置。
type Config struct {
	Env   string
	Port  string
	DB    DBConfig
	Redis RedisConfig
	JWT   JWTConfig
	AI    AIConfig
}

// DBConfig 数据库配置。
type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

// DSN 拼出 MySQL 连接串。
//
// parseTime=true 让 DATETIME 字段自动解析成 time.Time（否则会拿到 []byte）；
// loc=Local 使用本地时区；
// charset=utf8mb4 保证 emoji 能正常读写。
func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.Name,
	)
}

// RedisConfig Redis 配置。
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// JWTConfig 令牌配置。
type JWTConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// AIConfig AI 侧车配置。
type AIConfig struct {
	BaseURL string
}

// Load 读取并校验配置。
//
// 关键设计：**启动时就校验关键配置，缺了直接报错退出**。
// 这比"运行到一半才 panic"好得多 —— 部署时能立刻发现问题。
func Load() (*Config, error) {
	// .env 不存在不算错误（生产环境用环境变量注入）
	_ = godotenv.Load()
	_ = godotenv.Load(".env.local")

	cfg := &Config{
		Env:  getEnv("APP_ENV", "development"),
		Port: getEnv("APP_PORT", "8080"),
		DB: DBConfig{
			Host:     getEnv("DB_HOST", "127.0.0.1"),
			Port:     getEnv("DB_PORT", "3306"),
			User:     getEnv("DB_USER", ""),
			Password: getEnv("DB_PASSWORD", ""),
			Name:     getEnv("DB_NAME", "im_chat"),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		JWT: JWTConfig{
			Secret:     getEnv("JWT_SECRET", ""),
			AccessTTL:  time.Duration(getEnvInt("JWT_ACCESS_TTL_MINUTES", 30)) * time.Minute,
			RefreshTTL: time.Duration(getEnvInt("JWT_REFRESH_TTL_DAYS", 14)) * 24 * time.Hour,
		},
		AI: AIConfig{
			BaseURL: getEnv("AI_BASE_URL", ""),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 校验必填项。
//
// 注意错误信息要**明确告诉用户缺什么、去哪补**，而不是只说"配置错误"。
func (c *Config) validate() error {
	var missing []string

	if c.DB.User == "" {
		missing = append(missing, "DB_USER")
	}
	if c.DB.Password == "" {
		missing = append(missing, "DB_PASSWORD")
	}
	if c.JWT.Secret == "" {
		missing = append(missing, "JWT_SECRET")
	}

	if len(missing) > 0 {
		return fmt.Errorf(
			"缺少必需的环境变量: %s\n请复制 .env.example 为 .env 并填入真实值",
			strings.Join(missing, ", "),
		)
	}

	// JWT 密钥太短会造成安全风险，这里强制拦住
	// （HS256 的密钥短于 32 字节时，暴力破解的可行度显著上升）
	if len(c.JWT.Secret) < 32 {
		return errors.New("JWT_SECRET 长度不足 32 字节，请重新生成（见 .env.example 里的生成方法）")
	}

	return nil
}

// IsProduction 判断是否生产环境。
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// ---------- 内部工具 ----------

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
