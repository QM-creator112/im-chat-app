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
| Sprint 6 AI 助手侧车 | ⬜ 未开始 |
| 部署上线 | ⬜ 未开始 |

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
- [ ] AI 助手（会话总结，独立侧车服务 · Sprint 6）
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

> ⚠️ **待压测后回填**。所有数字必须来自真实运行，不接受估算。
> 压测客户端与服务端在同一台机器上时，两者争抢 CPU，需在结论中写明这一限制。

| 指标 | 结果 |
| --- | --- |
| 并发长连接 | 【实测后回填】 |
| 端到端延迟 P50 / P95 / P99 | 【实测后回填】 / 【实测后回填】 / 【实测后回填】 |
| 连续消息零丢失 | 【实测后回填】（1000 条对账，无重复无空洞） |
| 并发 seq 唯一性 | 【实测后回填】（20 协程 × 50 条，distinct seq = 1000） |

复现方式：`go run ./cmd/loadtest -conns 200 -per-conn 10 -tokens tokens.txt`

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
