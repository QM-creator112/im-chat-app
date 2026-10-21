/**
 * 指数退避 + 随机抖动。
 *
 * 为什么必须有抖动（面试高频追问）：
 *   服务端重启时，所有客户端会在同一瞬间断开。如果退避时间完全一样，
 *   它们会在 1 秒、2 秒、4 秒……这些时刻**同时**涌上来重连，
 *   把刚起来的服务端再打挂一次（惊群 / thundering herd）。
 *   叠加 ±20% 的随机抖动后，重连请求被摊平在一个时间窗口里。
 *
 * 参数取值理由：
 *   - 起始 1 秒：用户几乎无感，又能避开瞬时故障
 *   - 上限 30 秒：再长用户会觉得「卡死了」
 *   - 倍数 2：经典指数退避，1/2/4/8/16/30
 */

export const BACKOFF_BASE_MS = 1000
export const BACKOFF_MAX_MS = 30_000
/** 抖动比例：±20% */
export const JITTER_RATIO = 0.2

/** 第 attempt 次（从 1 开始）重连的**基准**等待时间，不含抖动。 */
export function baseBackoff(attempt: number): number {
  const n = Math.max(1, Math.floor(attempt))
  // 2^(n-1) 在 n 较大时会溢出成 Infinity，用上限截断即可
  const raw = BACKOFF_BASE_MS * Math.pow(2, n - 1)
  return Math.min(raw, BACKOFF_MAX_MS)
}

/**
 * 给一个基准值叠加 ±JITTER_RATIO 的抖动。
 * @param rand 随机源，注入以便测试（默认 Math.random）
 */
export function withJitter(ms: number, rand: () => number = Math.random): number {
  const delta = ms * JITTER_RATIO
  // rand() ∈ [0,1) → 结果 ∈ [ms-delta, ms+delta)
  return Math.round(ms - delta + rand() * delta * 2)
}

/** 第 attempt 次重连的最终等待时间（含抖动）。 */
export function computeBackoff(attempt: number, rand: () => number = Math.random): number {
  return withJitter(baseBackoff(attempt), rand)
}

/** 便于日志/调试：把前几次的退避时间列出来。 */
export function previewBackoff(count = 6): number[] {
  const out: number[] = []
  for (let i = 1; i <= count; i++) out.push(baseBackoff(i))
  return out
}
