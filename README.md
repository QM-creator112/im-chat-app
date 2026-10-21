# IM 即时通信应用

> 基于 **Go + WebSocket + Vue 3** 的即时通信应用。
> 重点解决**消息不丢、不重、不乱序**，以及**断线重连后的增量同步**。

![CI](https://github.com/QM-creator112/im-chat-app/actions/workflows/ci.yml/badge.svg)
![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)
![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vue.js)

## 当前状态

> ⚠️ 项目正在开发中。本表随每个 Sprint 更新，**不提前勾选未完成的项**。

| 阶段 | 状态 |
| --- | --- |
| 第 0 篇 环境准备 | ✅ 完成 |
| 第 1 篇 立项 | ✅ [docs/01-立项.md](docs/01-立项.md) |
| 第 2 篇 设计 | ✅ [架构](docs/02-设计.md) · [数据模型](docs/er.md) · [接口契约](docs/api.md) |
| 产品原型 | ✅ [线框图](docs/prototype/wireframes.png) · [可交互原型](docs/prototype/prototype.html) |
| Sprint 1 账号与鉴权 | ✅ **完成**（真实环境端到端验收 12 项全通过：bcrypt 哈希 / 防枚举 / 双令牌 / 登出失效） |
| Sprint 2 长连接网关 | ✅ **完成**（真实服务器 WebSocket 验收：presence 上下线广播 / 心跳 / 连接数 / 错误信封） |
| Sprint 3 单聊可靠投递 | ✅ **完成**（真实环境 60 项验收全通过：幂等 / 事务内 seq / 增量补拉 / 分页 / 已读 / 鉴权） |
| Sprint 4 前端 Vue 3 UI | ✅ **完成**（类型检查 / 62 个测试 / 生产构建 / 真实后端契约测试 8 项 / dev 代理实测打通） |
| Sprint 5 群聊 + 已读 | ✅ **完成**（后端 16 项单测 + **真实 WS 验收 38 项全过**：4 人群 / 3 端在线投递 / 已读 1/3→2/3 / 退群后收不到 / 群主不能退） |
| Sprint 6 AI 助手侧车 | ✅ **完成**（Python FastAPI 侧车 11 项 + 真实 WS 验收 30 项全过：`@AI` 触发 / 系统消息落库 / 停掉侧车降级 / 恢复） |
| 第 5 篇 测试与压测 | ✅ **完成**（后端 107 测试 + 前端 72 + 侧车 11；**四轮真实压测**，见 [docs/05-压测报告.md](docs/05-压测报告.md)） |
| 第 7 篇 故障演练与复盘 | ✅ **完成**（杀进程 3.2~6.4s 恢复 / 停 Redis 仍可发消息但 +3s / KILL 连接池 500 后自愈；见 [docs/07-复盘.md](docs/07-复盘.md)） |
| 第 8 篇 简历回填 + 面试 | ✅ **完成**（简历三处占位符已填实测数据、1 页；[38 个面试问答](docs/08-面试问答.md)） |
| 第 6 篇 部署物料 | ✅ **完成**（systemd / Nginx / 发布 / 回滚 / 备份 / 验收脚本；已做 `sh -n` + 真实 `nginx -t` + 交叉编译 + 纯环境变量启动验证，见 [deploy/README.md](deploy/README.md)） |
| 公网部署（需云服务器） | ⬜ **待买服务器** |

> **状态标记说明**：✅ = 有可复现的验证证据（测试/实测输出）；🔄 = 代码写完但还没在真实依赖上跑通；⬜ = 未开始。

**在线演示**：部署后填这里

## 功能

- [x] 用户注册 / 登录（bcrypt 密码哈希 + JWT 双令牌）
- [x] 好友添加与好友列表
- [x] **单聊消息可靠投递**（三层幂等去重 + 事务内分配会话内单调序列号）
- [x] **服务端增量补拉 `/sync`**（含 `hasGap` 降级语义）
- [x] 会话列表（最后一条消息 / 未读数）、历史消息游标分页、已读上报
- [x] **前端断线自动重连**（指数退避 1→2→4→8→16→30s + ±20% 随机抖动）+ 重连后按游标补拉
- [x] 前端消息三态（发送中 / 已发送 / 失败重试）+ 乐观更新 + `clientMsgId` 重试复用
- [x] **群聊**：建群 / 拉人（幂等）/ 退群（群主不能退）+ 写扩散投递 + 发送者昵称
- [x] **已读回执**：`last_read_seq` + `GREATEST` 防回退；群聊显示「已读 N/M」（只算接收者）
- [x] **AI 助手**（独立 FastAPI 侧车）：`@AI` 触发 / `POST /ai` 总结，**AI 挂了聊天照常**（降级为系统消息）
- [x] 结构化日志 + 健康检查
- [ ] 消息撤回（**明确不做**，见下方「设计取舍」）

## 技术方案

| 层 | 技术 | 说明 |
| --- | --- | --- |
| 后端 | Go 1.22+ + Gin | 模块化单体：handler / service / store 三层 |
| 长连接 | gorilla/websocket | 读/写双协程，有界 channel 做背压 |
| 存储 | MySQL 8.0 | 用户 / 会话 / 消息，唯一索引兜底幂等 |
| 缓存 | Redis 7 | 在线路由、幂等快速路径、未读计数 |
| 前端 | Vue 3 + TypeScript + Vite + Pinia + Element Plus | 乐观更新 + 三态消息 |
| AI 侧车 | Python 3.12 + FastAPI | 独立服务，故障不影响聊天主链路 |
| 部署 | Nginx + systemd | 交叉编译静态二进制，`mv` 原子切换 |

### 可靠投递的三个关键设计

**① 幂等（解决重复）** — 客户端生成 UUIDv7 作为幂等键，服务端三层防护：

| 层 | 机制 | 作用 |
| --- | --- | --- |
| 1 | Redis `SETNX` | 快速拦截，性能好 |
| 2 | MySQL `uk_sender_client` 唯一索引 | Redis 失效时兜底 |
| 3 | 客户端按 `seq` 去重 | 防止 UI 重复显示 |

**② 有序（解决乱序）** — 服务端在事务内用 `SELECT ... FOR UPDATE` 分配会话内单调 `seq`。

> ⚠️ `seq` 是**单调递增**，不是**连续**。客户端用它排序和推进游标，**不用它做连续性校验**。

**③ 不丢（离线补拉）** — WebSocket 重连成功后，客户端按本地最大 `seq` 调 `/sync` 补齐缺失消息。

详细链路见 [docs/02-设计.md](docs/02-设计.md)。

## 实测数据

> **全部来自实测**，原始 JSON 与采样 CSV 见 [docs/loadtest/](docs/loadtest/)，
> 完整报告（含瓶颈分析）见 [docs/05-压测报告.md](docs/05-压测报告.md)。
>
> ⚠️ **压测客户端与服务端在同一台机器上**（i5-12500H / 16 线程 / 15.7 GB / Windows 11），
> 两者争抢 CPU，因此这些数字是**本地单机上限，不代表生产环境**。

| 指标 | 结果 |
| --- | --- |
| **并发长连接** | **5000 条同时在线**（goroutine 9 → 10009 → 断连后回落 87，无泄漏；约 20 KB 堆/连接） |
| **端到端延迟 P50 / P95 / P99** | **46 ms / 126 ms / 164 ms**（200 连接）<br>940 ms / 3689 ms / 4563 ms（1000 连接） |
| **消息吞吐** | **387 条/秒**（200 连接）· 425 条/秒（1000 连接，拐点） |
| 连续消息零丢失 | ✅ 2000 条 / 10000 条全部 ack，DB 中 seq 连续无缺口、`client_msg_id` **零重复** |
| 并发 seq 唯一性 | ✅ 20 协程 × 50 条 = 1000 条，`COUNT(DISTINCT seq)` = 1000（`TestSendConcurrentSeqUnique`，1.70s） |
| 超载时的行为 | 5000 连接时 16835 次发送**明确返回错误**（`context deadline exceeded`）而非静默丢弃；DB 侧 13489 条消息 seq 仍连续、0 重复 |

**瓶颈（实测定位）**：不是 CPU（1000 连接时仅 38.5%），而是
① 单会话 `SELECT ... FOR UPDATE` 行锁串行（吞吐上限 ≈ 430 条/秒）；
② `maxOpenConns = 25` 的数据库连接池在高并发下排队超时。详见报告 §3。

复现：
```powershell
.\build\loadtest_round.ps1 -Label r1_200  -Conns 200  -PerConn 10 -RampUp 5s  -Hold 3s
.\build\loadtest_round.ps1 -Label r4_cap   -Conns 5000 -PerConn 0  -RampUp 20s -Hold 25s
```

## 部署（第 6 篇）

完整手册见 **[deploy/README.md](deploy/README.md)**。

```powershell
# 本地打包（交叉编译 linux/amd64 + 前端构建 + 密钥扫描 + 打包）
.\deploy\local\release.ps1

# 上传后由服务器端脚本发布（原子切换 + 健康检查 + 失败自动回滚）
scp build\release\im-chat-v0.7.0.tar.gz deploy@<IP>:/tmp/
ssh deploy@<IP> "sudo bash /opt/im-chat/deploy.sh /tmp/im-chat-v0.7.0.tar.gz"
```

| 组件 | 文件 | 关键点 |
| --- | --- | --- |
| systemd | `deploy/systemd/im-chat.service` | `Restart=always` + `NoNewPrivileges` + `ProtectSystem=strict` |
| Nginx | `deploy/nginx/im-chat.conf` | **WebSocket 三件套**：`Upgrade` 头 / `proxy_read_timeout 300s` / `proxy_buffering off` |
| 发布 | `deploy/scripts/deploy.sh` | 先传临时名再 `mv` 原子切换，保留 `.prev`，健康检查失败**自动回滚** |
| 备份 | `deploy/scripts/backup.sh` | 每天自动备份，**并恢复到临时库验证可用** |
| 验收 | `deploy/scripts/verify-production.sh` | 18 条上线检查清单自动化 |

## 快速开始

### 前置

- Go 1.22+
- Node 18+
- MySQL 8.0+ / Redis 6+

### 步骤

```powershell
# 1. 建库 + 建应用账号（需要 MySQL root 权限）
#    在 Navicat 里运行 server/migrations/000_bootstrap.sql
#    （记得先把里面的 CHANGE_ME_你的密码 换成你自己的密码）

# 2. 建表
#    运行 server/migrations/001_init.sql，末尾应显示「表数量 6」

# 3. 配置
Copy-Item .env.example .env
# 编辑 .env：填 DB_PASSWORD 与 JWT_SECRET

# 4. 启动后端
cd server
go mod download
go run ./cmd/server

# 5. 启动前端（另开终端）
cd web
npm install
npm run dev
# 打开 http://localhost:5173
```

> **Windows 上构建前端报 `[vite:esbuild-transpile] remove ...Temp\esbuild-xxx: Access is denied`**
> 这是 esbuild 无法删除系统 temp 下的自身副本（常见于杀软/受限环境）。把临时目录指到工作区内即可：
> ```powershell
> $env:TEMP = "$PWD\..\.tmp"; $env:TMP = $env:TEMP
> npm run build
> ```
> `npm run dev` 不受影响，只有生产构建会用到临时文件。

### 前端验证命令

```powershell
cd web
npm run type-check   # vue-tsc 类型检查
npm test             # vitest 单元测试（退避 / UUIDv7 / 消息去重 / WS 重连状态机）
npm run build        # 生产构建
```

> `src/api/contract.test.ts` 会连真实后端验证「前端代码 ↔ 接口契约」一致性；
> **后端没启动时这些用例会 SKIP（不是假通过）**。

### AI 侧车（Sprint 6）

```powershell
cd ai
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt

# 有三种运行模式，由环境变量决定（/health 会如实报告）
$env:AI_MOCK = "1"        # 没 key 时的模拟模式：返回**标注为 [MOCK]** 的结果，仅用于验证链路
# $env:LLM_API_KEY = "sk-..."   # 有 key 时走真实大模型
uvicorn main:app --host 127.0.0.1 --port 8090
```

| 模式 | 条件 | 行为 |
| --- | --- | --- |
| `real` | 配了 `LLM_API_KEY` | 真实调用大模型 |
| `mock` | 没 key 但 `AI_MOCK=1` | 返回 **`[MOCK]` 标注**的模拟结果 —— **这不是大模型能力，只是验证链路通** |
| `disabled` | 两者都没有 | 接口返回 503，Go 侧降级为「AI 助手暂时不可用」的系统消息 |

> **故障隔离的体现**：把侧车停掉，聊天完全正常。
> `@AI` 会得到一条「AI 助手暂时不可用」的**系统消息**（而不是报错），侧车恢复后自动可用。

## 目录结构

```text
├── server/          Go 后端
│   ├── cmd/         入口（server / loadtest）
│   ├── internal/    config · model · store · handler · ws · service · middleware
│   └── migrations/  SQL 迁移脚本（按序号）
├── web/             Vue 3 前端（api / stores / views / components）
├── ai/              Python AI 侧车（FastAPI）
├── docs/            立项 · 架构 · 数据模型 · 接口契约 · 复盘 · 原型
└── .github/         CI 与 PR 模板
```

## 设计取舍（为什么不做某些东西）

| 没做 | 为什么 |
| --- | --- |
| 微服务 | 一个人一个进程足够。**过早拆分会把调试成本放大数倍** |
| 移动端 | Web 端已能证明端到端能力；移动端是平台适配问题，不是架构问题 |
| 实时音视频 | 音视频应外购 SFU；自研不可行（webrtc-rs 官方承认内存泄漏） |
| E2EE | 涉及密钥管理与多设备一致性，工作量远超收益 |
| 消息撤回 | 纯增量功能，优先级让给了群聊与已读回执 |
| 恰好一次投递 | 分布式下代价过高；用「至少一次 + 幂等去重」等价实现 |
| Docker | 只有一个二进制和一个静态目录，systemd 运维面更小 |

## 开发过程

本项目按公司流程推进：立项（PRD）→ 设计（ADR + 契约）→ 6 个 Sprint → 测试与压测 → 部署上线 → 复盘。

- 提交历史：`git log --oneline`（Conventional Commits）
- 迭代看板：见 GitHub Projects
- 结项复盘：部署后补 `docs/postmortem.md`

## 文档索引

| 文档 | 内容 |
| --- | --- |
| [docs/01-立项.md](docs/01-立项.md) | 项目章程、用户故事与验收条件、MoSCoW、排期、风险登记表 |
| [docs/02-设计.md](docs/02-设计.md) | 技术选型 ADR、架构分层、消息链路时序、四个核心设计决策 |
| [docs/er.md](docs/er.md) | ER 图、六张表的职责、索引与分表策略 |
| [docs/api.md](docs/api.md) | REST 接口契约、WebSocket 协议、错误码 |
| [docs/prototype/](docs/prototype/) | 产品原型（线框图 + 可交互 HTML） |
