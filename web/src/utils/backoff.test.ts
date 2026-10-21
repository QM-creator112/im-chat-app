import { describe, expect, it } from 'vitest'

import {
  BACKOFF_BASE_MS,
  BACKOFF_MAX_MS,
  JITTER_RATIO,
  baseBackoff,
  computeBackoff,
  previewBackoff,
  withJitter,
} from './backoff'

describe('baseBackoff —— 指数退避的基准值', () => {
  it('应该是 1/2/4/8/16 秒', () => {
    expect(baseBackoff(1)).toBe(1000)
    expect(baseBackoff(2)).toBe(2000)
    expect(baseBackoff(3)).toBe(4000)
    expect(baseBackoff(4)).toBe(8000)
    expect(baseBackoff(5)).toBe(16000)
  })

  it('从第 6 次起被 30 秒上限截断', () => {
    expect(baseBackoff(6)).toBe(30_000)
    expect(baseBackoff(10)).toBe(30_000)
    // 即使 attempts 极大也不能变成 Infinity
    expect(baseBackoff(2000)).toBe(BACKOFF_MAX_MS)
  })

  it('非法 attempt（0 / 负数 / 小数）按 1 处理', () => {
    expect(baseBackoff(0)).toBe(BACKOFF_BASE_MS)
    expect(baseBackoff(-5)).toBe(BACKOFF_BASE_MS)
    expect(baseBackoff(2.9)).toBe(2000)
  })

  it('previewBackoff 序列符合预期', () => {
    expect(previewBackoff(6)).toEqual([1000, 2000, 4000, 8000, 16000, 30000])
  })
})

describe('withJitter —— 随机抖动', () => {
  it('rand=0 时取下界（-20%）', () => {
    expect(withJitter(1000, () => 0)).toBe(1000 - 1000 * JITTER_RATIO)
  })

  it('rand≈1 时取上界（+20%）', () => {
    expect(withJitter(1000, () => 0.999999)).toBe(1200)
  })

  it('rand=0.5 时抖动抵消，等于原值', () => {
    expect(withJitter(1000, () => 0.5)).toBe(1000)
    expect(withJitter(30_000, () => 0.5)).toBe(30_000)
  })

  it('永远不会超出 ±20% 的范围（跑 1000 次真实随机）', () => {
    for (let i = 0; i < 1000; i++) {
      const v = withJitter(4000)
      expect(v).toBeGreaterThanOrEqual(4000 * (1 - JITTER_RATIO) - 1)
      expect(v).toBeLessThanOrEqual(4000 * (1 + JITTER_RATIO) + 1)
    }
  })
})

describe('computeBackoff —— 最终等待时间', () => {
  it('随机抖动叠加在基准值上', () => {
    expect(computeBackoff(1, () => 0.5)).toBe(1000)
    expect(computeBackoff(3, () => 0.5)).toBe(4000)
  })

  it('抖动确实让不同的调用产生不同的值（防惊群）', () => {
    // 用真实 Math.random，统计去重后的取值个数
    const set = new Set<number>()
    for (let i = 0; i < 50; i++) set.add(computeBackoff(5))
    // 50 次至少应该有多样性；如果恒等说明抖动没生效
    expect(set.size).toBeGreaterThan(5)
  })

  it('抖动后的值仍落在合理区间内', () => {
    for (let attempt = 1; attempt <= 10; attempt++) {
      const base = baseBackoff(attempt)
      const v = computeBackoff(attempt)
      expect(v).toBeGreaterThanOrEqual(base * (1 - JITTER_RATIO) - 1)
      expect(v).toBeLessThanOrEqual(base * (1 + JITTER_RATIO) + 1)
    }
  })
})
