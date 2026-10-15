-- ============================================================================
-- 000_bootstrap.sql —— 建库 + 建应用账号（只需要跑一次，需要 root 权限）
--
-- 用途：创建 im_chat 数据库，以及应用专用账号 im_app。
--       程序绝不能直接用 root 连接 —— root 权限过大，一旦泄露影响整个实例。
--
-- ⚠️ 运行前：把下面两处  CHANGE_ME_你的密码  换成你自己的强密码，
--    并把同一个密码记下来，后面要填进项目的 .env 文件（DB_PASSWORD）。
--
-- 怎么运行（Navicat）：连接 root → 新建查询 → 粘贴本文件内容 → 运行
-- ============================================================================

-- 1) 建库。字符集必须 utf8mb4，否则 emoji 存不进去（聊天里一定有 emoji）。
CREATE DATABASE IF NOT EXISTS im_chat
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_unicode_ci;

-- 2) 建应用账号。
--    'im_app'@'127.0.0.1' 而不是 'im_app'@'%' ——
--    前者只允许本机连接，从公网连不上，这是纵深防御的一层。
CREATE USER IF NOT EXISTS 'im_app'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_你的密码';

-- 3) 只授予 im_chat 库的权限，不给全局权限。
GRANT ALL PRIVILEGES ON im_chat.* TO 'im_app'@'127.0.0.1';

FLUSH PRIVILEGES;

-- 4) 顺便建一个只读账号，给自己用 Navicat 查数据时用，避免误删。
CREATE USER IF NOT EXISTS 'im_readonly'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_只读密码';
GRANT SELECT ON im_chat.* TO 'im_readonly'@'127.0.0.1';
FLUSH PRIVILEGES;

-- 5) 验证
SELECT SCHEMA_NAME AS '已创建的库', DEFAULT_CHARACTER_SET_NAME AS '字符集'
  FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = 'im_chat';

SELECT User AS '账号', Host AS '允许来源'
  FROM mysql.user WHERE User IN ('im_app', 'im_readonly');
