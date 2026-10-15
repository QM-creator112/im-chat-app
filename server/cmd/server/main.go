// Command server 是 IM 后端的入口。
//
// 启动流程（顺序很重要）：
//
//	配置 → 日志 → MySQL → Redis → 装配依赖 → 注册路由 → 监听 → 优雅退出
//
// 任何一步失败都**立刻退出并给出明确原因** —— 而不是带着坏配置半死不活地跑。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/QM-creator112/im-chat-app/server/internal/cache"
	"github.com/QM-creator112/im-chat-app/server/internal/config"
	"github.com/QM-creator112/im-chat-app/server/internal/handler"
	"github.com/QM-creator112/im-chat-app/server/internal/middleware"
	"github.com/QM-creator112/im-chat-app/server/internal/service"
	"github.com/QM-creator112/im-chat-app/server/internal/store"
)

// version 由编译期注入：
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)" ./cmd/server
//
// 这样 /healthz 能报告线上跑的是哪一版，排查问题时非常有用。
var version = "dev"

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		// 用 slog.Error 而不是 fmt.Println：保证失败信息也带上时间戳
		slog.Error("服务启动失败", "err", err.Error())
		os.Exit(1)
	}
}

func run() error {
	startedAt := time.Now()

	// ---------- 1) 配置（最先，因为后面都依赖它）----------
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// ---------- 2) 日志 ----------
	setupLogger(cfg.IsProduction())
	slog.Info("配置加载完成", "env", cfg.Env, "port", cfg.Port, "version", version)

	// ---------- 3) MySQL ----------
	db, err := store.Open(cfg.DB.DSN())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	slog.Info("MySQL 连接成功", "host", cfg.DB.Host, "database", cfg.DB.Name)

	// ---------- 4) Redis ----------
	rdb, err := cache.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	slog.Info("Redis 连接成功", "addr", cfg.Redis.Addr)

	// ---------- 5) 装配依赖 ----------
	// 依赖方向：handler → service → store
	userStore := store.NewUserStore(db)
	deviceStore := store.NewDeviceStore(db)

	authSvc := service.NewAuthService(
		userStore, deviceStore,
		cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.JWT.RefreshTTL,
	)

	authH := handler.NewAuthHandler(authSvc)
	userH := handler.NewUserHandler(authSvc)
	// Sprint 1 还没有 WebSocket，连接数传 nil，/healthz 会跳过该字段
	healthH := handler.NewHealthHandler(db, rdb, version, nil)

	// ---------- 6) 路由 ----------
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	// 自定义请求日志：用 slog 而不是 gin 默认的文本日志，
	// 这样日志格式统一，便于 grep 与后续接入采集
	r.Use(requestLogger())

	registerRoutes(r, cfg, authH, userH, healthH)

	// ---------- 7) 监听 + 优雅退出 ----------
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// 在独立协程里跑，主协程负责等信号
	errCh := make(chan error, 1)
	go func() {
		printBanner(cfg)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("监听 %s 失败: %w", srv.Addr, err)
		}
	}()

	// 等 Ctrl+C 或 kill
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		slog.Info("收到退出信号，开始优雅退出", "signal", sig.String())
	}

	// 优雅退出：停止接收新请求，等在途请求处理完（最多 10 秒）
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("优雅退出超时: %w", err)
	}

	slog.Info("服务已退出", "运行时长", time.Since(startedAt).Round(time.Second).String())
	return nil
}

// registerRoutes 注册全部路由。
//
// 单独抽成函数的好处：**测试可以拿到同一份路由表**，
// 保证「测的就是跑的那份」，不会出现测试与线上路由不一致的情况。
func registerRoutes(
	r *gin.Engine,
	cfg *config.Config,
	authH *handler.AuthHandler,
	userH *handler.UserHandler,
	healthH *handler.HealthHandler,
) {
	// 健康检查：不需要鉴权，且不记访问日志（避免刷屏）
	r.GET("/healthz", healthH.Health)

	v1 := r.Group("/api/v1")

	// ---------- 公开接口 ----------
	auth := v1.Group("/auth")
	{
		auth.POST("/register", authH.Register)
		auth.POST("/login", authH.Login)
		auth.POST("/refresh", authH.Refresh)
	}

	// ---------- 需要鉴权的接口 ----------
	authed := v1.Group("")
	authed.Use(middleware.Auth(cfg.JWT.Secret))
	{
		authed.POST("/auth/logout", authH.Logout)

		authed.GET("/users/me", userH.Me)
		authed.GET("/users", userH.Search)
	}

	// Sprint 2 会在这里加：r.GET("/ws", wsHandler.Handle)
}

// setupLogger 配置结构化日志。
//
//	生产：JSON（便于机器解析与采集）
//	开发：文本（人眼友好）
func setupLogger(production bool) {
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if production {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

// requestLogger 记录每个请求，并把错误（由 c.Error 收集）打进日志。
//
// 注意：**不记录请求体与响应体** ——
// 里面可能有密码（注册/登录请求）。
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", latency.Milliseconds(),
			"ip", c.ClientIP(),
		}
		if query != "" {
			attrs = append(attrs, "query", query)
		}

		// 把 handler 里 c.Error() 收集到的错误一并打出来
		// —— 这样内部错误既不会泄露给客户端，又不会丢
		if errs := c.Errors.ByType(gin.ErrorTypePrivate); len(errs) > 0 {
			attrs = append(attrs, "error", errs.String())
			slog.Error("请求处理出错", attrs...)
			return
		}

		switch {
		case c.Writer.Status() >= 500:
			slog.Error("请求失败", attrs...)
		case c.Writer.Status() >= 400:
			slog.Warn("请求被拒绝", attrs...)
		case latency > 100*time.Millisecond:
			// 慢请求告警：索引或连接池问题的早期信号
			slog.Warn("慢请求", attrs...)
		default:
			slog.Info("请求完成", attrs...)
		}
	}
}

// printBanner 启动时打印可点击的地址。
//
// 这一小段能省掉大量「我该访问哪个地址」的困惑。
func printBanner(cfg *config.Config) {
	base := "http://127.0.0.1:" + cfg.Port
	fmt.Printf(`
  ──────────────  服务已启动  ──────────────
  环境        : %s
  版本        : %s
  本机访问    : %s/healthz
  接口前缀    : %s/api/v1
  停止服务    : Ctrl + C
  ─────────────────────────────────────────

`, cfg.Env, version, base, base)
}
