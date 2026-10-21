/**
 * UUIDv7 生成器。
 *
 * 为什么用 v7 而不是 v4（面试要能讲）：
 *
 *   v4: 128 位全随机        → 做数据库主键时，插入位置随机，
 *                              B+ 树频繁页分裂，写放大明显
 *   v7: 前 48 位是毫秒时间戳 → 时间有序，插入近似「追加」，
 *                              索引友好；后 74 位仍是随机的，不冲突
 *
 * 它同时是**幂等键**：客户端在「用户点发送」时生成一次，
 * 重试时必须复用同一个值，否则服务端的幂等就失效了。
 *
 * 位布局（RFC 9562）：
 *   48 bit  unix_ts_ms
 *    4 bit  ver = 7
 *   12 bit  rand_a
 *    2 bit  var = 0b10
 *   62 bit  rand_b
 */

/** 优先用平台 CSPRNG，退化到 Math.random（只影响不可预测性，不影响唯一性）。 */
function randomBytes(n: number): Uint8Array {
  const out = new Uint8Array(n)
  const c = (globalThis as { crypto?: Crypto }).crypto
  if (c && typeof c.getRandomValues === 'function') {
    c.getRandomValues(out)
    return out
  }
  for (let i = 0; i < n; i++) out[i] = Math.floor(Math.random() * 256)
  return out
}

const HEX: string[] = []
for (let i = 0; i < 256; i++) HEX.push((i + 0x100).toString(16).slice(1))

function format(bytes: Uint8Array): string {
  // 8-4-4-4-12
  const h: string[] = []
  for (let i = 0; i < 16; i++) h.push(HEX[bytes[i] as number] as string)
  return (
    h.slice(0, 4).join('') +
    '-' +
    h.slice(4, 6).join('') +
    '-' +
    h.slice(6, 8).join('') +
    '-' +
    h.slice(8, 10).join('') +
    '-' +
    h.slice(10, 16).join('')
  )
}

/**
 * 生成一个 UUIDv7。
 * @param nowMs 毫秒时间戳，注入以便测试
 */
export function uuidv7(nowMs: number = Date.now()): string {
  const bytes = new Uint8Array(16)

  // --- 48 位时间戳，大端写入前 6 字节 ---
  let t = Math.floor(nowMs)
  for (let i = 5; i >= 0; i--) {
    bytes[i] = t & 0xff
    // 用除法而不是 >>>，避免超过 32 位时精度问题
    t = Math.floor(t / 256)
  }

  // --- 随机部分 ---
  const rnd = randomBytes(10)

  // byte 6: 高 4 位 = 版本号 7
  bytes[6] = 0x70 | ((rnd[0] as number) & 0x0f)
  bytes[7] = rnd[1] as number
  // byte 8: 高 2 位 = 变体 0b10
  bytes[8] = 0x80 | ((rnd[2] as number) & 0x3f)
  for (let i = 0; i < 7; i++) {
    bytes[9 + i] = rnd[3 + i] as number
  }

  return format(bytes)
}

/** 从 UUIDv7 里解析出毫秒时间戳（非 v7 返回 null）。 */
export function extractTimestamp(uuid: string): number | null {
  const hex = uuid.replace(/-/g, '')
  if (hex.length !== 32) return null
  if (hex[12] !== '7') return null // 版本位
  let ms = 0
  for (let i = 0; i < 12; i++) {
    ms = ms * 16 + parseInt(hex[i] as string, 16)
  }
  return ms
}

/** 校验格式（版本 7 + 变体位正确）。 */
export function isUuidv7(uuid: string): boolean {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(uuid)) {
    return false
  }
  return true
}
