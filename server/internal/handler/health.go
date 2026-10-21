package handler

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"runtime"
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
			"runtime":       runtimeSnapshot(),
		},
	})
}

// runtimeSnapshot 暴露服务端自身的运行时指标。
//
// 为什么放进 /healthz：
//   - **goroutine 数是发现泄漏最直接的信号**：连接关了但 goroutine 不降，
//     说明读写协程没有退出（这是长连接服务最经典的 bug）
//   - 堆内存 / GC 次数是判断内存是否稳定的依据
//
// 这些数字全部由 Go 运行时自己给出，不存在"估算"。
//
// ⚠️ 曾经在这里放过 "cpuSeconds"（读 runtime/metrics 的
// /cpu/classes/total:cpu-seconds），但在 Windows + Go 1.27 上实测**恒为 0**，
// 会让压测报告算出荒谬的 CPU%。已移除 —— 宁可没有，也不给一个假数字。
// CPU 占用改为由外部按进程 TotalProcessorTime 采样。
func runtimeSnapshot() gin.H {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return gin.H{
		"goroutines":  runtime.NumGoroutine(),
		"goVersion":   runtime.Version(),
		"numCPU":      runtime.NumCPU(),
		"heapAllocMB": round1(float64(mem.HeapAlloc) / (1 << 20)),
		"heapSysMB":   round1(float64(mem.HeapSys) / (1 << 20)),
		"sysMB":       round1(float64(mem.Sys) / (1 << 20)),
		"gcCount":     mem.NumGC,
	}
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

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
