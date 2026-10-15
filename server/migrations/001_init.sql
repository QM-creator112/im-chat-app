-- ============================================================================
-- 001_init.sql —— 基础表结构
--
-- 约定（全库一致，不要破例）：
--   1. 时间统一用 DATETIME(3)：毫秒精度，与前端 JS 的时间戳直接对齐
--   2. 字符集统一 utf8mb4：否则 emoji 存不进去
--   3. 主键统一 BIGINT UNSIGNED
--   4. 索引命名：uk_ 唯一索引 / idx_ 普通索引
--
-- 共 6 张表：
--   users                 用户
--   conversations         会话（单聊 / 群聊）
--   conversation_members  会话成员（含已读位置）
--   messages              消息（含会话内序列号与幂等键）
--   friendships           好友关系
--   user_devices          登录设备与刷新令牌
--
-- 怎么运行（Navicat）：连接 → 选中 im_chat 库 → 新建查询 → 粘贴 → 运行
-- ============================================================================

USE im_chat;

-- ---------- 用户 ----------
CREATE TABLE IF NOT EXISTS users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username      VARCHAR(32)     NOT NULL              COMMENT '登录名，唯一',
    password_hash VARCHAR(60)     NOT NULL              COMMENT 'bcrypt 哈希，固定 60 字符',
    nickname      VARCHAR(32)     NOT NULL DEFAULT ''   COMMENT '昵称',
    avatar_url    VARCHAR(255)    NOT NULL DEFAULT ''   COMMENT '头像地址',
    created_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                  ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='用户';

-- ---------- 会话 ----------
CREATE TABLE IF NOT EXISTS conversations (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    type       TINYINT UNSIGNED NOT NULL               COMMENT '1=单聊 2=群聊',
    title      VARCHAR(64)      NOT NULL DEFAULT ''    COMMENT '群名（单聊为空）',
    owner_id   BIGINT UNSIGNED  NOT NULL DEFAULT 0     COMMENT '群主（单聊为 0）',
    -- dm_key 用 NULL 表示群聊：MySQL 的唯一索引允许多个 NULL，
    -- 所以群聊可以有很多条，而单聊只有一条。用空字符串就错了 ——
    -- 第二个群会直接插不进去。
    dm_key     VARCHAR(50)      NULL                   COMMENT '单聊唯一键 "小id:大id"；群聊为 NULL',
    -- 会话内序列号分配器：由服务端在事务里递增，客户端不能生成。
    next_seq   BIGINT UNSIGNED  NOT NULL DEFAULT 0     COMMENT '会话内序列号分配器',
    created_at DATETIME(3)      NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3)      NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_dm_key (dm_key),
    KEY idx_updated (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='会话（单聊/群聊）';

-- ---------- 会话成员 ----------
CREATE TABLE IF NOT EXISTS conversation_members (
    conversation_id BIGINT UNSIGNED NOT NULL,
    user_id         BIGINT UNSIGNED NOT NULL,
    -- 已读是「每个人读到哪」，是成员属性，不是消息属性。
    -- 如果在 messages 上加 is_read 布尔字段，群聊里一条消息有 N 个已读状态，
    -- 一个布尔值根本表达不了。
    last_read_seq   BIGINT UNSIGNED NOT NULL DEFAULT 0  COMMENT '我已读到哪一条（已读回执的基础）',
    joined_at       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (conversation_id, user_id),
    KEY idx_user (user_id, conversation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='会话成员';

-- ---------- 消息 ----------
CREATE TABLE IF NOT EXISTS messages (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    conversation_id BIGINT UNSIGNED NOT NULL,
    seq             BIGINT UNSIGNED NOT NULL              COMMENT '会话内单调递增，由服务端分配',
    sender_id       BIGINT UNSIGNED NOT NULL,
    client_msg_id   CHAR(36)        NOT NULL              COMMENT '客户端生成的 UUIDv7，幂等键',
    content_type    TINYINT UNSIGNED NOT NULL DEFAULT 1   COMMENT '1=文本 2=图片 3=文件 4=语音 5=系统消息',
    content         TEXT            NOT NULL,
    created_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    -- 会话内 seq 唯一：既是顺序保证，也是「不重」的数据库兜底。
    -- 不要再单独建 idx_conv_seq —— 这个唯一索引的前缀就是 (conversation_id, seq)，
    -- 再加一个纯属冗余，只会拖慢写入。
    UNIQUE KEY uk_conv_seq (conversation_id, seq),
    -- 同一发送者同一 client_msg_id 只能有一条：幂等的最后一道防线。
    -- 即使 Redis 整个挂掉，也不会产生重复消息。
    UNIQUE KEY uk_sender_client (sender_id, client_msg_id),
    KEY idx_conv_created (conversation_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='消息';

-- ---------- 好友关系（双向存两条） ----------
CREATE TABLE IF NOT EXISTS friendships (
    user_id    BIGINT UNSIGNED NOT NULL,
    friend_id  BIGINT UNSIGNED NOT NULL,
    status     TINYINT UNSIGNED NOT NULL DEFAULT 1   COMMENT '1=已通过 0=待通过',
    created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (user_id, friend_id),
    KEY idx_friend (friend_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='好友关系';

-- ---------- 登录设备（refresh token） ----------
CREATE TABLE IF NOT EXISTS user_devices (
    id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id            BIGINT UNSIGNED NOT NULL,
    -- 存哈希不存明文：即使数据库泄露，攻击者也拿不到能直接用的 refresh token。
    refresh_token_hash CHAR(64)        NOT NULL   COMMENT 'SHA-256 后的 refresh token',
    user_agent         VARCHAR(255)    NOT NULL DEFAULT '',
    expires_at         DATETIME(3)     NOT NULL,
    created_at         DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_token (refresh_token_hash),
    KEY idx_user (user_id),
    KEY idx_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='登录设备与刷新令牌';

-- ---------- 验证 ----------
SHOW TABLES;
SELECT COUNT(*) AS '表数量（应为 6）'
  FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'im_chat';
