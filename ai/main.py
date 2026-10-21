"""IM AI 侧车服务（FastAPI）。

为什么 AI 是独立服务而不是塞进 Go 后端（面试会问，三条理由）：
  1. 生态：大模型的官方 SDK 与示例在 Python 生态里最完整。
  2. 故障隔离：大模型可能几秒甚至超时。塞进 Go 会占用主服务的 goroutine
     和连接池；独立服务可以单独设超时与熔断。
  3. 可替换：换模型、加 RAG、加 Agent 都只改 Python，Go 侧只看到一个 HTTP 接口。

运行模式（由环境变量决定，`/health` 会如实报告当前模式）：
  - real     : 配了 LLM_API_KEY，真实调用大模型
  - mock     : 没配 key 但 AI_MOCK=1，返回**明确标注为模拟**的结果，
               用于在没有 key 的情况下验证「Go → 侧车 → Go → WS 推送」整条链路
  - disabled : 没配 key 也没开 mock，接口返回 503，由 Go 侧降级为系统提示

⚠️ mock 模式的价值只是「验证链路通」，**不是大模型能力**。
   任何对外说明都必须标明这是模拟结果。
"""

from __future__ import annotations

import os
from typing import List

import httpx
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------

LLM_API_BASE = os.getenv("LLM_API_BASE", "https://api.deepseek.com").rstrip("/")
LLM_API_KEY = os.getenv("LLM_API_KEY", "").strip()
LLM_MODEL = os.getenv("LLM_MODEL", "deepseek-chat")
# 显式超时：不设的话这个请求可能挂死，把调用方一起拖住
LLM_TIMEOUT = float(os.getenv("AI_TIMEOUT_SECONDS", "20"))
AI_MOCK = os.getenv("AI_MOCK", "").strip().lower() in ("1", "true", "yes", "on")

# 传给模型的上下文上限，防止 token 爆炸与超长请求
MAX_CONTEXT_MESSAGES = 50
MAX_MESSAGE_CHARS = 500


def resolve_mode() -> str:
    """当前运行模式。"""
    if LLM_API_KEY:
        return "real"
    if AI_MOCK:
        return "mock"
    return "disabled"


app = FastAPI(
    title="IM AI Sidecar",
    version="0.6.0",
    description="IM 项目的 AI 侧车：会话总结与回复建议。",
)


# ---------------------------------------------------------------------------
# 请求模型
# ---------------------------------------------------------------------------


class SummarizeRequest(BaseModel):
    messages: List[str] = Field(..., description="要总结的聊天消息（按时间顺序）")


class ReplyRequest(BaseModel):
    messages: List[str] = Field(..., description="聊天上下文（按时间顺序）")


# ---------------------------------------------------------------------------
# 工具
# ---------------------------------------------------------------------------


def _trim(messages: List[str], limit: int) -> List[str]:
    """只保留最近 limit 条，并截断单条长度。

    为什么要截断：一条 8000 字的消息会瞬间吃掉大量 token，
    而总结/回复并不需要全文。这是最省事也最有效的成本控制。
    """
    recent = messages[-limit:]
    return [m[:MAX_MESSAGE_CHARS] for m in recent]


def _mock_text(kind: str, messages: List[str]) -> str:
    """生成**明确标注为模拟**的返回，用于无 key 时验证链路。

    刻意不假装像真模型：开头就是 [MOCK]，让任何人一眼看出这不是大模型输出。
    """
    joined = " / ".join(m.strip()[:24] for m in _trim(messages, 3) if m.strip())
    if kind == "summary":
        return (
            f"[MOCK] 未配置 LLM_API_KEY，这是本地模拟结果，不是大模型输出。"
            f"共 {len(messages)} 条消息，最近内容是：{joined}"
        )
    return (
        f"[MOCK] 未配置 LLM_API_KEY，这是本地模拟回复，不是大模型输出。"
        f"（基于最近 {len(_trim(messages, 20))} 条上下文生成）"
    )


async def call_llm(system_prompt: str, user_prompt: str) -> str:
    """调用大模型。

    三个必须做对的地方：
      1. **显式超时** —— 默认不超时会让请求挂死
      2. **错误不泄露敏感信息** —— 只返回状态码，不把完整响应体透出去
         （响应体里可能带账号信息、额度信息）
      3. 返回纯文本，不含 markdown 包装，调用方直接当消息内容用
    """
    mode = resolve_mode()
    if mode == "disabled":
        # 503：让 Go 侧走"降级为系统提示消息"的路径
        raise HTTPException(status_code=503, detail="未配置 LLM_API_KEY（也未开启 AI_MOCK）")

    payload = {
        "model": LLM_MODEL,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
        "temperature": 0.7,
        "max_tokens": 512,
    }
    headers = {
        "Authorization": f"Bearer {LLM_API_KEY}",
        "Content-Type": "application/json",
    }

    try:
        async with httpx.AsyncClient(timeout=LLM_TIMEOUT) as client:
            resp = await client.post(
                f"{LLM_API_BASE}/v1/chat/completions",
                json=payload,
                headers=headers,
            )
    except httpx.TimeoutException:
        raise HTTPException(status_code=504, detail="大模型请求超时")
    except httpx.HTTPError as e:
        # 只暴露异常类型，不把完整 URL / 请求头带出去
        raise HTTPException(status_code=502, detail=f"调用大模型失败: {type(e).__name__}")

    if resp.status_code != 200:
        # ⚠️ 不要把 resp.text 透出去
        raise HTTPException(status_code=502, detail=f"大模型返回 {resp.status_code}")

    try:
        data = resp.json()
        content = data["choices"][0]["message"]["content"]
    except (KeyError, IndexError, ValueError):
        raise HTTPException(status_code=502, detail="大模型返回格式不符合预期")

    if not isinstance(content, str) or content.strip() == "":
        raise HTTPException(status_code=502, detail="大模型返回了空内容")
    return content.strip()


# ---------------------------------------------------------------------------
# 路由
# ---------------------------------------------------------------------------


@app.get("/health")
def health() -> dict:
    """健康检查。

    如实报告 mode —— 调用方（和验收脚本）据此判断当前是真实调用还是模拟。
    """
    return {
        "status": "ok",
        "model": LLM_MODEL,
        "mode": resolve_mode(),
        "llm_api_base": LLM_API_BASE,
        "timeout_seconds": LLM_TIMEOUT,
    }


@app.post("/summarize")
async def summarize(req: SummarizeRequest) -> dict:
    """把一段聊天记录总结成几句话。"""
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages 不能为空")

    msgs = _trim(req.messages, MAX_CONTEXT_MESSAGES)
    if resolve_mode() == "mock":
        return {"summary": _mock_text("summary", msgs), "mode": "mock"}

    joined = "\n".join(f"- {m}" for m in msgs)
    text = await call_llm(
        system_prompt="你是一个聊天记录总结助手。用中文输出，最多 3 句话，不要寒暄。",
        user_prompt=f"请总结下面这段聊天记录的主要内容：\n{joined}",
    )
    return {"summary": text, "mode": "real"}


@app.post("/reply")
async def reply(req: ReplyRequest) -> dict:
    """根据上下文生成一条简短的回复建议。"""
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages 不能为空")

    msgs = _trim(req.messages, 20)
    if resolve_mode() == "mock":
        return {"reply": _mock_text("reply", msgs), "mode": "mock"}

    joined = "\n".join(f"- {m}" for m in msgs)
    text = await call_llm(
        system_prompt="你是一个聊天助手。根据上下文生成一条简短、自然的回复，只输出回复内容本身。",
        user_prompt=f"聊天上下文：\n{joined}",
    )
    return {"reply": text, "mode": "real"}
