package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// HealthHandler 健康检查。
//
// 为什么需要它：
//  1. 部署后（第 6 篇）用它判断服务是否真的起来了
//  2. systemd / Nginx 可以用它做存活探测
//  3. **压测时可以直接从浏览器看当前连接数**，不用另开工具
type HealthHandler struct {
	db      *sql.DB
	rdb     *redis.Client
	version string
	started time.Time
	// online 返回当前 WebSocket 连接数。
	// 用函数注入而不是直接依赖 ws 包，避免 Sprint 1 阶段就引入未完成的模块。
	online func() int
}

// NewHealthHandler 构造 HealthHandler。
func NewHealthHandler(db *sql.DB, rdb *redis.Client, version string, online func() int) *HealthHandler {
	return &HealthHandler{
		db:      db,
		rdb:     rdb,
		version: version,
		started: time.Now(),
		online:  online,
	}
}

// Health 处理 GET /healthz
func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := gin.H{
		"mysql": h.checkMySQL(ctx),
		"redis": h.checkRedis(ctx),
	}
	status := "ok"
	if checks["mysql"] != "ok" || checks["redis"] != "ok" {
		status = "degraded"
	}
	if h.online != nil {
		checks["wsConnections"] = h.online()
	}

	c.JSON(http.StatusOK, Response{
		Code:    CodeOK,
		Message: "ok",
		Data: gin.H{
			"status":        status,
			"version":       h.version,
			"uptimeSeconds": int64(time.Since(h.started).Seconds()),
			"checks":        checks,
		},
	})
}

func (h *HealthHandler) checkMySQL(ctx context.Context) string {
	if h.db == nil {
		return "not_configured"
	}
	if err := h.db.PingContext(ctx); err != nil {
		return "error"
	}
	return "ok"
}

func (h *HealthHandler) checkRedis(ctx context.Context) string {
	if h.rdb == nil {
		return "not_configured"
	}
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		return "error"
	}
	return "ok"
}
