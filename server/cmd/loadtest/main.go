// Package main 是 IM 项目的压测客户端。
//
// 为什么自己写而不是用 k6 / wrk / ab：
//   - wrk / ab 只能压 HTTP，**测不了 WebSocket**
//   - k6 要学 JS API，且 WebSocket 支持有限
//   - Go 的 goroutine 天生适合开几千条连接；而且这个工具本身就是项目的一部分，
//     面试时可以直接展示
//
// 延迟怎么测（关键设计）：
//
//	协议里的 send_ack 只回 {clientMsgId, seq, createdAt}，**不回显发送时间**。
//	所以这里在**客户端本地**记「发出时刻」，收到 ack 时按 clientMsgId 查出来相减。
//	这比在协议里加 sent_at_ms 字段更准（不受时钟偏移影响），也不用改接口契约。
//
// 用法：
//
//	go run ./cmd/loadtest -conns 200 -per-conn 10 -conv 77 -tokens tokens.txt -ramp-up 5s
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	flagHost       = flag.String("host", "127.0.0.1:8080", "服务地址（不含 scheme）")
	flagConns      = flag.Int("conns", 200, "并发连接数")
	flagPerConn    = flag.Int("per-conn", 10, "每个连接发送的消息数")
	flagTokens     = flag.String("tokens", "", "token 列表文件（每行一个）")
	flagConv       = flag.Uint64("conv", 0, "目标会话 ID（必填）")
	flagRampUp     = flag.Duration("ramp-up", 5*time.Second, "建立全部连接的总时长（错峰上线）")
	flagHold       = flag.Duration("hold", 2*time.Second, "发完后保持连接的时长（用于观察承载量/内存）")
	flagAckTimeout = flag.Duration("ack-timeout", 30*time.Second, "等待 ack 的总超时")
	flagJSONOut    = flag.String("json-out", "", "把报告写成 JSON 文件（便于结果表取值）")
	flagLabel      = flag.String("label", "", "本轮标签，写进报告")
)

// 计数器。
type stats struct {
	connected    atomic.Int64
	connFailed   atomic.Int64
	connClosed   atomic.Int64
	sent         atomic.Int64
	acked        atomic.Int64
	sendFailed   atomic.Int64
	pushes       atomic.Int64
	errEnvelopes atomic.Int64
	badAcks      atomic.Int64
}

// 延迟样本收集器。
//
// 用分片锁而不是单一 mutex：5000 连接 × N 条消息时会频繁 append，
// 单锁会成为压测客户端自己的瓶颈（压测工具自己跑不动就没意义了）。
type latencyCollector struct {
	shards [16]struct {
		mu   sync.Mutex
		data []time.Duration
	}
}

func (c *latencyCollector) add(d time.Duration) {
	// 用纳秒低位做分片，分布均匀且无需原子操作
	idx := int(d.Nanoseconds()) & 15
	c.shards[idx].mu.Lock()
	c.shards[idx].data = append(c.shards[idx].data, d)
	c.shards[idx].mu.Unlock()
}

func (c *latencyCollector) all() []time.Duration {
	var out []time.Duration
	for i := range c.shards {
		c.shards[i].mu.Lock()
		out = append(out, c.shards[i].data...)
		c.shards[i].mu.Unlock()
	}
	return out
}

// Report 压测报告（同时用于终端输出与 JSON 落盘）。
type Report struct {
	Label          string  `json:"label"`
	StartedAt      string  `json:"startedAt"`
	DurationMs     int64   `json:"durationMs"`
	Target         string  `json:"target"`
	ConnsRequested int     `json:"connsRequested"`
	PerConn        int     `json:"perConn"`
	MessagesTotal  int64   `json:"messagesRequested"`
	Connected      int64   `json:"connected"`
	ConnFailed     int64   `json:"connFailed"`
	ConnectRate    float64 `json:"connectRatePct"`
	Sent           int64   `json:"sent"`
	Acked          int64   `json:"acked"`
	SendFailed     int64   `json:"sendFailed"`
	Pushes         int64   `json:"pushesReceived"`
	ErrEnvelopes   int64   `json:"errorEnvelopes"`
	BadAcks        int64   `json:"acksWithoutPending"`

	LatencyAvgMs float64 `json:"latencyAvgMs"`
	LatencyP50Ms float64 `json:"latencyP50Ms"`
	LatencyP95Ms float64 `json:"latencyP95Ms"`
	LatencyP99Ms float64 `json:"latencyP99Ms"`
	LatencyMaxMs float64 `json:"latencyMaxMs"`
	Samples      int     `json:"latencySamples"`

	// 吞吐：以「发出第一条消息 → 收到最后一个 ack」为窗口
	SendWindowMs int64   `json:"sendWindowMs"`
	Throughput   float64 `json:"throughputMsgPerSec"`

	// 数据完整性：应发 vs 实收
	Lossless bool `json:"lossless"`
}

func main() {
	flag.Parse()

	if *flagConv == 0 {
		fmt.Println("错误：必须指定 -conv（目标会话 ID）")
		flag.Usage()
		os.Exit(2)
	}
	tokens := loadTokens(*flagTokens)
	if len(tokens) == 0 {
		fmt.Println("错误：需要至少一个 token（用 -tokens 指定文件，每行一个）")
		os.Exit(2)
	}

	var st stats
	lat := &latencyCollector{}

	totalMsgs := int64(*flagConns) * int64(*flagPerConn)
	fmt.Printf("开始压测  label=%s\n", orDash(*flagLabel))
	fmt.Printf("  目标      : ws://%s/ws  会话=%d\n", *flagHost, *flagConv)
	fmt.Printf("  规模      : %d 连接 × %d 条 = %d 条消息\n", *flagConns, *flagPerConn, totalMsgs)
	fmt.Printf("  错峰上线  : %v（间隔 %v）\n", *flagRampUp, rampInterval(*flagRampUp, *flagConns))
	fmt.Printf("  token 池  : %d 个\n", len(tokens))

	startedAt := time.Now()

	// 发送窗口：所有连接的第一条消息前 ~ 最后一个 ack
	var firstSendNanos atomic.Int64
	var lastAckNanos atomic.Int64
	markFirstSend := func() {
		if firstSendNanos.Load() == 0 {
			firstSendNanos.CompareAndSwap(0, time.Now().UnixNano())
		}
	}

	interval := rampInterval(*flagRampUp, *flagConns)

	var wg sync.WaitGroup
	for i := 0; i < *flagConns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			runOne(idx, tokens[idx%len(tokens)], &st, lat, markFirstSend, &lastAckNanos)
		}(i)
		if interval > 0 {
			time.Sleep(interval)
		}
	}
	wg.Wait()

	elapsed := time.Since(startedAt)
	report := buildReport(*flagLabel, startedAt, elapsed, *flagHost, *flagConns, *flagPerConn,
		totalMsgs, &st, lat, firstSendNanos.Load(), lastAckNanos.Load())

	printReport(report)

	if *flagJSONOut != "" {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Printf("\n写 JSON 失败: %v\n", err)
			return
		}
		if err := os.WriteFile(*flagJSONOut, b, 0o644); err != nil {
			fmt.Printf("\n写 JSON 失败: %v\n", err)
			return
		}
		fmt.Printf("\n报告已写入 %s\n", *flagJSONOut)
	}
}

func runOne(
	idx int,
	token string,
	st *stats,
	lat *latencyCollector,
	markFirstSend func(),
	lastAckNanos *atomic.Int64,
) {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	url := fmt.Sprintf("ws://%s/ws?token=%s", *flagHost, token)
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		st.connFailed.Add(1)
		return
	}
	defer func() { _ = conn.Close() }()
	st.connected.Add(1)

	conn.SetReadLimit(64 << 10)

	// 每条连接自己维护「已发出但未收到 ack」的表。
	// 只在读协程删、写协程加，用一把小锁足够。
	var pendMu sync.Mutex
	pending := make(map[string]time.Time, *flagPerConn)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(data, &env) != nil {
				continue
			}
			switch env.Type {
			case "send_ack":
				var ack struct {
					ClientMsgID string `json:"clientMsgId"`
				}
				if json.Unmarshal(env.Data, &ack) != nil {
					continue
				}
				pendMu.Lock()
				sentAt, ok := pending[ack.ClientMsgID]
				delete(pending, ack.ClientMsgID)
				pendMu.Unlock()
				if !ok {
					st.badAcks.Add(1)
					continue
				}
				lat.add(time.Since(sentAt))
				st.acked.Add(1)
				// 记录最后一个 ack 的时刻
				now := time.Now().UnixNano()
				for {
					prev := lastAckNanos.Load()
					if now <= prev || lastAckNanos.CompareAndSwap(prev, now) {
						break
					}
				}
			case "push":
				st.pushes.Add(1)
			case "error":
				st.errEnvelopes.Add(1)
			}
		}
	}()

	// 发送
	for j := 0; j < *flagPerConn; j++ {
		id, err := uuid.NewV7()
		if err != nil {
			st.sendFailed.Add(1)
			break
		}
		cid := id.String()

		payload, err := json.Marshal(map[string]any{
			"type":  "send",
			"reqId": cid,
			"data": map[string]any{
				"clientMsgId":    cid,
				"conversationId": *flagConv,
				"contentType":    1,
				"content":        fmt.Sprintf("loadtest %d-%d", idx, j),
			},
		})
		if err != nil {
			st.sendFailed.Add(1)
			break
		}

		pendMu.Lock()
		pending[cid] = time.Now()
		pendMu.Unlock()

		markFirstSend()
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			st.sendFailed.Add(1)
			break
		}
		st.sent.Add(1)
	}

	// 等自己的 ack 全部回来（或总超时）
	deadline := time.Now().Add(*flagAckTimeout)
	for time.Now().Before(deadline) {
		pendMu.Lock()
		left := len(pending)
		pendMu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 保持连接一段时间：用于观察「N 条连接同时挂着」时的内存与连接数
	if *flagHold > 0 {
		time.Sleep(*flagHold)
	}

	_ = conn.Close()
	<-readDone
	st.connClosed.Add(1)
}

func buildReport(
	label string, startedAt time.Time, elapsed time.Duration,
	host string, conns, perConn int, totalMsgs int64,
	st *stats, lat *latencyCollector,
	firstSendNanos, lastAckNanos int64,
) Report {
	r := Report{
		Label:          label,
		StartedAt:      startedAt.Format(time.RFC3339),
		DurationMs:     elapsed.Milliseconds(),
		Target:         fmt.Sprintf("ws://%s/ws", host),
		ConnsRequested: conns,
		PerConn:        perConn,
		MessagesTotal:  totalMsgs,
		Connected:      st.connected.Load(),
		ConnFailed:     st.connFailed.Load(),
		Sent:           st.sent.Load(),
		Acked:          st.acked.Load(),
		SendFailed:     st.sendFailed.Load(),
		Pushes:         st.pushes.Load(),
		ErrEnvelopes:   st.errEnvelopes.Load(),
		BadAcks:        st.badAcks.Load(),
	}

	if conns > 0 {
		r.ConnectRate = round2(float64(r.Connected) / float64(conns) * 100)
	}

	samples := lat.all()
	if len(samples) > 0 {
		sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
		r.Samples = len(samples)
		var sum time.Duration
		for _, d := range samples {
			sum += d
		}
		r.LatencyAvgMs = ms(sum / time.Duration(len(samples)))
		r.LatencyP50Ms = ms(percentile(samples, 0.50))
		r.LatencyP95Ms = ms(percentile(samples, 0.95))
		r.LatencyP99Ms = ms(percentile(samples, 0.99))
		r.LatencyMaxMs = ms(samples[len(samples)-1])
	}

	if firstSendNanos > 0 && lastAckNanos > firstSendNanos {
		window := time.Duration(lastAckNanos - firstSendNanos)
		r.SendWindowMs = window.Milliseconds()
		if window > 0 {
			r.Throughput = round2(float64(r.Acked) / window.Seconds())
		}
	}

	// 零丢失 = 目标条数全部落 ack，且没有发送失败
	r.Lossless = r.Acked == totalMsgs && r.SendFailed == 0 && r.BadAcks == 0

	return r
}

func printReport(r Report) {
	fmt.Println()
	fmt.Println("==================== 压测报告 ====================")
	fmt.Printf("标签              : %s\n", orDash(r.Label))
	fmt.Printf("总耗时            : %d ms\n", r.DurationMs)
	fmt.Printf("连接成功          : %d / %d  (%.2f%%)\n", r.Connected, r.ConnsRequested, r.ConnectRate)
	fmt.Printf("连接失败          : %d\n", r.ConnFailed)
	fmt.Printf("发送成功(已发出)  : %d / %d\n", r.Sent, r.MessagesTotal)
	fmt.Printf("收到 ack          : %d\n", r.Acked)
	fmt.Printf("发送失败          : %d\n", r.SendFailed)
	fmt.Printf("收到 push         : %d\n", r.Pushes)
	fmt.Printf("error 信封        : %d\n", r.ErrEnvelopes)
	fmt.Printf("无主 ack          : %d\n", r.BadAcks)
	if r.Samples > 0 {
		fmt.Printf("延迟 平均         : %.2f ms\n", r.LatencyAvgMs)
		fmt.Printf("延迟 P50          : %.2f ms\n", r.LatencyP50Ms)
		fmt.Printf("延迟 P95          : %.2f ms\n", r.LatencyP95Ms)
		fmt.Printf("延迟 P99          : %.2f ms\n", r.LatencyP99Ms)
		fmt.Printf("延迟 MAX          : %.2f ms\n", r.LatencyMaxMs)
		fmt.Printf("延迟样本数        : %d\n", r.Samples)
	}
	fmt.Printf("发送窗口          : %d ms\n", r.SendWindowMs)
	fmt.Printf("吞吐              : %.2f 条/秒（以 ack 计）\n", r.Throughput)
	fmt.Printf("零丢失            : %v\n", r.Lossless)
	fmt.Println("=================================================")
}

// ---------------- 小工具 ----------------

func loadTokens(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func rampInterval(total time.Duration, n int) time.Duration {
	if n <= 0 || total <= 0 {
		return 0
	}
	d := total / time.Duration(n)
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(sorted))*q)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func ms(d time.Duration) float64 {
	return round2(float64(d.Microseconds()) / 1000.0)
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
