#!/usr/bin/env python3
"""Thin Codex SessionStart adapter with a deliberately small context index."""

from __future__ import annotations

import json
import os


context = """ECS 项目治理入口：
- 工程规则唯一真源是仓库根 AGENTS.md；按其中路径读取需要的细节。
- skills.d/ 是 ECS Runtime 能力，不是编码 Agent skill，不要迁入 .agents/skills/。
- .codex/hooks.json 由 scripts/render-codex-hooks.py 生成，禁止手改。
- 本地 Git hooks 与 CI 是最终机器兜底；Codex hook 只提供提前阻断。"""
if os.environ.get("ECS_HARNESS_OUTPUT_CANARY") == "1":
    context += "\n" + "\n".join(
        f"ECS_HARNESS_OUTPUT_CANARY_{index:04d}: verify spill preservation"
        for index in range(1200)
    )

print(
    json.dumps(
        {
            "hookSpecificOutput": {
                "hookEventName": "SessionStart",
                "additionalContext": context,
            }
        },
        ensure_ascii=False,
    )
)
