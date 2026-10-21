#!/usr/bin/env bash
# ============================================================================
# 服务器端发布脚本：解包 → 原子切换 → 重启 → 健康检查
#
# 用法（在服务器上）：
#   sudo bash deploy.sh /tmp/im-chat-v0.7.0.tar.gz
#
# 设计要点（面试可讲）：
#   1. 先解包到临时目录，校验通过才切换 —— 避免「传了一半就上线」
#   2. 用 **mv 改名做原子切换**，保留 .prev 供回滚
#   3. 切换后必须 curl /healthz 拿到 ok，否则**自动回滚**
#   4. 整个过程中旧进程一直服务，只有最后 restart 那一瞬中断（秒级）
# ============================================================================
set -euo pipefail

TARBALL="${1:-}"
if [[ -z "$TARBALL" || ! -f "$TARBALL" ]]; then
    echo "用法: sudo bash $0 <发布包.tar.gz>"
    exit 1
fi

APP_DIR=/opt/im-chat
STAGE=$(mktemp -d /tmp/im-chat-stage.XXXXXX)
KEEP_RELEASES=3

log()  { echo -e "\n\033[36m==> $*\033[0m"; }
ok()   { echo -e "\033[32m  ✓ $*\033[0m"; }
err()  { echo -e "\033[31m  ✗ $*\033[0m"; }

cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

# ---------------------------------------------------------------------------
log "1/8 解包到临时目录（不直接覆盖线上目录）"
tar -xzf "$TARBALL" -C "$STAGE"
ls -la "$STAGE"

for f in im-server-linux web/index.html; do
    if [[ ! -e "$STAGE/$f" ]]; then
        err "发布包缺少 $f，中止"
        exit 1
    fi
done
ok "发布包结构完整"

# ---------------------------------------------------------------------------
log "2/8 安装 systemd 单元（首次部署时）"
if [[ -d "$STAGE/systemd" ]]; then
    for u in "$STAGE"/systemd/*.service; do
        name=$(basename "$u")
        if [[ -f "/etc/systemd/system/$name" ]]; then
            ok "$name 已存在，保留线上版本"
        else
            install -m 644 -o root -g root "$u" "/etc/systemd/system/$name"
            ok "已安装 $name"
        fi
    done
    systemctl daemon-reload
fi

# ---------------------------------------------------------------------------
log "3/8 安装 Nginx 站点配置（首次部署时）"
if [[ -f "$STAGE/nginx/im-chat.conf" ]]; then
    if [[ -f /etc/nginx/sites-available/im-chat ]]; then
        ok "Nginx 配置已存在，保留线上版本"
    else
        install -m 644 -o root -g root "$STAGE/nginx/im-chat.conf" /etc/nginx/sites-available/im-chat
        ln -sf /etc/nginx/sites-available/im-chat /etc/nginx/sites-enabled/im-chat
        rm -f /etc/nginx/sites-enabled/default
        ok "已安装 Nginx 配置"
    fi
    # ⚠️ 每次都要 test，通过了才 reload
    if nginx -t; then
        systemctl reload nginx
        ok "nginx -t 通过并已 reload"
    else
        err "nginx 配置有误，已中止（线上配置未改动）"
        exit 1
    fi
fi

# ---------------------------------------------------------------------------
log "4/8 执行数据库迁移（幂等）"
if [[ -d "$STAGE/migrations" ]]; then
    set -a
    # shellcheck disable=SC1091
    source "$APP_DIR/im-chat.env"
    set +a
    for m in $(ls -1 "$STAGE"/migrations/*.sql 2>/dev/null | sort); do
        base=$(basename "$m")
        case "$base" in
            *bootstrap*|*local*) ok "跳过 $base（本地专用）"; continue ;;
        esac
        # 用 IF NOT EXISTS 保证重复执行安全
        if mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" "$DB_NAME" < "$m" 2>/dev/null; then
            ok "迁移 $base"
        else
            err "迁移 $base 失败（可能是已执行过，检查表结构）"
        fi
    done
fi

# ---------------------------------------------------------------------------
log "5/8 备份当前版本（回滚用）"
cd "$APP_DIR"
if [[ -f im-server-linux ]]; then
    cp -p im-server-linux "im-server-linux.prev"
    ok "已备份二进制 → im-server-linux.prev"
fi
if [[ -d web ]]; then
    rm -rf web.prev
    cp -rp web web.prev
    ok "已备份前端 → web.prev"
fi

# ---------------------------------------------------------------------------
log "6/8 原子切换（mv 改文件名，不是复制）"
install -m 755 -o deploy -g deploy "$STAGE/im-server-linux" "$APP_DIR/im-server-new"
mv -f "$APP_DIR/im-server-new" "$APP_DIR/im-server-linux"
ok "二进制已切换"

rm -rf "$APP_DIR/web-new"
cp -rp "$STAGE/web" "$APP_DIR/web-new"
chown -R deploy:deploy "$APP_DIR/web-new"
if [[ -d "$APP_DIR/web" ]]; then
    rm -rf "$APP_DIR/web.old"
    mv "$APP_DIR/web" "$APP_DIR/web.old"
fi
mv "$APP_DIR/web-new" "$APP_DIR/web"
rm -rf "$APP_DIR/web.old"
ok "前端已切换"

# ---------------------------------------------------------------------------
log "7/8 重启服务并等待健康"
systemctl restart im-chat
if systemctl is-active --quiet im-chat-ai 2>/dev/null; then
    systemctl restart im-chat-ai
fi

HEALTHY=false
for i in $(seq 1 20); do
    sleep 1
    body=$(curl -fsS http://127.0.0.1:8080/healthz 2>/dev/null || true)
    if echo "$body" | grep -q '"status":"ok"'; then
        HEALTHY=true
        ok "健康检查通过（第 ${i} 秒）"
        echo "    $body" | head -c 300
        break
    fi
done

# ---------------------------------------------------------------------------
log "8/8 结果判定"
if [[ "$HEALTHY" != "true" ]]; then
    err "健康检查失败 —— 自动回滚到上一版本"
    if [[ -f im-server-linux.prev ]]; then
        mv -f im-server-linux im-server-broken
        mv -f im-server-linux.prev im-server-linux
        systemctl restart im-chat
        sleep 2
        err "已回滚。请用 journalctl -u im-chat -n 80 查看失败原因"
        err "坏版本保留为 im-server-broken 供排查"
    else
        err "没有可回滚的版本"
    fi
    journalctl -u im-chat -n 40 --no-pager || true
    exit 1
fi

# 清理旧备份（保留最近几个）
ls -1t "$APP_DIR"/im-server-broken* 2>/dev/null | tail -n +$((KEEP_RELEASES + 1)) | xargs -r rm -f

echo
echo "============================================================"
echo -e "\033[32m 部署成功\033[0m"
echo "============================================================"
echo " 版本    : $(curl -fsS http://127.0.0.1:8080/healthz | grep -o '"version":"[^"]*"')"
echo " 服务    : $(systemctl is-active im-chat) / 开机自启 $(systemctl is-enabled im-chat)"
echo " 前端    : $(ls "$APP_DIR/web" | head -c 120)"
echo " 下一步  : 用浏览器打开 http://<你的IP>/ 验证登录与发消息"
echo "============================================================"
