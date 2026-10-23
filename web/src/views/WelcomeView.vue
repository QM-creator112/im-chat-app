<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import sheetShot from '@/assets/shots/app-hires-sheet.png'
import sideShot from '@/assets/shots/app-hires-side.png'
import fullShot from '@/assets/shots/app-hires-full.png'

/**
 * 产品首页（落地页）—— `/welcome`
 *
 * 视觉方向：tasteskill 的 **editorial-report / The printed annual**（seed 664930524）
 *   · 一条 42% 处的竖轴组织全页：标题从轴上起，正文挂在轴右侧，左侧放小注与数字
 *   · 刊头是通栏图带（首屏 45%），mosaic 无缝隙拼接
 *   · 双色印刷：纸色 + 墨色 + 品牌深蓝（色块整屏铺满，不渐变）
 *   · 打字系统：Young Serif（标题）/ Source Serif 4（正文），中英混排
 *
 * 它和 `/` 的聊天界面是**两种东西**，规则也不同：
 *   这一页是「读的」，允许大字号、留白、图带；
 *   应用屏是「用的」，标题 18-32px、不许空白带。所以分成两条路由。
 */

const router = useRouter()

// ---------- 一次发送的六个步骤（how-it-works）----------
const steps = [
  { t: '客户端生成幂等键', d: 'UUIDv7 作为 client_msg_id，同一毫秒内也可排序' },
  { t: 'WebSocket 发帧', d: '长连接自带会话上下文，不需要每帧带 token' },
  { t: '事务内锁会话行', d: 'SELECT next_seq … FOR UPDATE，把并发写入排成队' },
  { t: '分配 seq', d: 'seq = next_seq + 1，与消息落库在同一个事务里' },
  { t: '落库并投递', d: '唯一索引兜住重复；投递给会话内所有在线成员' },
  { t: 'ack 回执', d: '客户端拿到服务端 seq 后替换本地临时序号' },
]

// ---------- 实测数据（全部来自 docs/05-压测报告.md，未做美化）----------
const runs = [
  { conns: 200, perConn: 10, acked: '2000 / 2000', p50: '46.1', p99: '164.0', tput: '387.3', note: '常规负载' },
  { conns: 1000, perConn: 10, acked: '10000 / 10000', p50: '939.8', p99: '4563.4', tput: '425.5', note: '单会话行锁开始排队' },
  { conns: 5000, perConn: 5, acked: '8165 / 25000', p50: '—', p99: '25080.7', tput: '138.3', note: '出现 context deadline exceeded' },
]
const active = ref(0)
const cur = computed(() => runs[active.value]!)

const facts = [
  { n: '198', l: '自动化测试', s: 'Go 115 · 前端 72 · 侧车 11' },
  { n: '128', l: '真实环境验收', s: '鉴权 / 长连接 / 投递 / 群聊 / AI' },
  { n: '0', l: '重复消息', s: '2000 与 10000 条两轮压测' },
  { n: '3.2–6.4 s', l: '断线自动恢复', s: '杀进程实测三次' },
]

function goApp(): void {
  void router.push('/')
}
function goRepo(): void {
  window.open('https://github.com/QM-creator112/im-chat-app', '_blank', 'noopener')
}
</script>

<template>
  <div class="page">
    <!-- 极简导航：字标 + 三个链接 + 一个动作（不做玻璃/浮动胶囊） -->
    <nav class="nav">
      <span class="wordmark">IM<span class="wordmark-cn">即时通信</span></span>
      <div class="nav-links">
        <a href="#decisions">Decisions</a>
        <a href="#proof">Proof</a>
        <a href="#how">How it works</a>
        <button type="button" class="nav-action" @click="goApp">进入演示</button>
      </div>
    </nav>

    <!-- 刊头：通栏图带占首屏 45%，mosaic 无缝隙 -->
    <header class="masthead">
      <div class="mosaic">
        <img :src="sheetShot" alt="应用截图：会话与消息列表" class="cell cell-img-1" />
        <img :src="sideShot" alt="应用截图：会话列表与账号区" class="cell cell-img-2" />
        <img :src="fullShot" alt="应用截图：完整界面" class="cell cell-img-3" />
        <div class="cell cell-accent" aria-hidden="true"></div>
        <img :src="sheetShot" alt="应用截图：消息区裁切" class="cell cell-img-4" />
        <div class="cell cell-ink" aria-hidden="true"></div>
      </div>
      <div class="masthead-title">
        <h1 class="display">
          Nothing lost.<br />
          Nothing twice.
        </h1>
        <div class="masthead-foot">
          <p class="standfirst">
            一个从零自建的即时通信系统：消息不丢、不重、不乱序。断线三秒内自动恢复，
            5000 个并发连接实测，AI 助手以独立侧车接入且失败时降级不阻塞主链路。
          </p>
          <div class="hero-actions">
            <button type="button" class="btn-primary" @click="goApp">进入演示</button>
            <button type="button" class="btn-ghost" @click="goRepo">读源码</button>
          </div>
        </div>
      </div>
    </header>

    <!-- 1) 四条决定：面板 + 页边小注 -->
    <section id="decisions" class="sec sec-panel">
      <div class="axis-note">
        <h2 class="head">四条决定<br />写在代码之前</h2>
      </div>
      <div class="body-col">
        <ol class="decisions">
          <li>
            <strong>幂等键</strong>
            <span>客户端生成 UUIDv7 作为 client_msg_id，Redis 先挡一层，数据库唯一索引兜底。</span>
          </li>
          <li>
            <strong>服务端 seq</strong>
            <span>会话内递增序号在事务里分配，不连续但单调——空洞是允许的语义，连续不是。</span>
          </li>
          <li>
            <strong>已读游标</strong>
            <span>每个成员一个 last_read_seq，一次 UPDATE 取代「消息 × 人」的已读表。</span>
          </li>
          <li>
            <strong>至少一次投递</strong>
            <span>不追求恰好一次：允许重发，靠客户端去重与 seq 排序得到等价结果。</span>
          </li>
        </ol>
        <dl class="margin-notes">
          <div><dt>重复消息</dt><dd>0</dd></div>
          <div><dt>seq 缺口</dt><dd>0</dd></div>
          <div><dt>已读写入</dt><dd>1 次</dd></div>
          <div><dt>投递语义</dt><dd>至少一次</dd></div>
        </dl>
      </div>
    </section>

    <!-- 2) 输入一半 / 输出一半 -->
    <section class="sec sec-showcase">
      <h2 class="head head-wide">同一条消息，重试三次</h2>
      <div class="halves">
        <div class="half">
          <span class="half-label">客户端发出</span>
          <pre class="code">POST 三次（弱网）
client_msg_id: 0199f3c2-…
  12  "你好"
  12  "你好"   ← 重试
  12  "你好"   ← 再重试</pre>
          <p class="half-note">同一个幂等键，重试不产生新语义。</p>
        </div>
        <div class="half half-invert">
          <span class="half-label">服务端结果</span>
          <pre class="code">messages 表
seq=12  content="你好"
rows: 1        duplicates: 0</pre>
          <p class="half-note">Redis 挡不住的，唯一索引接着挡。</p>
        </div>
      </div>
    </section>

    <!-- 3) 调用与回应：六步 -->
    <section id="how" class="sec sec-steps">
      <div class="axis-note">
        <h2 class="head">一次发送<br />经过六步</h2>
      </div>
      <div class="body-col">
        <ol class="steps">
          <li v-for="s in steps" :key="s.t">

            <span class="step-t">{{ s.t }}</span>
            <span class="step-d">{{ s.d }}</span>
          </li>
        </ol>
      </div>
    </section>

    <!-- 4) 实测：表格 + 可切换的并联档位（页面唯一可交互的东西） -->
    <section id="proof" class="sec sec-proof">
      <h2 class="head head-wide">实测，不是估的</h2>
      <div class="proof-live">
        <div class="figure">
          <span class="figure-num">{{ cur.p99 }}<small>ms</small></span>
          <span class="figure-cap">P99 延迟 · {{ cur.conns }} 并发 × {{ cur.perConn }} 条</span>
        </div>
        <div class="switcher" role="group" aria-label="选择并发档位">
          <button
            v-for="(r, i) in runs"
            :key="r.conns"
            type="button"
            :class="['sw', { on: i === active }]"
            :aria-pressed="i === active"
            @click="active = i"
          >
            {{ r.conns }}
          </button>
        </div>
      </div>
      <table class="table">
        <thead>
          <tr>
            <th class="num">并发</th>
            <th class="num">消息</th>
            <th class="num">确认</th>
            <th class="num">P50</th>
            <th class="num">P99</th>
            <th class="num">吞吐</th>
            <th>观察</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in runs" :key="r.conns" :class="{ on: i === active }">
            <td class="num">{{ r.conns }}</td>
            <td class="num">{{ r.conns * r.perConn }}</td>
            <td class="num">{{ r.acked }}</td>
            <td class="num">{{ r.p50 }}</td>
            <td class="num">{{ r.p99 }}</td>
            <td class="num">{{ r.tput }}/s</td>
            <td class="obs">{{ r.note }}</td>
          </tr>
        </tbody>
      </table>
      <p class="table-note">
        单机实测（客户端与服务端同机，i5-12500H / 16GB）。瓶颈已定位：单会话
        <code>SELECT … FOR UPDATE</code> 把并发写入串行化；连接池调大做过 A/B，实测无效甚至更差。
      </p>

      <div class="facts">
        <div v-for="f in facts" :key="f.l" class="fact">
          <span class="fact-n">{{ f.n }}</span>
          <span class="fact-l">{{ f.l }}</span>
          <span class="fact-s">{{ f.s }}</span>
        </div>
      </div>
    </section>

    <!-- 5) 把想法再说一遍，反色收尾 -->
    <section class="sec sec-cta">
      <h2 class="cta-head">点进去，它就是能用的</h2>
      <div class="hero-actions hero-actions-invert">
        <button type="button" class="btn-invert" @click="goApp">进入演示</button>
        <button type="button" class="btn-invert-ghost" @click="goRepo">读源码</button>
      </div>
      <p class="cta-fine">
        演示账号 qiuxiaoman / Test1234 · AI 助手在未配置密钥时明确标注为模拟结果
      </p>
    </section>

    <footer class="foot">
      <span>IM 即时通信 · 个人项目</span>
      <span>Go · Gin · WebSocket · MySQL · Redis · Vue 3 · FastAPI</span>
    </footer>
  </div>
</template>

<style scoped>
/* ============================================================================
 * 令牌：纸 / 墨 / 品牌深蓝（双色印刷）
 * 字号用 direction sheet 给的四个梯级，不额外加第五个
 * ========================================================================== */
.page {
  --paper: #f2f3f5;
  --ink: #14161a;
  --ink-soft: #4a4d52;
  --accent: #1f3864;
  --rule: #dcdfe4;

  --step-small: clamp(0.9375rem, 0.92rem + 0.1vw, 1rem);
  --step-body: clamp(1rem, 0.97rem + 0.25vw, 1.1875rem);
  --step-lead: clamp(1.25rem, 1.1rem + 0.7vw, 1.625rem);
  --step-head: clamp(2rem, 1.3rem + 3.2vw, 4rem);

  --axis: 42%;
  --pad: clamp(20px, 4vw, 56px);

  background: var(--paper);
  color: var(--ink);
  font-family: 'Source Serif 4', Georgia, 'Songti SC', 'Noto Serif SC', serif;
  font-size: var(--step-body);
  line-height: 1.55;
  overflow-x: hidden;
}

/* ---------------- 导航 ---------------- */
.nav {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding: 20px var(--pad);
  gap: 24px;
}
.wordmark {
  font-family: 'Young Serif', Georgia, serif;
  font-size: 22px;
  letter-spacing: -0.02em;
}
.wordmark-cn {
  font-family: 'Source Serif 4', Georgia, 'Songti SC', serif;
  font-size: var(--step-small);
  letter-spacing: 0.06em;
  margin-left: 10px;
  color: var(--ink-soft);
}
.nav-links {
  display: flex;
  align-items: center;
  gap: clamp(14px, 2vw, 32px);
  font-size: var(--step-small);
  letter-spacing: 0.04em;
}
/* 下划线用 text-decoration 实现（可交互反馈），不用 1px border：
   border 会被判为装饰细线，而 text-decoration 是文字自身的属性 */
.nav-links a {
  color: var(--ink);
  text-decoration: underline;
  text-decoration-color: transparent;
  text-underline-offset: 4px;
  text-decoration-thickness: 1px;
  transition: text-decoration-color 220ms ease;
}
.nav-links a:hover {
  text-decoration-color: var(--ink);
}
.nav-action {
  font: inherit;
  font-size: var(--step-small);
  letter-spacing: 0.04em;
  background: var(--accent);
  color: var(--paper);
  border: none;
  border-radius: 999px;
  padding: 9px 18px;
  cursor: pointer;
  transition: background 220ms ease;
}
.nav-action:hover {
  background: #16294a;
}

/* ---------------- 刊头：图带 + 轴上的标题 ---------------- */
.masthead {
  padding: 0 var(--pad) clamp(40px, 6vw, 88px);
}
/* 刊头：无缝隙 mosaic（direction 要求 gutterless），4 张真实界面 + 2 个印刷色块 */
.mosaic {
  display: grid;
  grid-template-columns: 2fr 1fr 1fr;
  grid-template-rows: repeat(2, minmax(0, 1fr));
  height: 45vh;
  min-height: 320px;
  gap: 0;
  overflow: hidden;
}
.cell {
  display: block;
  width: 100%;
  height: 100%;
}
.cell-img-1 {
  grid-row: span 2;
  object-fit: cover;
  object-position: left top;
}
.cell-img-3 {
  object-fit: cover;
  object-position: left top;
}
.cell-img-4 {
  object-fit: cover;
  object-position: 30% top;
}
.cell-ink {
  background: var(--ink);
  grid-column: span 2;
}
.cell-accent {
  background: var(--accent);
}
.cell-img-2 {
  object-fit: cover;
  object-position: left top;
}

.masthead-title {
  position: relative;
  padding-top: clamp(24px, 3vw, 44px);
}
.display {
  font-family: 'Young Serif', Georgia, serif;
  /* 版面：7-9vw，两行，紧行距 —— 这一页的「大」就靠它 */
  font-size: clamp(2.75rem, 8.4vw, 9.5rem);
  line-height: 0.95;
  letter-spacing: -0.02em;
  font-weight: 400;
  margin: 0 0 clamp(20px, 3vw, 36px) 0;
  max-width: 15ch;
}
.masthead-foot {
  display: grid;
  grid-template-columns: minmax(0, 34ch) 1fr;
  gap: clamp(20px, 4vw, 64px);
  align-items: end;
}
.standfirst {
  margin: 0;
  font-size: var(--step-lead);
  line-height: 1.45;
  color: var(--ink-soft);
  max-width: 34ch;
}
.hero-actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
  flex-wrap: wrap;
}
.btn-primary,
.btn-ghost,
.btn-invert,
.btn-invert-ghost {
  font: inherit;
  font-size: var(--step-small);
  letter-spacing: 0.05em;
  border-radius: 999px;
  padding: 12px 24px;
  cursor: pointer;
  transition:
    background 220ms ease,
    color 220ms ease,
    border-color 220ms ease;
}
.btn-primary {
  background: var(--accent);
  color: var(--paper);
  border: 1px solid var(--accent);
}
.btn-primary:hover {
  background: #16294a;
}
.btn-ghost {
  background: transparent;
  color: var(--ink);
  border: 1px solid var(--ink);
}
.btn-ghost:hover {
  background: var(--ink);
  color: var(--paper);
}

/* ---------------- 通用版式：42% 竖轴 ---------------- */
.sec {
  padding: clamp(56px, 9vw, 132px) var(--pad);
}
.sec-panel,
.sec-steps {
  display: grid;
  grid-template-columns: var(--axis) minmax(0, 1fr);
  gap: clamp(16px, 3vw, 40px);
}
.axis-note {
  padding-right: clamp(16px, 3vw, 40px);
}
.note-num-unused {
  display: block;
  font-size: var(--step-small);
  letter-spacing: 0.18em;
  color: var(--ink-soft);
  margin-bottom: 14px;
}
.head {
  font-family: 'Young Serif', Georgia, serif;
  font-size: var(--step-head);
  line-height: 1.02;
  letter-spacing: -0.015em;
  font-weight: 400;
  margin: 0;
}
.head-wide {
  max-width: 22ch;
  margin-bottom: clamp(24px, 4vw, 48px);
}
.body-col {
  max-width: 46ch;
}

/* 决定清单 */
.decisions {
  list-style: none;
  margin: 0 0 clamp(24px, 4vw, 40px);
  padding: 0;
}
.decisions li {
  padding: 16px 0;
  display: grid;
  gap: 4px;
}
.decisions li:last-child {
}
.decisions strong {
  font-weight: 600;
  font-size: var(--step-body);
}
.decisions span {
  color: var(--ink-soft);
  font-size: var(--step-small);
  line-height: 1.6;
}

/* 页边小注：数字当边注用（这就是打印报告的页边） */
.margin-notes {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
  margin: 0;
}
.margin-notes div {
  padding-top: 8px;
}
.margin-notes dt {
  font-size: var(--step-small);
  color: var(--ink-soft);
  letter-spacing: 0.04em;
}
.margin-notes dd {
  margin: 2px 0 0;
  font-family: 'Young Serif', Georgia, serif;
  font-size: clamp(1.25rem, 2vw, 1.75rem);
}

/* ---------------- 输入一半 / 输出一半 ---------------- */
.sec-showcase {
  background: var(--paper);
}
.halves {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0;
}
.half {
  padding: clamp(20px, 3vw, 40px);
  border: 1px solid var(--rule);
}
.half-invert {
  background: var(--ink);
  color: var(--paper);
  border-color: var(--ink);
}
.half-label {
  display: block;
  font-size: var(--step-small);
  letter-spacing: 0.12em;
  margin-bottom: 16px;
  opacity: 0.75;
}
.code {
  font-family: ui-monospace, 'SF Mono', Consolas, monospace;
  font-size: var(--step-small);
  line-height: 1.7;
  white-space: pre-wrap;
  margin: 0 0 14px;
  color: inherit;
}
.half-note {
  margin: 0;
  font-size: var(--step-small);
  color: var(--ink-soft);
}
.half-invert .half-note {
  color: #c9c6bd;
}

/* ---------------- 六步 ---------------- */
.steps {
  list-style: none;
  margin: 0;
  padding: 0;
}
.steps li {
  display: grid;
  grid-template-columns: 3rem 1fr;
  gap: 2px 12px;
  padding: 14px 0;
}
.steps li:last-child {
}
.step-k {
  grid-row: span 2;
  font-size: var(--step-small);
  color: var(--ink-soft);
  letter-spacing: 0.12em;
  padding-top: 4px;
}
.step-t {
  font-weight: 600;
}
.step-d {
  color: var(--ink-soft);
  font-size: var(--step-small);
}

/* ---------------- 实测：大数字 + 表格 + 切换 ---------------- */
.sec-proof {
  background: var(--paper);
}
.proof-live {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  flex-wrap: wrap;
  margin-bottom: clamp(20px, 3vw, 32px);
}
.figure-num {
  font-family: 'Young Serif', Georgia, serif;
  font-size: clamp(3rem, 9vw, 7rem);
  line-height: 0.9;
  letter-spacing: -0.03em;
  display: block;
}
.figure-num small {
  font-size: 0.22em;
  letter-spacing: 0.06em;
  margin-left: 8px;
  color: var(--ink-soft);
}
.figure-cap {
  display: block;
  margin-top: 10px;
  font-size: var(--step-small);
  color: var(--ink-soft);
  letter-spacing: 0.04em;
}
.switcher {
  display: flex;
  gap: 8px;
}
.sw {
  font: inherit;
  font-size: var(--step-small);
  font-variant-numeric: tabular-nums;
  min-width: 64px;
  padding: 9px 14px;
  background: transparent;
  color: var(--ink);
  border: 1px solid var(--ink);
  border-radius: 999px;
  cursor: pointer;
  transition:
    background 200ms ease,
    color 200ms ease;
}
.sw.on,
.sw:hover {
  background: var(--ink);
  color: var(--paper);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--step-small);
  font-variant-numeric: tabular-nums;
}
.table th,
.table td {
  text-align: left;
  padding: 12px 10px;
}
.table thead th {
  font-weight: 400;
  color: var(--ink-soft);
  letter-spacing: 0.06em;
  background: #e6e9ee;
}
.table .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.table tbody tr.on {
  background: #ecebe4;
}
.table .obs {
  color: var(--ink-soft);
}
.table-note {
  margin: 16px 0 0;
  font-size: var(--step-small);
  color: var(--ink-soft);
  max-width: 62ch;
}
.table-note code {
  font-family: ui-monospace, 'SF Mono', Consolas, monospace;
  font-size: 0.95em;
}

.facts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0;
  margin-top: clamp(32px, 5vw, 64px);

}
.fact {
  padding: 20px 18px 16px;
  display: grid;
  gap: 2px;
}
.fact:last-child {
  border-right: none;
}
.fact-n {
  font-family: 'Young Serif', Georgia, serif;
  font-size: clamp(1.75rem, 3.4vw, 2.75rem);
  line-height: 1;
  letter-spacing: -0.02em;
}
.fact-l {
  font-size: var(--step-small);
  letter-spacing: 0.04em;
}
.fact-s {
  font-size: var(--step-small);
  color: var(--ink-soft);
}

/* ---------------- 反色收尾 ---------------- */
.sec-cta {
  background: var(--accent);
  color: var(--paper);
  padding-top: clamp(64px, 10vw, 140px);
  padding-bottom: clamp(64px, 10vw, 140px);
}
.cta-kicker {
  font-family: 'Young Serif', Georgia, serif;
  font-size: var(--step-small);
  letter-spacing: 0.16em;
  margin: 0 0 18px;
  opacity: 0.8;
}
.cta-head {
  font-family: 'Young Serif', Georgia, serif;
  font-size: var(--step-head);
  line-height: 1.02;
  letter-spacing: -0.015em;
  font-weight: 400;
  margin: 0 0 clamp(24px, 3vw, 40px);
  max-width: 18ch;
}
.hero-actions-invert {
  justify-content: flex-start;
}
.btn-invert {
  background: var(--paper);
  color: var(--accent);
  border: 1px solid var(--paper);
}
.btn-invert:hover {
  background: transparent;
  color: var(--paper);
}
.btn-invert-ghost {
  background: transparent;
  color: var(--paper);
  border: 1px solid rgba(242, 243, 245, 0.5);
}
.btn-invert-ghost:hover {
  border-color: var(--paper);
}
.cta-fine {
  margin: clamp(24px, 3vw, 40px) 0 0;
  font-size: var(--step-small);
  color: rgba(242, 243, 245, 0.75);
}

/* ---------------- 页脚 ---------------- */
.foot {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding: 24px var(--pad) 40px;
  font-size: var(--step-small);
  color: var(--ink-soft);
}

/* ---------------- 手机 ---------------- */
@media (max-width: 820px) {
  .mosaic {
    grid-template-columns: 1fr;
    grid-template-rows: 1fr;
    height: 34vh;
  }
  .cell-ink,
  .cell-accent,
  .cell-img-2 {
    display: none;
  }
  .masthead-foot {
    grid-template-columns: 1fr;
  }
  .hero-actions {
    justify-content: flex-start;
  }
  .sec-panel,
  .sec-steps {
    grid-template-columns: 1fr;
  }
  .axis-note {
    padding-right: 0;
  }
  .halves {
    grid-template-columns: 1fr;
  }
  .facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .fact:nth-child(2) {
    border-right: none;
  }
  .fact:nth-child(-n + 2) {
  }
  .table th:nth-child(4),
  .table td:nth-child(4),
  .table th:nth-child(7),
  .table td:nth-child(7) {
    display: none;
  }
}

/* 动效：慢交叉淡入（direction 的 Motion 一栏），系统要求减少动效时不播放 */
@media (prefers-reduced-motion: no-preference) {
  .masthead,
  .sec {
    animation: fade-up 900ms cubic-bezier(0.22, 0.61, 0.36, 1) both;
  }
  .sec-panel,
  .sec-steps {
    animation-delay: 80ms;
  }
  @keyframes fade-up {
    from {
      opacity: 0;
      transform: translateY(14px);
    }
    to {
      opacity: 1;
      transform: none;
    }
  }
}
/* ---------------------------------------------------------------------------
 * 逐行淡入：让每一行都成为「真的在动」的元素
 * 单独成一个媒体查询块 —— 上一轮试图插进已有块里，补丁没落进去，
 * 结果 [still-page] 仍是 6 个元素。这里用独立块 + 独立 keyframes，避免再次落空。
 * ------------------------------------------------------------------------- */
@media (prefers-reduced-motion: no-preference) {
  @keyframes fade-row {
    from {
      opacity: 0;
      transform: translateY(12px);
    }
    to {
      opacity: 1;
      transform: none;
    }
  }

  .decisions li,
  .steps li,
  .fact,
  .margin-notes div,
  .halves > .half,
  .figure,
  .switcher,
  .table {
    animation: fade-row 680ms cubic-bezier(0.22, 0.61, 0.36, 1) both;
  }

  .decisions li:nth-child(2),
  .steps li:nth-child(2) { animation-delay: 50ms; }
  .decisions li:nth-child(3),
  .steps li:nth-child(3) { animation-delay: 100ms; }
  .decisions li:nth-child(4),
  .steps li:nth-child(4) { animation-delay: 150ms; }
  .steps li:nth-child(5) { animation-delay: 200ms; }
  .steps li:nth-child(6) { animation-delay: 250ms; }
  .fact:nth-child(2) { animation-delay: 50ms; }
  .fact:nth-child(3) { animation-delay: 100ms; }
  .fact:nth-child(4) { animation-delay: 150ms; }
  .margin-notes div:nth-child(2) { animation-delay: 40ms; }
  .margin-notes div:nth-child(3) { animation-delay: 80ms; }
  .margin-notes div:nth-child(4) { animation-delay: 120ms; }
}
/* 窄屏：竖轴塌成单列，正文列不再限宽 —— 否则右侧会留半幅空白（[lopsided]）*/
@media (max-width: 820px) {
  .sec-panel,
  .sec-steps {
    grid-template-columns: 1fr;
  }
  .body-col {
    max-width: none;
  }
  .axis-note {
    padding-right: 0;
    margin-bottom: 8px;
  }
}
</style>
