#!/usr/bin/env python3
"""Static contracts for the ECS compact coding-agent Harness."""

from __future__ import annotations

import json
import importlib.util
import pathlib
import stat
import subprocess
import sys
import tomllib


ROOT = pathlib.Path(__file__).resolve().parents[1]
ADAPTERS = (
    ROOT / ".codex/hooks/pre-tool-use.py",
    ROOT / ".codex/hooks/session-start.py",
)


def fail(message: str) -> None:
    print(message, file=sys.stderr)
    raise SystemExit(1)


def main() -> int:
    result = subprocess.run(
        [sys.executable, "scripts/render-codex-hooks.py", "--check"], cwd=ROOT
    )
    if result.returncode:
        return result.returncode

    hooks = json.loads((ROOT / ".codex/hooks.json").read_text())
    if hooks.get("hooks", {}).keys() != {"SessionStart", "PreToolUse"}:
        fail("compact Harness must register exactly SessionStart and PreToolUse")

    for adapter in ADAPTERS:
        if adapter.stat().st_size > 3 * 1024:
            fail(f"Codex adapter exceeds 3 KiB: {adapter.relative_to(ROOT)}")

    claude_entry = (ROOT / "CLAUDE.md").read_text()
    if not claude_entry.startswith("@AGENTS.md\n") or len(claude_entry.encode()) > 3 * 1024:
        fail("CLAUDE.md must remain a thin @AGENTS.md adapter below 3 KiB")

    for hook in (ROOT / ".githooks/pre-commit", ROOT / ".githooks/pre-push"):
        if not hook.stat().st_mode & stat.S_IXUSR:
            fail(f"Git hook is not executable: {hook.relative_to(ROOT)}")

    codex_config = tomllib.loads((ROOT / ".codex/config.toml").read_text())
    if set(codex_config) != {"project_doc_max_bytes"}:
        fail("project .codex/config.toml may only set project_doc_max_bytes")
    agents_size = (ROOT / "AGENTS.md").stat().st_size
    if codex_config["project_doc_max_bytes"] != 8 * 1024:
        fail("project_doc_max_bytes must remain fixed at 8 KiB")
    agents_skills = ROOT / ".agents/skills"
    if agents_skills.is_symlink() and agents_skills.resolve() == (ROOT / "skills.d").resolve():
        fail("skills.d is ECS Runtime state and must not be exposed as coding-agent skills")

    route_source = ROOT / "scripts/harness/route_tree.py"
    route_spec = importlib.util.spec_from_file_location("ecs_verify_route_tree", route_source)
    if route_spec is None or route_spec.loader is None:
        fail("cannot load recursive route tree verifier")
    route_module = importlib.util.module_from_spec(route_spec)
    sys.modules[route_spec.name] = route_module
    route_spec.loader.exec_module(route_module)
    route_raw = (ROOT / "scripts/harness/context-routes.json").read_bytes()
    _, route_nodes, budgets = route_module.parse(route_raw)
    session_result = subprocess.run(
        [sys.executable, ".codex/hooks/session-start.py"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    session_document = json.loads(session_result.stdout)
    session_context = session_document["hookSpecificOutput"]["additionalContext"]
    session_size = len(session_context.encode())
    if agents_size > budgets["agents_max_bytes"]:
        fail(f"AGENTS.md exceeds recursive tree budget: {agents_size} bytes")
    if session_size > budgets["session_context_max_bytes"]:
        fail(f"SessionStart context exceeds recursive tree budget: {session_size} bytes")
    if agents_size + session_size > budgets["startup_total_bytes"]:
        fail(
            "project startup context exceeds 8 KiB: "
            f"{agents_size + session_size} bytes"
        )
    for node in route_nodes.values():
        for context in node.contexts:
            context_path = ROOT / context.split("#", 1)[0]
            if not context_path.is_file():
                fail(f"recursive context target is missing: {context}")
    installer = (ROOT / "scripts/install-harness.sh").read_text()
    if 'git show "HEAD:${relative}"' not in installer:
        fail("Harness installer must source Git hooks from HEAD")
    if "accepts no bootstrap override" not in installer:
        fail("Harness installer must reject bootstrap overrides")
    if "git diff --cached --quiet" not in installer:
        fail("Harness installer must reject uncommitted trust-root updates")
    pre_commit = (ROOT / ".githooks/pre-commit").read_text()
    if "review" in pre_commit or "receipt" in pre_commit:
        fail("pre-commit must not require a local submission review")
    if "git diff --cached --check" not in pre_commit:
        fail("pre-commit must retain the staged diff check")
    if "scripts/verify-harness.py" in pre_commit or "scripts/verify-agents-md.py" in pre_commit:
        fail("pre-commit must not execute worktree candidate Python")
    pre_push = (ROOT / ".githooks/pre-push").read_text()
    if 'subprocess.Popen(["make", "test"]' not in pre_push:
        fail("pre-push must use the canonical make test gate")
    if '"${local_sha}" != "${head}"' not in pre_push:
        fail("pre-push must bind its gate to the actual pushed HEAD")
    if "git diff --cached --quiet" not in pre_push:
        fail("pre-push must reject a dirty index")
    if "PUSH_CHECK_TIMEOUT_SECONDS = 60" not in pre_push:
        fail("pre-push must cap make test at 60 seconds")
    if (
        '"start_new_session": True' not in pre_push
        or "os.killpg" not in pre_push
        or '"taskkill"' not in pre_push
    ):
        fail("pre-push timeout must terminate the full make test process group")
    if '${state}/installed' in installer:
        fail("Harness installer must not create an unused installed-state stamp")
    core = (ROOT / "scripts/harness/core.py").read_text()
    if "trusted-review-staged.py" in core or "staged_review_check" in core:
        fail("PreToolUse must not require a local submission review")

    print("ECS compact Harness structure verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
