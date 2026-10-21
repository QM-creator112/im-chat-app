#!/usr/bin/env bash
# ============================================================================
# 服务器初始化（在全新的 Ubuntu 22.04/24.04 上跑一次）
#
# 用法：
#   sudo bash deploy/scripts/server-init.sh <你的公网IP>
#
# 做的事：装依赖 → 建 deploy 用户 → 建库建账号 → 装并加固 Redis →
#         装 Nginx → 配防火墙 → 建目录
#
# ⚠️ 跑之前请先看一遍。这个脚本会改系统配置，看不懂就别跑。
# ============================================================================
set -euo pipefail

PUBLIC_IP="${1:-}"
if [[ -z "$PUBLIC_IP" ]]; then
    echo "用法: sudo bash $0 <你的公网IP>"
    echo "（公网 IP 用于日志提示；不传也能跑，但安全组仍需手动限制 22 端口）"
    PUBLIC_IP="0.0.0.0"
fi

log() { echo -e "\n\033[36m==> $*\033[0m"; }
warn() { echo -e "\033[33m[警告] $*\033[0m"; }

if [[ $EUID -ne 0 ]]; then
    echo "请用 sudo 运行"
    exit 1
fi

# ---------------------------------------------------------------------------
log "1/8 系统更新与基础工具"
apt-get update -y
apt-get upgrade -y
apt-get install -y curl wget git ufw fail2ban ca-certificates

# ---------------------------------------------------------------------------
log "2/8 创建 deploy 用户（不用 root 跑服务）"
if id deploy &>/dev/null; then
    echo "deploy 用户已存在，跳过"
else
    adduser --disabled-password --gecos "" deploy
    usermod -aG sudo deploy
fi
mkdir -p /home/deploy/.ssh
if [[ -f /root/.ssh/authorized_keys ]]; then
    cp /root/.ssh/authorized_keys /home/deploy/.ssh/
    chown -R deploy:deploy /home/deploy/.ssh
    chmod 700 /home/deploy/.ssh
    chmod 600 /home/deploy/.ssh/authorized_keys
fi

# ---------------------------------------------------------------------------
log "3/8 安装 MySQL"
apt-get install -y mysql-server
systemctl enable --now mysql

DB_PASS=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
mysql <<SQL
CREATE DATABASE IF NOT EXISTS im_chat
    DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
-- ⚠️ 只允许 127.0.0.1 连接，不是 '%'
CREATE USER IF NOT EXISTS 'im_app'@'127.0.0.1' IDENTIFIED BY '${DB_PASS}';
ALTER USER 'im_app'@'127.0.0.1' IDENTIFIED BY '${DB_PASS}';
GRANT ALL PRIVILEGES ON im_chat.* TO 'im_app'@'127.0.0.1';
-- 只读账号：给自己用 Navicat 查数据，避免误删
CREATE USER IF NOT EXISTS 'im_readonly'@'127.0.0.1' IDENTIFIED BY '${DB_PASS}';
GRANT SELECT ON im_chat.* TO 'im_readonly'@'127.0.0.1';
FLUSH PRIVILEGES;
SQL

# ---------------------------------------------------------------------------
log "4/8 安装并加固 Redis"
apt-get install -y redis-server
REDIS_PASS=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
# ⚠️ bind 127.0.0.1：只监听本机。
# ⚠️ requirepass：即使有人从本机其他进程也能拦住。
sed -i 's/^bind .*/bind 127.0.0.1 ::1/' /etc/redis/redis.conf
sed -i "s/^# *requirepass .*/requirepass ${REDIS_PASS}/" /etc/redis/redis.conf
if ! grep -q '^requirepass ' /etc/redis/redis.conf; then
    echo "requirepass ${REDIS_PASS}" >> /etc/redis/redis.conf
fi
# 危险命令重命名（纵深防御，防止误操作清库）
if ! grep -q '^rename-command FLUSHALL' /etc/redis/redis.conf; then
    cat >> /etc/redis/redis.conf <<'REDIS_EOF'
rename-command FLUSHALL ""
rename-command FLUSHDB ""
rename-command CONFIG ""
REDIS_EOF
fi
systemctl enable --now redis-server
systemctl restart redis-server
sleep 1
if redis-cli -a "${REDIS_PASS}" ping 2>/dev/null | grep -q PONG; then
    echo "Redis 就绪（需要密码才能连）"
else
    echo "Redis 启动检查失败，请查 journalctl -u redis-server"
    exit 1
fi

# ---------------------------------------------------------------------------
log "5/8 安装 Nginx 与 Python"
apt-get install -y nginx python3-venv python3-pip
systemctl enable --now nginx
rm -f /etc/nginx/sites-enabled/default

# ---------------------------------------------------------------------------
log "6/8 创建部署目录"
mkdir -p /opt/im-chat/web /opt/im-chat/ai /opt/im-chat/backup
chown -R deploy:deploy /opt/im-chat
chmod 755 /opt/im-chat

# ---------------------------------------------------------------------------
log "7/8 防火墙（只开 22/80/443）"
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ufw status verbose

# ---------------------------------------------------------------------------
log "8/8 生成配置文件（密码自动生成）"
if [[ -f /opt/im-chat/im-chat.env ]]; then
    warn "/opt/im-chat/im-chat.env 已存在，不覆盖"
else
    JWT_SECRET=$(openssl rand -base64 48)
    cat > /opt/im-chat/im-chat.env <<ENV_EOF
APP_ENV=production
APP_PORT=8080

DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=im_app
DB_PASSWORD=${DB_PASS}
DB_NAME=im_chat

REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=${REDIS_PASS}
REDIS_DB=0

JWT_SECRET=${JWT_SECRET}
JWT_ACCESS_TTL_MINUTES=30
JWT_REFRESH_TTL_DAYS=14

AI_BASE_URL=http://127.0.0.1:8090
ENV_EOF
    chown deploy:deploy /opt/im-chat/im-chat.env
    chmod 600 /opt/im-chat/im-chat.env
fi

if [[ -f /opt/im-chat/ai.env ]]; then
    warn "/opt/im-chat/ai.env 已存在，不覆盖"
else
    cat > /opt/im-chat/ai.env <<'ENV_EOF'
LLM_API_BASE=https://api.deepseek.com
LLM_MODEL=deepseek-chat
LLM_API_KEY=
AI_MOCK=1
AI_TIMEOUT_SECONDS=20
ENV_EOF
    chown deploy:deploy /opt/im-chat/ai.env
    chmod 600 /opt/im-chat/ai.env
fi

# ---------------------------------------------------------------------------
echo
echo "============================================================"
echo " 服务器初始化完成"
echo "============================================================"
echo " 下一步："
echo "   1. 把数据库密码记进密码管理器（下面这份只显示一次）"
echo "      DB_PASSWORD=${DB_PASS}"
echo "   2. 在本机执行：deploy/local/release.ps1  生成发布包"
echo "   3. 上传：scp build/release/im-chat-*.tar.gz deploy@${PUBLIC_IP}:/tmp/"
echo "   4. 服务器执行：sudo bash /opt/im-chat/deploy.sh /tmp/im-chat-*.tar.gz"
echo "============================================================"
