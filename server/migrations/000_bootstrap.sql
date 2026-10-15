-- ============================================================================
-- 000_bootstrap.sql —— 建库 + 建应用账号（只需要跑一次，需要 root 权限）
--
-- 用途：创建 im_chat 数据库，以及应用专用账号 im_app。
--       程序绝不能直接用 root 连接 —— root 权限过大，一旦泄露影响整个实例。
--
-- ⚠️ 运行前：把下面  CHANGE_ME_你的密码 / CHANGE_ME_只读密码  换成你自己的强密码，
--    并把 im_app 的密码记下来，填进项目的 .env 文件（DB_PASSWORD）。
--
-- 怎么运行（Navicat）：连接 root → 新建查询 → 粘贴本文件内容 → 运行
-- ============================================================================

-- 1) 建库。字符集必须 utf8mb4，否则 emoji 存不进去（聊天里一定有 emoji）。
CREATE DATABASE IF NOT EXISTS im_chat
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_unicode_ci;

-- 2) 建应用账号。
--
-- ⚠️⚠️ 为什么必须给 'localhost' 和 '127.0.0.1' **各建一个**：
--
-- 这是本项目真实踩过的坑。踩坑现象是：
--     程序报 Access denied for user 'im_app'@'localhost'
-- 但脚本里只建了 'im_app'@'127.0.0.1' —— 两个 Host 不匹配，账号被判为不存在。
--
-- 原因：MySQL 判断客户端身份用的是**它自己解析出来的主机名**，而不是你连接串里写的地址。
-- 当 skip_name_resolve=OFF（默认）时，从 127.0.0.1 进来的 TCP 连接
-- 会被反向解析成 'localhost'，于是 MySQL 去找 'im_app'@'localhost'，
-- 找不到就报 1045。
--
-- 两个都建的成本几乎为零，但能省掉一次非常难定位的排查。
--
-- 为什么不用 'im_app'@'%'：
-- 那会允许从任意 IP 连接，等于把数据库暴露到公网。只给本机是纵深防御的一层。
CREATE USER IF NOT EXISTS 'im_app'@'localhost' IDENTIFIED BY 'CHANGE_ME_你的密码';
CREATE USER IF NOT EXISTS 'im_app'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_你的密码';

-- ALTER USER 是为了「账号已存在但密码不对」时也能修正密码，让本脚本可重复执行。
-- 只用 CREATE USER IF NOT EXISTS 的话，账号已存在时密码不会被更新。
ALTER USER 'im_app'@'localhost' IDENTIFIED BY 'CHANGE_ME_你的密码';
ALTER USER 'im_app'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_你的密码';

-- 3) 只授予 im_chat 库的权限，不给全局权限。
GRANT ALL PRIVILEGES ON im_chat.* TO 'im_app'@'localhost';
GRANT ALL PRIVILEGES ON im_chat.* TO 'im_app'@'127.0.0.1';

FLUSH PRIVILEGES;

-- 4) 顺便建一个只读账号，给自己用 Navicat 查数据时用，避免误删。
CREATE USER IF NOT EXISTS 'im_readonly'@'localhost' IDENTIFIED BY 'CHANGE_ME_只读密码';
CREATE USER IF NOT EXISTS 'im_readonly'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_只读密码';
ALTER USER 'im_readonly'@'localhost' IDENTIFIED BY 'CHANGE_ME_只读密码';
ALTER USER 'im_readonly'@'127.0.0.1' IDENTIFIED BY 'CHANGE_ME_只读密码';

GRANT SELECT ON im_chat.* TO 'im_readonly'@'localhost';
GRANT SELECT ON im_chat.* TO 'im_readonly'@'127.0.0.1';
FLUSH PRIVILEGES;

-- 5) 验证。下面两条查询应当各返回 2 行、2 行。
SELECT SCHEMA_NAME AS '已创建的库', DEFAULT_CHARACTER_SET_NAME AS '字符集'
  FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = 'im_chat';

SELECT User AS '账号', Host AS '允许来源'
  FROM mysql.user WHERE User IN ('im_app', 'im_readonly')
  ORDER BY User, Host;
