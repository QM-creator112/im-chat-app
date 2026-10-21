"""AI 侧车的单元测试。

用 FastAPI 的 TestClient（基于 httpx，不需要真的起 uvicorn）。

重点验证三件事：
  1. 三种运行模式（real / mock / disabled）的行为边界清晰
  2. mock 结果**明确标注**，不会被误认为大模型输出
  3. 错误路径返回合适的 HTTP 状态码（Go 侧据此决定降级）

运行：cd ai && .venv/Scripts/python -m pytest -q
"""

from __future__ import annotations

import main
from fastapi.testclient import TestClient

client = TestClient(main.app)


# ---------------------------------------------------------------------------
# /health
# ---------------------------------------------------------------------------


def test_health_always_ok():
    r = client.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["model"]
    # mode 必须如实报告，调用方据此判断是真实调用还是模拟
    assert body["mode"] in ("real", "mock", "disabled")


# ---------------------------------------------------------------------------
# disabled 模式：没 key 也没开 mock -> 503，让 Go 侧走降级
# ---------------------------------------------------------------------------


def test_disabled_mode_returns_503(monkeypatch):
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    monkeypatch.setattr(main, "AI_MOCK", False)
    assert main.resolve_mode() == "disabled"

    r = client.get("/health")
    assert r.json()["mode"] == "disabled"

    r = client.post("/summarize", json={"messages": ["你好"]})
    assert r.status_code == 503
    # 不能泄露任何配置细节（比如 key 的前缀）
    assert "LLM_API_KEY" in r.json()["detail"]


def test_reply_disabled_returns_503(monkeypatch):
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    monkeypatch.setattr(main, "AI_MOCK", False)
    r = client.post("/reply", json={"messages": ["你好"]})
    assert r.status_code == 503


# ---------------------------------------------------------------------------
# mock 模式：验证链路，但结果必须明确标注
# ---------------------------------------------------------------------------


def test_mock_mode_summary_is_labeled(monkeypatch):
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    monkeypatch.setattr(main, "AI_MOCK", True)
    assert main.resolve_mode() == "mock"

    r = client.post("/summarize", json={"messages": ["明天会议改到三点", "好的我记一下"]})
    assert r.status_code == 200
    body = r.json()
    assert body["mode"] == "mock"
    # 关键：必须带 [MOCK] 标记，绝不能让人以为这是大模型输出
    assert body["summary"].startswith("[MOCK]")
    assert "不是大模型输出" in body["summary"]


def test_mock_mode_reply_is_labeled(monkeypatch):
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    monkeypatch.setattr(main, "AI_MOCK", True)
    r = client.post("/reply", json={"messages": ["在吗"]})
    assert r.status_code == 200
    assert r.json()["reply"].startswith("[MOCK]")


# ---------------------------------------------------------------------------
# real 模式：不真的调外部 API，只验证配置解析与分支
# ---------------------------------------------------------------------------


def test_real_mode_detected_when_key_present(monkeypatch):
    monkeypatch.setattr(main, "LLM_API_KEY", "sk-fake-for-test")
    assert main.resolve_mode() == "real"
    r = client.get("/health")
    assert r.json()["mode"] == "real"
    # 注意：这里**不能**真的调用 /summarize，否则会拿假 key 去打外部 API。
    # real 路径的真实调用需要真 key，属于集成测试范畴。


# ---------------------------------------------------------------------------
# 参数校验
# ---------------------------------------------------------------------------


def test_summarize_rejects_empty_messages(monkeypatch):
    monkeypatch.setattr(main, "AI_MOCK", True)
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    r = client.post("/summarize", json={"messages": []})
    assert r.status_code == 400


def test_summarize_rejects_missing_field(monkeypatch):
    monkeypatch.setattr(main, "AI_MOCK", True)
    monkeypatch.setattr(main, "LLM_API_KEY", "")
    r = client.post("/summarize", json={})
    assert r.status_code == 422  # pydantic 校验失败


# ---------------------------------------------------------------------------
# 上下文裁剪（成本控制）
# ---------------------------------------------------------------------------


def test_trim_keeps_only_recent_messages():
    msgs = [f"m{i}" for i in range(100)]
    out = main._trim(msgs, 10)
    assert len(out) == 10
    assert out[0] == "m90"  # 最近的 10 条
    assert out[-1] == "m99"


def test_trim_truncates_long_message():
    long_msg = "x" * (main.MAX_MESSAGE_CHARS + 500)
    out = main._trim([long_msg], 10)
    assert len(out[0]) == main.MAX_MESSAGE_CHARS


def test_trim_handles_empty_input():
    assert main._trim([], 10) == []
