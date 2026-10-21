// Package main 是故障演练探针：测量「服务端被杀 → 客户端恢复可用」的真实耗时。
//
// 为什么需要它：
//
//	手册第 7 篇要填一个数字「从杀进程到客户端恢复可用，共 ___ 秒」。
//	这个数字只能实测 —— 它取决于三件事：
//	  ① 服务端被杀到被拉起的时间（systemd RestartSec=3 或手动重启）
//	  ② 客户端**多久发现**连接断了（onclose 立即触发，所以很快）
//	  ③ 客户端的退避重连策略（本项目 1→2→4→8→16→30s ±20% 抖动）
//
//	探针刻意**复刻前端 WsClient 的重连策略**（同一组退避值），
//	这样测出来的数字才代表真实用户体感，而不是随便造一个。
//
// 用法：
//
//	go run ./cmd/wsprobe -token <accessToken> -conv 77 -limit 120s
//
// 输出的关键一行是 `RECOVERED after ...`，就是手册要填的数字。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	flagHost  = flag.String("host", "127.0.0.1:8080", "服务地址")
	flagToken = flag.String("token", "", "access token（必填）")
	flagConv  = flag.Uint64("conv", 0, "用于发探测消息的会话 ID（必填）")
	flagLimit = flag.Duration("limit", 120*time.Second, "最长观察时间")
	flagProbe = flag.Duration("probe-before", 4*time.Second, "连上后先正常收发的时间（之后才认为可以开始杀服务）")
)

// 与前端 src/utils/backoff.ts 完全一致的退避策略。
var backoffSchedule = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second,
	8 * time.Second, 16 * time.Second, 30 * time.Second,
}

const jitterRatio = 0.2

func jitter(base time.Duration) time.Duration {
	delta := float64(base) * jitterRatio
	d := float64(base) - delta + rand.Float64()*2*delta
	return time.Duration(d)
}

func main() {
	flag.Parse()
	if *flagToken == "" || *flagConv == 0 {
		fmt.Println("必须提供 -token 与 -conv")
		os.Exit(2)
	}

	start := time.Now()
	url := fmt.Sprintf("ws://%s/ws?token=%s", *flagHost, *flagToken)

	fmt.Printf("探针启动 %s\n", start.Format("15:04:05.000"))
	fmt.Printf("  目标: %s  会话=%d\n", *flagHost, *flagConv)
	fmt.Printf("  观察上限: %v\n", *flagLimit)
	fmt.Println("  退避策略与前端一致: 1→2→4→8→16→30s ±20%")
	fmt.Println()

	// ---- 第 1 阶段：正常连上并收发一条，确认基线可用 ----
	conn, err := dial(url)
	if err != nil {
		fmt.Printf("首次连接失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[%s] 已连接\n", since(start))

	if err := sendAndWaitAck(conn, *flagConv, 5*time.Second); err != nil {
		fmt.Printf("首次收发失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[%s] 基线收发正常（可以开始杀服务了）\n", since(start))

	// 等一小会儿，让操作者有窗口去 kill 服务
	time.Sleep(*flagProbe)
	fmt.Printf("[%s] 进入监听状态，等待服务端断开...\n", since(start))

	// ---- 第 2 阶段：等它断。断的那一刻记录 downAt ----
	var downAt time.Time
	deadline := start.Add(*flagLimit)
	_ = conn.SetReadDeadline(deadline)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			downAt = time.Now()
			fmt.Printf("[%s] 检测到连接断开: %v\n", since(start), err)
			_ = conn.Close()
			break
		}
	}

	// ---- 第 3 阶段：按前端策略退避重连，直到「能再次成功收发一条」 ----
	attempt := 0
	var recoveredAt time.Time
	for time.Now().Before(deadline) {
		attempt++
		idx := attempt - 1
		if idx >= len(backoffSchedule) {
			idx = len(backoffSchedule) - 1
		}
		delay := jitter(backoffSchedule[idx])
		fmt.Printf("[%s] 第 %d 次重连，%v 后尝试...\n", since(start), attempt, delay.Round(time.Millisecond))
		time.Sleep(delay)

		c, err := dial(url)
		if err != nil {
			fmt.Printf("[%s]   重连失败: %v\n", since(start), shortErr(err))
			continue
		}
		fmt.Printf("[%s]   重连成功，发探测消息验证可用性...\n", since(start))

		if err := sendAndWaitAck(c, *flagConv, 10*time.Second); err != nil {
			fmt.Printf("[%s]   连上了但收发失败: %v\n", since(start), shortErr(err))
			_ = c.Close()
			continue
		}
		recoveredAt = time.Now()
		_ = c.Close()
		break
	}

	fmt.Println()
	if recoveredAt.IsZero() {
		fmt.Printf("FAILED 未在 %v 内恢复\n", *flagLimit)
		os.Exit(1)
	}

	downtime := recoveredAt.Sub(downAt)
	// 从服务端被杀的「真实时刻」我们并不知道（探针只能感知 onclose），
	// 所以同时报「检测到断开 → 恢复可用」，这是客户端体感的下界。
	fmt.Println("==================== 故障演练结果 ====================")
	fmt.Printf("检测到断开       : %s\n", downAt.Format("15:04:05.000"))
	fmt.Printf("恢复可用         : %s\n", recoveredAt.Format("15:04:05.000"))
	fmt.Printf("恢复耗时         : %.2f 秒（%v）\n", downtime.Seconds(), downtime.Round(time.Millisecond))
	fmt.Printf("重连尝试次数     : %d\n", attempt)
	fmt.Printf("RECOVERED after  : %.2f s\n", downtime.Seconds())
	fmt.Println("=====================================================")
	fmt.Println()
	fmt.Println("说明：这个数字是「客户端感知到断开 → 再次成功收发」的耗时。")
	fmt.Println("      它包含 服务端被拉起的时间 + 一次退避等待 + 重连握手。")
}

func dial(url string) (*websocket.Conn, error) {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := d.Dial(url, nil)
	return conn, err
}

// sendAndWaitAck 发一条消息并等它的 send_ack（用 clientMsgId 匹配）。
func sendAndWaitAck(conn *websocket.Conn, conv uint64, timeout time.Duration) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	cid := id.String()

	payload, err := json.Marshal(map[string]any{
		"type":  "send",
		"reqId": cid,
		"data": map[string]any{
			"clientMsgId":    cid,
			"conversationId": conv,
			"contentType":    1,
			"content":        "故障演练探测消息",
		},
	})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		if env.Type != "send_ack" {
			continue
		}
		var ack struct {
			ClientMsgID string `json:"clientMsgId"`
		}
		if json.Unmarshal(env.Data, &ack) != nil {
			continue
		}
		if ack.ClientMsgID == cid {
			_ = conn.SetReadDeadline(time.Time{})
			return nil
		}
	}
}

func since(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
