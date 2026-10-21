#!/usr/bin/env bash
# ============================================================================
# 回滚脚本：把上一版本的二进制/前端切回来
#
# 用法（在服务器上）：
#   sudo bash deploy/scripts/rollback.sh
#
# 为什么必须有这个脚本：**回滚方案要在第一次发布之前就写好**。
# 出事的时候现场现想「怎么退回去」，是事故扩大的典型原因。
# ============================================================================
set -euo pipefail

APP_DIR=/opt/im-chat
cd "$APP_DIR"

log() { echo -e "\n\033[36m==> $*\033[0m"; }
ok()  { echo -e "\033[32m  ✓ $*\033[0m"; }
err() { echo -e "\033[31m  ✗ $*\033[0m"; }

if [[ $EUID -ne 0 ]]; then
    echo "请用 sudo 运行"
    exit 1
fi

log "1/4 检查可回滚版本"
if [[ ! -f im-server-linux.prev ]]; then
    err "没有 im-server-linux.prev，无法回滚"
    exit 1
fi
ok "找到上一版本二进制"
if [[ -d web.prev ]]; then
    ok "找到上一版本前端"
else
    echo "  （没有 web.prev，前端不回滚）"
fi

log "2/4 保留当前（坏）版本供排查"
if [[ -f im-server-linux ]]; then
    mv -f im-server-linux "im-server-broken-$(date +%Y%m%d-%H%M%S)"
    ok "当前二进制已重命名保存"
fi

log "3/4 切回上一版本"
mv -f im-server-linux.prev im-server-linux
chmod +x im-server-linux
ok "二进制已切回"

if [[ -d web.prev ]]; then
    rm -rf web.broken
    mv web web.broken
    mv web.prev web
    ok "前端已切回"
fi

log "4/4 重启并验证"
systemctl restart im-chat
sleep 2
if systemctl is-active --quiet im-chat; then
    ok "服务已启动"
else
    err "服务启动失败"
    journalctl -u im-chat -n 40 --no-pager
    exit 1
fi

for i in $(seq 1 15); do
    if curl -fsS http://127.0.0.1:8080/healthz 2>/dev/null | grep -q '"status":"ok"'; then
        ok "健康检查通过"
        curl -fsS http://127.0.0.1:8080/healthz | head -c 300
        echo
        echo
        echo "============================================================"
        echo -e "\033[32m 回滚完成\033[0m"
        echo "============================================================"
        exit 0
    fi
    sleep 1
done

err "回滚后健康检查仍失败，需要人工介入"
journalctl -u im-chat -n 60 --no-pager
exit 1
