import { describe, expect, it } from 'vitest'

import {
  applyAck,
  createPending,
  markFailed,
  markSending,
  maxSeq,
  removeByClientId,
  sortMessages,
  upsertMessages,
  type ChatMessage,
} from './chatMessage'
import type { Message } from '@/api/types'

function serverMsg(seq: number, overrides: Partial<Message> = {}): Message {
  return {
    id: seq * 10,
    conversationId: 1,
    seq,
    senderId: 2,
    senderName: '对方',
    clientMsgId: `srv-${seq}`,
    contentType: 1,
    content: `msg ${seq}`,
    createdAt: 1_000_000 + seq,
    ...overrides,
  }
}

function pending(clientMsgId: string, createdAt = 2_000_000): ChatMessage {
  return createPending({
    conversationId: 1,
    senderId: 1,
    senderName: '我',
    content: 'hi',
    clientMsgId,
    createdAt,
  })
}

describe('sortMessages —— seq 是唯一的顺序权威', () => {
  it('已确认消息按 seq 升序，未确认的排在最后', () => {
    const list = [pending('c1'), toChat(serverMsg(3)), toChat(serverMsg(1)), toChat(serverMsg(2))]
    const sorted = sortMessages(list)
    expect(sorted.map((m) => m.seq)).toEqual([1, 2, 3, 0])
  })

  it('多条 pending 按本地时间排，且顺序稳定', () => {
    const a = pending('aaa', 1000)
    const b = pending('bbb', 2000)
    const sorted = sortMessages([b, a])
    expect(sorted.map((m) => m.clientMsgId)).toEqual(['aaa', 'bbb'])
  })

  it('不修改原数组', () => {
    const list = [toChat(serverMsg(2)), toChat(serverMsg(1))]
    const before = list.map((m) => m.seq)
    sortMessages(list)
    expect(list.map((m) => m.seq)).toEqual(before)
  })
})

describe('upsertMessages —— 去重（服务端"至少一次"投递的必需品）', () => {
  it('同一 seq 重复到达只保留一条', () => {
    const once = upsertMessages([], [toChat(serverMsg(1))])
    const twice = upsertMessages(once, [toChat(serverMsg(1))])
    expect(twice).toHaveLength(1)
  })

  it('重复推送时用后到的内容覆盖（同一条消息的最新版本）', () => {
    const once = upsertMessages([], [toChat(serverMsg(1, { content: '旧' }))])
    const twice = upsertMessages(once, [toChat(serverMsg(1, { content: '新' }))])
    expect(twice).toHaveLength(1)
    expect((twice[0] as ChatMessage).content).toBe('新')
  })

  it('按 seq 合并成正确的顺序（补拉回来的消息插到中间）', () => {
    let list = upsertMessages([], [toChat(serverMsg(1)), toChat(serverMsg(4))])
    list = upsertMessages(list, [toChat(serverMsg(2)), toChat(serverMsg(3))])
    expect(list.map((m) => m.seq)).toEqual([1, 2, 3, 4])
  })

  it('本地「发送中」的消息被服务端版本取代（不会出现两条）', () => {
    const p = pending('cid-1')
    let list = upsertMessages([], [p])
    expect(list).toHaveLength(1)
    expect((list[0] as ChatMessage).status).toBe('sending')

    // 补拉时服务端把这条也返回了（带同一个 clientMsgId 和真实 seq）
    const fromServer = toChat(serverMsg(5, { clientMsgId: 'cid-1', senderId: 1, content: 'hi' }))
    list = upsertMessages(list, [fromServer])
    expect(list).toHaveLength(1)
    expect((list[0] as ChatMessage).seq).toBe(5)
    expect((list[0] as ChatMessage).status).toBe('sent')
  })

  it('空输入直接返回原列表引用（性能优化且不破坏响应式）', () => {
    const list = [toChat(serverMsg(1))]
    expect(upsertMessages(list, [])).toBe(list)
  })
})

describe('applyAck —— 三态流转：sending → sent', () => {
  it('按 clientMsgId 定位，补上 seq 并变已发送', () => {
    const list = upsertMessages([], [pending('cid-9')])
    const after = applyAck(list, 'cid-9', 7, 1_500_000)
    expect(after).toHaveLength(1)
    const m = after[0] as ChatMessage
    expect(m.seq).toBe(7)
    expect(m.status).toBe('sent')
    expect(m.createdAt).toBe(1_500_000)
  })

  it('ack 后消息按 seq 落到正确位置，而不是因为"后发"就排最后', () => {
    // 真实场景：对方先发了两条（seq 1、seq 3 —— 我的消息占用了 seq 2 但还没 ack），
    // 所以本地列表是 [1, 3] + 一条 pending。ack 回来后应当插到 1 和 3 之间。
    let list = upsertMessages([], [toChat(serverMsg(1)), toChat(serverMsg(3))])
    list = upsertMessages(list, [pending('cid-x')])
    expect(list[list.length - 1]?.clientMsgId).toBe('cid-x')

    list = applyAck(list, 'cid-x', 2, 1_000_002)
    expect(list.map((m) => m.seq)).toEqual([1, 2, 3])
  })

  it('ack 的 seq 单调递增时，插入位置永远正确（1 和 3 之间插 2）', () => {
    let list = upsertMessages([], [toChat(serverMsg(1)), toChat(serverMsg(3))])
    list = upsertMessages(list, [pending('mid')])
    list = applyAck(list, 'mid', 2, 1_000_002)
    expect(list.map((m) => m.seq)).toEqual([1, 2, 3])
  })

  it('找不到 clientMsgId 时原样返回（幂等，不报错）', () => {
    const list = [toChat(serverMsg(1))]
    expect(applyAck(list, '不存在', 9, 0)).toEqual(list)
  })
})

describe('markFailed / markSending —— 重试的基础', () => {
  it('失败后可以再改回发送中（重试）', () => {
    let list = upsertMessages([], [pending('r1')])
    list = markFailed(list, 'r1')
    expect((list[0] as ChatMessage).status).toBe('failed')
    list = markSending(list, 'r1')
    expect((list[0] as ChatMessage).status).toBe('sending')
  })

  it('标记失败不会改变 clientMsgId（重试必须复用同一个幂等键）', () => {
    const list = upsertMessages([], [pending('reuse-me')])
    const failed = markFailed(list, 'reuse-me')
    expect((failed[0] as ChatMessage).clientMsgId).toBe('reuse-me')
  })
})

describe('maxSeq —— 断线补拉的游标', () => {
  it('返回最大的已确认 seq，忽略 pending', () => {
    const list = upsertMessages([], [
      toChat(serverMsg(3)),
      toChat(serverMsg(11)),
      toChat(serverMsg(7)),
      pending('p1'),
    ])
    expect(maxSeq(list)).toBe(11)
  })

  it('空列表返回 0（表示从头拉）', () => {
    expect(maxSeq([])).toBe(0)
    expect(maxSeq([pending('p')])).toBe(0)
  })
})

describe('removeByClientId', () => {
  it('按 clientMsgId 删除', () => {
    const list = upsertMessages([], [toChat(serverMsg(1)), pending('del-1')])
    const after = removeByClientId(list, 'del-1')
    expect(after).toHaveLength(1)
  })
})

// 小工具：Message → ChatMessage
function toChat(m: Message): ChatMessage {
  return {
    clientMsgId: m.clientMsgId,
    id: m.id,
    conversationId: m.conversationId,
    seq: m.seq,
    senderId: m.senderId,
    senderName: m.senderName,
    contentType: m.contentType,
    content: m.content,
    createdAt: m.createdAt,
    status: 'sent',
  }
}
