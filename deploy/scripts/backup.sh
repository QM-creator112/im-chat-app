#!/usr/bin/env bash
# ============================================================================
# 每日数据库备份（保留 7 天），并且**验证备份可用**
#
# 用法：
#   手动跑一次：sudo bash /opt/im-chat/backup.sh
#   加定时任务：sudo crontab -e  然后加这一行：
#     0 3 * * * /opt/im-chat/backup.sh >> /var/log/im-chat-backup.log 2>&1
#
# 为什么「验证备份」和「做备份」同样重要：
#   一个从来没恢复过的备份文件，你并不知道它能不能用。
#   等到真出事才发现备份是坏的，那是最坏的情况。
# ============================================================================
set -euo pipefail

BACKUP_DIR=/opt/im-chat/backup
ENV_FILE=/opt/im-chat/im-chat.env
KEEP=7
VERIFY=${VERIFY:-1}   # VERIFY=0 可跳过恢复验证（备份窗口紧张时用）

mkdir -p "$BACKUP_DIR"
DATE=$(date +%Y%m%d-%H%M%S)
FILE="$BACKUP_DIR/im_chat-$DATE.sql.gz"

log() { echo "[$(date '+%F %T')] $*"; }

if [[ ! -f "$ENV_FILE" ]]; then
    echo "找不到 $ENV_FILE" >&2
    exit 1
fi

# ⚠️ 密码从环境文件读，不写在脚本里（脚本可能被提交/被看见）
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

log "开始备份 $DB_NAME → $FILE"

# --single-transaction：一致性快照，且**不锁表**，不影响线上读写
if ! mysqldump -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
        --single-transaction --routines --triggers --quick \
        --set-gtid-purged=OFF "$DB_NAME" 2>/dev/null | gzip > "$FILE"; then
    echo "备份失败" >&2
    rm -f "$FILE"
    exit 1
fi

SIZE=$(du -h "$FILE" | cut -f1)
log "备份完成: $FILE ($SIZE)"

# ---------------------------------------------------------------------------
# 验证：恢复到一个临时库，数一下行数，然后删掉
# ---------------------------------------------------------------------------
if [[ "$VERIFY" == "1" ]]; then
    VERIFY_DB="im_chat_restore_verify"
    log "验证备份可用性（恢复到临时库 $VERIFY_DB）"
    if ! mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
            -e "DROP DATABASE IF EXISTS $VERIFY_DB; CREATE DATABASE $VERIFY_DB;" 2>/dev/null; then
        echo "无法创建验证库" >&2
        exit 1
    fi
    if gunzip -c "$FILE" | mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
            "$VERIFY_DB" 2>/dev/null; then
        ROWS=$(mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
               -N -B -e "SELECT COUNT(*) FROM $VERIFY_DB.messages;" 2>/dev/null || echo "?")
        log "验证通过：恢复后 messages 表有 $ROWS 行"
        mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
              -e "DROP DATABASE $VERIFY_DB;" 2>/dev/null || true
    else
        echo "备份文件无法恢复 —— 这是严重问题，请立即排查" >&2
        mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASSWORD" \
              -e "DROP DATABASE IF EXISTS $VERIFY_DB;" 2>/dev/null || true
        exit 1
    fi
fi

# ---------------------------------------------------------------------------
# 清理旧备份
# ---------------------------------------------------------------------------
DELETED=$(ls -1t "$BACKUP_DIR"/im_chat-*.sql.gz 2>/dev/null | tail -n +$((KEEP + 1)) | wc -l)
ls -1t "$BACKUP_DIR"/im_chat-*.sql.gz 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f
log "清理旧备份 ${DELETED} 个，保留最近 ${KEEP} 个"

log "备份任务结束"
