# 部署手册（deploy/）

> 目标：**从一台空服务器到「公网浏览器能打开并聊天」**，全程可照抄。
> 所有命令都已在本机验证过语法（`sh -n` / 真实 `nginx -t` / 交叉编译 + ELF 校验 / 纯环境变量启动）。
> **尚未验证的是「在真实云服务器上跑通」** —— 那需要一台服务器，等你买到后我们一起过一遍。

## 目录结构

```text
deploy/
├── systemd/
│   ├── im-chat.service          # Go 后端（Restart=always + 安全加固）
│   └── im-chat-ai.service       # AI 侧车（只监听 127.0.0.1）
├── nginx/
│   └── im-chat.conf             # 站点配置（WebSocket 三个关键项都配了）
├── env/
│   ├── im-chat.env.example      # 生产环境变量模板（种进 /opt/im-chat/im-chat.env）
│   └── ai.env.example           # 侧车环境变量模板
├── scripts/
│   ├── server-init.sh           # 服务器一次性初始化（装环境/建库/加固/防火墙）
│   ├── deploy.sh                # 发布：解包 → 原子切换 → 重启 → 健康检查 → 失败自动回滚
│   ├── rollback.sh              # 手动回滚
│   ├── backup.sh                # 每日备份 + **验证备份可恢复**
│   └── verify-production.sh     # 上线检查清单自动化（18 条）
└── local/
    └── release.ps1              # 本地：交叉编译 + 前端构建 + 打包 + 密钥扫描
```

## 0. 前置

| 项 | 要求 |
| --- | --- |
| 服务器 | Ubuntu 22.04 / 24.04，**2 核 4G** 建议（1 核 2G 能跑但紧） |
| 安全组 | 只开 **22 / 80**（有 HTTPS 再开 443）；**3306 / 6379 / 8080 绝不开放** |
| SSH | 用密钥登录，不用密码 |
| 本地 | Go 1.22+、Node 18+、`tar`（Windows 10+ 自带） |

## 1. 服务器初始化（一次性）

```bash
# 用你的公网 IP 替换
sudo bash server-init.sh 1.2.3.4
```

它会：装依赖 → 建 `deploy` 用户 → 建库和 `im_app`/`im_readonly` 账号（**只允许 127.0.0.1**）
→ 装并加固 Redis（`bind 127.0.0.1` + 密码 + 禁用 `FLUSHALL`/`CONFIG`）
→ 装 Nginx/Python → 建 `/opt/im-chat` → 配 ufw（只开 22/80/443）→ 生成 `/opt/im-chat/im-chat.env`

> ⚠️ 脚本会**自动生成**数据库密码和 Redis 密码并写进 `/opt/im-chat/im-chat.env`（权限 600）。
> 跑完把密码记进密码管理器 —— 屏幕上的那份只显示一次。

## 2. 本地打包

```powershell
# 在仓库根目录
.\deploy\local\release.ps1
```

做四件事，每一步都会自检：

| 步骤 | 自检 |
| --- | --- |
| 交叉编译 `linux/amd64`（`CGO_ENABLED=0` 静态链接） | 检查 ELF magic + 机器码 `x86-64` |
| 前端 `vue-tsc` + `vite build` | 类型检查不通过就中止 |
| 打包 `build/release/im-chat-<tag>.tar.gz` | 检查必需文件齐全、无 `.env`/`.venv`/`node_modules`/`migrations/local` |
| **密钥扫描** | 包里出现硬编码密码或 `sk-` key 就**拒绝打包** |

> `migrations/local/` 里是本地开发用的真实密码，已经被排除 —— 这个泄漏是本项目的发布脚本**真的踩到并修掉**的。

## 3. 上传并发布

```powershell
scp build\release\im-chat-v0.7.0.tar.gz deploy@<IP>:/tmp/
ssh deploy@<IP> "sudo bash /opt/im-chat/deploy.sh /tmp/im-chat-v0.7.0.tar.gz"
```

> 首次部署前要先把这个 `deploy.sh` 传上去：
> `scp deploy\scripts\deploy.sh deploy@<IP>:/tmp/ && ssh deploy@<IP> "sudo install -m755 /tmp/deploy.sh /opt/im-chat/deploy.sh"`

`deploy.sh` 的 8 步：解包到临时目录 → 装 systemd 单元 → 装 Nginx 配置（**先 `nginx -t` 再 reload**）
→ 跑迁移 → **备份旧版本为 `.prev`** → `mv` 原子切换 → 重启 + 等健康检查 → **失败自动回滚**。

## 4. 回滚

```bash
sudo bash /opt/im-chat/rollback.sh    # 或 deploy/scripts/rollback.sh
```

一条 `mv` + 一次 `restart`，秒级完成。坏版本会保留成 `im-server-broken-<时间戳>` 供排查。

## 5. 备份

```bash
# 手动跑一次
sudo bash /opt/im-chat/backup.sh

# 加定时任务（每天 3 点）
sudo crontab -e
# 加这一行：
0 3 * * * /opt/im-chat/backup.sh >> /var/log/im-chat-backup.log 2>&1
```

> 备份脚本默认会**把备份恢复到一个临时库验证一遍**再算成功。
> 「没验证过的备份等于没有备份」这句话，面试时也可以讲。

## 6. 上线验收

```bash
sudo bash /opt/im-chat/verify-production.sh <公网IP>
```

自动检查能验的（服务状态 / 健康 / 版本号 / 公网 HTTP / Nginx 三个 WebSocket 配置项 /
MySQL 和 Redis 是否暴露 / ufw / 备份 / 日志 / 配置文件权限），
剩下的**必须人工验**：两个浏览器互发消息、挂 3 分钟再发、断网重连、刷新不 404。

## 7. 三个最容易踩的坑（都是真事故）

| # | 坑 | 现象 | 原因 |
| --- | --- | --- | --- |
| 1 | 漏了 `Upgrade`/`Connection` 头 | 本地好、线上 WebSocket 握手 400 | Nginx 不自动转发升级头 |
| 2 | 漏了 `proxy_read_timeout` | **能连上，但 60 秒后必断，日志里没有错误** | Nginx 默认 60s 超时，静默断开 |
| 3 | 漏了 `proxy_buffering off` | 消息延迟明显 | 响应被攒起来批量发 |

> 第 2 条最难查 —— 「过 60 秒就断」看起来像心跳 bug，其实是 Nginx。**记住这个数字：60 秒。**

## 8. 已知限制（要主动说，别等被问）

| 限制 | 说明 |
| --- | --- |
| **没有 HTTPS** | 只有 IP 没有域名时无法签发证书。此时 WebSocket 是 `ws://`，token 走 query string **明文传输**。仅用于演示可接受，但要主动说明。 |
| 发布有秒级中断 | 单实例重启。客户端会断线重连（Sprint 4 做的），所以体验接近无缝，但**不是真正的零停机**。 |
| AI 侧车可能是 mock 模式 | 没配 `LLM_API_KEY` 时返回带 `[MOCK]` 标记的内容，**不代表模型能力**。 |
| 单机部署 | 没有主从、没有负载均衡。挂了就是挂了（`Restart=always` 能兜住崩溃，兜不住宕机）。 |
---

## 9. 没有服务器时，怎么验证 Nginx 配置？（**本机 nginx + 公网隧道**）

那三个 WebSocket 坑光看文档是不够的 —— **必须真的让一条 WS 连接穿过去**。
买服务器之前，可以用本机的 Windows 版 nginx 把这条路走通。

### 9.1 本机验证（脚本已封装）

```powershell
# 前置：后端已在 127.0.0.1:8080 跑起来，前端已 npm run build
powershell -ExecutionPolicy Bypass -File deploy\local\verify-nginx-local.ps1
```

脚本做 9 步、18 项断言：

| 步骤 | 验证内容 |
| --- | --- |
| 1-2 | 准备纯 ASCII 路径（nginx for Windows **不支持中文路径**，用目录联接 `D:\imngx` 指向工作区）|
| 2 | `nginx -t` 退出码必须为 0 |
| 4 | 静态文件 / `/api` 代理 / `/healthz` / `/chat` SPA 回落 |
| 5 | **坑 #1**：WS 握手返回 `Open`（缺 `Upgrade` 头会 400）|
| 6 | **坑 #3**：`send_ack` 往返成功（缺 `proxy_buffering off` 会明显延迟）|
| 7 | **坑 #2**：空闲 N 秒后连接仍 `Open`（默认 `proxy_read_timeout 60s` 会静默断开）|
| 8 | 检查 `access.log` 里出现 `101 Switching Protocols` |

**实测结果：18/18 PASS。** access.log 里的证据：

```text
"GET /ws?token=*** HTTP/1.1" 101 338
```

> 脚本只停自己启动的 nginx（读 `logs/nginx.pid`），不会误杀别人正在用的实例。

### 9.2 公网验证（无需买服务器）

用 `localhost.run`（SSH 隧道，**不需要注册**）把本机 80 端口映射到公网：

```powershell
ssh -o StrictHostKeyChecking=no -R 80:localhost:80 nokey@localhost.run
# 输出里会给出一个 https://xxxx.lhr.life 地址
```

**实测结果**（2026-10-21）：

| 验证 | 结果 |
| --- | --- |
| 公网 HTTPS 首页 | `GET https://xxxx.lhr.life/` → 200，页面正常 |
| 公网 API | `/api/v1/auth/login` → `code=0` |
| **公网 WSS 握手** | `wss://xxxx.lhr.life/ws` → Open（TLS + Upgrade + nginx 全链路通）|
| 公网消息往返 | `send_ack` seq 递增，往返 697ms |
| **公网空闲 70 秒** | 连接仍存活，再发消息成功 |

> ⚠️ **这只证明「配置正确、公网能访问」，不等于「部署到云服务器」。**
> 隧道地址是临时的（进程一停就失效），也不验证 systemd / 域名 / 长期稳定性。
> **简历和面试里要明确区分这两件事。**
