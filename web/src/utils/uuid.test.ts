import { describe, expect, it } from 'vitest'

import { extractTimestamp, isUuidv7, uuidv7 } from './uuid'

describe('uuidv7 —— 格式与语义', () => {
  it('符合 UUID 的标准格式（8-4-4-4-12）', () => {
    const id = uuidv7()
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/)
    expect(id).toHaveLength(36)
  })

  it('版本位是 7（不是 4）', () => {
    for (let i = 0; i < 50; i++) {
      const id = uuidv7()
      // 第 13 个十六进制字符是版本号
      expect(id.replace(/-/g, '')[12]).toBe('7')
    }
  })

  it('变体位是 0b10（第 17 个十六进制字符属于 8/9/a/b）', () => {
    for (let i = 0; i < 50; i++) {
      const id = uuidv7()
      expect(['8', '9', 'a', 'b']).toContain(id.replace(/-/g, '')[16])
    }
  })

  it('能反解出原始毫秒时间戳', () => {
    const now = 1_791_197_597_369
    const id = uuidv7(now)
    expect(extractTimestamp(id)).toBe(now)
  })

  it('时间戳精度到毫秒（前后相邻毫秒都能还原）', () => {
    const base = 1_700_000_000_000
    for (const ms of [base, base + 1, base + 999, base + 123_456]) {
      expect(extractTimestamp(uuidv7(ms))).toBe(ms)
    }
  })

  it('时间有序：更晚的时间戳生成的字符串字典序也更大（B+ 树友好）', () => {
    const a = uuidv7(1_700_000_000_000)
    const b = uuidv7(1_700_000_000_001)
    const c = uuidv7(1_800_000_000_000)
    expect(a < b).toBe(true)
    expect(b < c).toBe(true)
  })

  it('大量生成不重复（含同一毫秒内的并发场景）', () => {
    const fixed = 1_791_197_597_369
    const set = new Set<string>()
    for (let i = 0; i < 5000; i++) set.add(uuidv7(fixed))
    expect(set.size).toBe(5000)
  })

  it('isUuidv7 能区分 v7 与非法输入', () => {
    expect(isUuidv7(uuidv7())).toBe(true)
    expect(isUuidv7('')).toBe(false)
    expect(isUuidv7('not-a-uuid')).toBe(false)
    // v4 的版本位是 4，不应当通过
    expect(isUuidv7('9f1b6c2e-7a3d-4b1e-8c4f-2d5a6b7c8d9e')).toBe(false)
    // 变体位不合法（第 17 位是 c，不属于 8/9/a/b）
    expect(isUuidv7('9f1b6c2e-7a3d-7b1e-c4f0-2d5a6b7c8d9e')).toBe(false)
  })

  it('extractTimestamp 对非 v7 返回 null', () => {
    expect(extractTimestamp('9f1b6c2e-7a3d-4b1e-8c4f-2d5a6b7c8d9e')).toBeNull()
    expect(extractTimestamp('whatever')).toBeNull()
  })
})
