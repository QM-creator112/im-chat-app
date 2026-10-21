#!/usr/bin/env bash
# ============================================================================
# 上线检查清单自动化（能自动验的都验，验不了的明确说明）
#
# 用法（在服务器上）：
#   sudo bash deploy/scripts/verify-production.sh [公网IP]
#
# 对应手册 §6.8 的 18 条清单。每条都会打印 PASS / FAIL / MANUAL。
# ⚠️ 标 MANUAL 的是「必须人眼看」的（比如两个浏览器互发消息），
#    **不要因为脚本说 PASS 就跳过人工验证**。
# ============================================================================
set -uo pipefail

PUBLIC_IP="${1:-}"
PASS=0; FAIL=0; MANUAL=0

pass()   { PASS=$((PASS+1));   echo -e "  \033[32m[PASS]\033[0m $*"; }
fail()   { FAIL=$((FAIL+1));   echo -e "  \033[31m[FAIL]\033[0m $*"; }
manual() { MANUAL=$((MANUAL+1)); echo -e "  \033[33m[MANUAL]\033[0m $*"; }

section() { echo; echo -e "\033[36m=== $* ===\033[0m"; }

# ---------------------------------------------------------------------------
section "1-2 服务状态与开机自启"
if systemctl is-active --quiet im-chat; then
    pass "im-chat 正在运行（$(systemctl is-active im-chat)）"
else
    fail "im-chat 未运行"
fi
if [[ "$(systemctl is-enabled im-chat 2>/dev/null)" == "enabled" ]]; then
    pass "im-chat 已设置开机自启"
else
    fail "im-chat 未设置开机自启"
fi
if systemctl is-active --quiet im-chat-ai 2>/dev/null; then
    pass "im-chat-ai 侧车运行中"
else
    echo "  [INFO] AI 侧车未运行（如果没做 Sprint 6 属正常）"
fi

# ---------------------------------------------------------------------------
section "3-4 健康检查与版本号"
BODY=$(curl -fsS --max-time 5 http://127.0.0.1:8080/healthz 2>/dev/null || true)
if echo "$BODY" | grep -q '"status":"ok"'; then
    pass "本地 /healthz 返回 ok"
else
    fail "本地 /healthz 不 ok：$BODY"
fi
if echo "$BODY" | grep -q '"mysql":"ok"'; then pass "MySQL 连通"; else fail "MySQL 不通"; fi
if echo "$BODY" | grep -q '"redis":"ok"'; then pass "Redis 连通"; else fail "Redis 不通"; fi
if echo "$BODY" | grep -q '"version":"dev"'; then
    fail "version 是 dev —— 发布时忘了用 -ldflags 编入 git tag"
elif echo "$BODY" | grep -q '"version":"'; then
    pass "版本号：$(echo "$BODY" | grep -o '"version":"[^"]*"')"
else
    fail "拿不到版本号"
fi

# ---------------------------------------------------------------------------
section "5-7 公网访问（需要人工确认界面与聊天）"
if [[ -n "$PUBLIC_IP" ]]; then
    if curl -fsS --max-time 8 "http://${PUBLIC_IP}/" 2>/dev/null | grep -q '<div id="app">'; then
        pass "公网 HTTP 能拿到前端页面（http://${PUBLIC_IP}/）"
    else
        fail "公网 HTTP 拿不到页面 —— 检查安全组/ufw/Nginx"
    fi
    # /ws 不带 Upgrade 头访问：能到后端（不是 404）就说明 location 生效
    CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://${PUBLIC_IP}/ws" 2>/dev/null || echo 000)
    if [[ "$CODE" != "404" && "$CODE" != "000" ]]; then
        pass "公网 /ws 路由生效（返回 $CODE，不是 404）"
    else
        fail "公网 /ws 返回 $CODE —— Nginx 的 /ws location 可能没生效"
    fi
else
    manual "公网 HTTP 访问：传公网 IP 作为参数可自动检查；否则请浏览器验证"
fi
manual "公网 WebSocket 握手：浏览器 F12 → Network → WS，应看到 101 Switching Protocols"
manual "两个浏览器窗口互发消息，确认实时到达"

# ---------------------------------------------------------------------------
section "8 长连接不掉（验证 Nginx proxy_read_timeout）"
TIMEOUT=$(grep -rh 'proxy_read_timeout' /etc/nginx/sites-enabled/ 2>/dev/null | head -1 | tr -d ' ;' | cut -d' ' -f2)
if [[ -n "$TIMEOUT" ]]; then
    pass "Nginx proxy_read_timeout = $TIMEOUT（默认是 60s，必须放大）"
else
    fail "没找到 proxy_read_timeout —— 长连接会在 60 秒被静默断开"
fi
if grep -rq 'proxy_set_header Upgrade' /etc/nginx/sites-enabled/ 2>/dev/null; then
    pass "Nginx 转发了 Upgrade 头"
else
    fail "缺 proxy_set_header Upgrade —— WebSocket 握手会 400"
fi
if grep -rq 'proxy_buffering off' /etc/nginx/sites-enabled/ 2>/dev/null; then
    pass "Nginx 关闭了响应缓冲"
else
    fail "缺 proxy_buffering off —— 消息会有明显延迟"
fi
manual "挂 3 分钟不动再发消息，确认还能发出去"

# ---------------------------------------------------------------------------
section "9-11 客户端韧性"
manual "断网重连：切手机热点，界面应提示重连并自动恢复"
manual "离线补拉：关窗口 → 另一窗口发消息 → 重开，消息应补齐"
manual "SPA 刷新不 404：在 /chat 页面按 F5"

# ---------------------------------------------------------------------------
section "12-14 安全：数据库与缓存绝不对外"
LISTEN_3306=$(ss -lntp 2>/dev/null | grep ':3306 ' || true)
if echo "$LISTEN_3306" | grep -qE '0\.0\.0\.0:3306|\*:3306|\[::\]:3306'; then
    fail "MySQL 监听在所有网卡上（$LISTEN_3306）—— 必须在 my.cnf 里 bind-address=127.0.0.1"
else
    pass "MySQL 未监听在 0.0.0.0:3306"
fi

LISTEN_6379=$(ss -lntp 2>/dev/null | grep ':6379 ' || true)
if echo "$LISTEN_6379" | grep -qE '0\.0\.0\.0:6379|\*:6379|\[::\]:6379'; then
    fail "Redis 监听在所有网卡上（$LISTEN_6379）—— 必须在 redis.conf 里 bind 127.0.0.1"
else
    pass "Redis 未监听在 0.0.0.0:6379"
fi

REDIS_REQUIREPASS=$(grep -E '^requirepass .+' /etc/redis/redis.conf 2>/dev/null | head -1 | sed 's/requirepass .*/requirepass 已设置/')
if [[ -n "$REDIS_REQUIREPASS" ]]; then
    pass "Redis 设置了密码"
else
    fail "Redis 没设密码 —— 未授权访问是最经典的入侵入口"
fi

UFW=$(ufw status 2>/dev/null | head -10)
if echo "$UFW" | grep -q 'Status: active'; then
    pass "ufw 已启用"
    for port in 22 80 443; do
        if echo "$UFW" | grep -qE "^${port}/tcp"; then pass "ufw 允许 ${port}/tcp"; fi
    done
    for port in 3306 6379 8080; do
        if echo "$UFW" | grep -qE "^${port}/tcp"; then fail "ufw 开放了 ${port} —— 不应对外开放"; fi
    done
else
    fail "ufw 未启用"
fi
manual "云厂商安全组：确认只开放 22/80/443，且 3306/6379/8080 未开放"

# ---------------------------------------------------------------------------
section "15 备份"
if crontab -l 2>/dev/null | grep -q backup.sh; then
    pass "已配置每日备份定时任务"
else
    fail "没有备份定时任务"
fi
LATEST_BACKUP=$(ls -1t /opt/im-chat/backup/im_chat-*.sql.gz 2>/dev/null | head -1)
if [[ -n "$LATEST_BACKUP" ]]; then
    pass "最近备份：$(basename "$LATEST_BACKUP") ($(du -h "$LATEST_BACKUP" | cut -f1))"
else
    fail "还没有任何备份文件 —— 手动跑一次 backup.sh"
fi

# ---------------------------------------------------------------------------
section "16 日志"
if journalctl -u im-chat -n 1 --no-pager &>/dev/null; then
    N=$(journalctl -u im-chat --since '10 min ago' --no-pager 2>/dev/null | wc -l)
    pass "journalctl 可查 im-chat 日志（近 10 分钟 $N 行）"
else
    fail "查不到 im-chat 日志"
fi

# ---------------------------------------------------------------------------
section "17-18 配置不泄露"
ENV_PERM=$(ls -l /opt/im-chat/im-chat.env 2>/dev/null | awk '{print $1, $3, $4}')
if [[ "$ENV_PERM" == "-rw------- deploy deploy" ]]; then
    pass "im-chat.env 权限正确（$ENV_PERM）"
else
    fail "im-chat.env 权限不对：$ENV_PERM（应为 -rw------- deploy deploy）"
fi
if [[ -n "$(find /opt/im-chat -maxdepth 1 -name '.env' 2>/dev/null)" ]]; then
    fail "/opt/im-chat/.env 存在 —— 生产环境应该只用 im-chat.env，不要留开发用的 .env"
else
    pass "没有遗留开发用的 .env"
fi

# ---------------------------------------------------------------------------
echo
echo "============================================================"
echo -e "  PASS: $PASS   FAIL: $FAIL   MANUAL: $MANUAL"
echo "============================================================"
if [[ $FAIL -gt 0 ]]; then
    echo -e "\033[31m有 $FAIL 项失败，不要对外说「已上线」。\033[0m"
    exit 1
fi
echo -e "\033[33m自动检查全过，但请把 $MANUAL 项 MANUAL 逐条人工验证。\033[0m"
