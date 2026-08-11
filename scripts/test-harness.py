#!/usr/bin/env python3
"""Known-positive regression cases for the ECS compact Harness."""

from __future__ import annotations

import importlib.util
import io
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[1]
CORE = ROOT / "scripts/harness/core.py"
sys.dont_write_bytecode = True


def load_core():
    spec = importlib.util.spec_from_file_location("ecs_harness_test_core", CORE)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load Harness core")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def payload(tool: str, values: dict[str, str]) -> dict[str, object]:
    return {"tool_name": tool, "tool_input": values}


def expect(allowed: bool, actual, label: str) -> None:
    if actual.allowed != allowed:
        raise AssertionError(f"{label}: allowed={actual.allowed} message={actual.message}")


def main() -> int:
    core = load_core()
    if "trusted-review-staged.py" in CORE.read_text():
        raise AssertionError("preflight still invokes the removed submission reviewer")
    office_hint = core.context_hint(
        payload("apply_patch", {"patch": "*** Begin Patch\n*** Update File: internal/office/service.go\n"}),
        ROOT,
    )
    if "base/agent-runtime/office" not in office_hint:
        raise AssertionError("Office edit did not receive recursive context ancestry")
    if "docs/office-human-approval.md" not in office_hint:
        raise AssertionError("Office edit did not receive its lazy context pointers")
    if len(office_hint.encode()) > 1024:
        raise AssertionError("recursive context hint exceeded its hard budget")
    for relative, expected in (
        ("internal/browsernode/host.go", "base/platform"),
        ("internal/servicehost/service.go", "base/platform"),
        ("internal/discovery/service.go", "base/security"),
        ("internal/observability/metrics.go", "base/security"),
        ("scripts/build-goai-preliminary-deck.py", "base/delivery"),
    ):
        hint = core.context_hint(
            payload(
                "apply_patch",
                {"patch": f"*** Begin Patch\n*** Update File: {relative}\n"},
            ),
            ROOT,
        )
        if expected not in hint:
            raise AssertionError(f"{relative} did not receive {expected} context")
    shell_office_hint = core.context_hint(
        payload(
            "exec_command",
            {"cmd": "sed -i '' 's/old/new/' internal/office/service.go"},
        ),
        ROOT,
    )
    if "base/agent-runtime/office" not in shell_office_hint:
        raise AssertionError("shell edit path bypassed recursive context routing")
    dynamic_write_hint = core.context_hint(
        payload("shell", {"command": "target=$FILE; sed -i '' 's/x/y/' \"$target\""}),
        ROOT,
    )
    if "无法从这条 shell 写入中确定目标分支" not in dynamic_write_hint:
        raise AssertionError("unresolved shell write did not receive conservative guidance")
    python_write_hint = core.context_hint(
        payload(
            "exec_command",
            {"cmd": "python3 -c \"open('internal/office/service.go','w').write('x')\""},
        ),
        ROOT,
    )
    if "base/agent-runtime/office" not in python_write_hint:
        raise AssertionError("path embedded in Python write bypassed recursive routing")
    unknown_writer_hint = core.context_hint(
        payload("shell", {"command": "custom-code-generator --output $TARGET"}),
        ROOT,
    )
    if "无法从这条 shell 写入中确定目标分支" not in unknown_writer_hint:
        raise AssertionError("unknown shell writer did not fail conservatively")
    if core.context_hint(payload("shell", {"command": "git status"}), ROOT):
        raise AssertionError("known read-only shell command received noisy context")
    if core.has_hook_bypass(("commit", (), ("-m", "document --no-verify behavior"))):
        raise AssertionError("commit message text was mistaken for --no-verify")
    if core.has_hook_bypass(("commit", (), ("--message=-n is documented",))):
        raise AssertionError("inline commit message text was mistaken for -n")
    if not core.has_hook_bypass(("commit", (), ("--no-verify", "-m", "x"))):
        raise AssertionError("real --no-verify was not detected")
    expect(True, core.evaluate(payload("shell", {"command": "git status"}), ROOT), "read")
    expect(
        False,
        core.evaluate(payload("shell", {"command": "ecs-harness-block-canary"}), ROOT),
        "exit-2 canary",
    )
    expect(
        False,
        core.evaluate(payload("shell", {"command": "git commit --no-verify -m x"}), ROOT),
        "no-verify",
    )
    for command in (
        "/usr/bin/git commit --no-verify -m x",
        "git --no-pager commit --no-verify -m x",
        "git -c core.hooksPath=/dev/null commit -m x",
        "git commit -n -m x",
        "command git -C . commit --no-verify -m x",
        "$(command -v git) commit --no-verify -m x",
        "(git commit --no-verify -m x)",
        "$(command -v git) -c core.hooksPath=/dev/null commit -m x",
        "git push --no-verify origin main",
        "git add -A && git -c core.hookspath=/dev/null commit -m x",
        "git --config-env=core.HOOKSPATH=HOOKS_PATH commit -m x",
        "k=core.hooksPath; git -c \"$k=/dev/null\" commit -m x",
        "git -c user.name=ECS commit -m x",
        "git --config-env=user.name=IDENTITY push origin main",
        "git -c alias.x='!git push --no-verify origin main' x",
        "git ci --no-verify -m x",
        "git ci -n -m x",
        "g=git; $g push --no-verify origin main",
        "sh -c 'git push --no-verify origin main'",
        "bash -c 'git config core.hooksPath /tmp/empty'",
        "zsh -c 'g=git; $g push origin main'",
    ):
        expect(
            False,
            core.evaluate(payload("shell", {"command": command}), ROOT),
            f"git bypass: {command}",
        )
    expect(
        False,
        core.evaluate(
            payload("shell", {"command": "git config core.hooksPath /tmp/empty"}),
            ROOT,
        ),
        "standalone hooksPath override",
    )
    expect(
        False,
        core.evaluate(
            payload("shell", {"command": 'git config core."hooksPath" /tmp/empty'}),
            ROOT,
        ),
        "quoted hooksPath override",
    )
    for command in (
        "(git commit -m x)",
        "sh -c 'git commit -m x'",
        "$(command -v git) commit -m x",
        "x=c; x+=ommit; n=-n; git $x $n -m msg",
        "g=git; $g commit -m x",
    ):
        expect(
            False,
            core.evaluate(payload("shell", {"command": command}), ROOT),
            f"ambiguous wrapped commit: {command}",
        )
    expect(
        True,
        core.evaluate(payload("shell", {"command": "git custom-helper --check"}), ROOT),
        "custom Git helper",
    )
    expect(
        True,
        core.evaluate(payload("shell", {"command": "sh -c 'printf ok'"}), ROOT),
        "non-sensitive nested shell",
    )
    preflight_calls = []
    original_fast_checks = core.fast_checks
    core.fast_checks = lambda _root: (
        preflight_calls.append(True) or core.Decision(True)
    )
    try:
        expect(
            True,
            core.evaluate(payload("shell", {"command": "git push origin main"}), ROOT),
            "push delegates to pre-push",
        )
        if preflight_calls:
            raise AssertionError("git push duplicated the pre-push checks")
        expect(
            True,
            core.evaluate(payload("shell", {"command": "git commit -m x"}), ROOT),
            "commit preflight",
        )
        if len(preflight_calls) != 1:
            raise AssertionError("git commit did not retain its fast preflight")
    finally:
        core.fast_checks = original_fast_checks
    expect(
        False,
        core.evaluate(
            payload(
                "apply_patch",
                {"patch": "*** Begin Patch\n*** Update File: .codex/hooks.json\n"},
            ),
            ROOT,
        ),
        "generated registration",
    )
    for command in (
        "python3 -c \"open('.codex/hooks.json', 'w').write('x')\"",
        "sed -i '' 's/x/y/' .codex/hooks.json",
        "printf x > .codex/hooks.json",
        "git add .codex/hooks.json && printf x > .codex/hooks.json",
        "printf x > .codex/./hooks.json",
    ):
        expect(
            False,
            core.evaluate(payload("shell", {"command": command}), ROOT),
            f"generated registration shell write: {command}",
        )
    expect(
        True,
        core.evaluate(
            payload("shell", {"command": "git add .codex/hooks.json"}), ROOT
        ),
        "stage generated registration",
    )
    malformed = io.StringIO("[]")
    try:
        core.load_payload(malformed)
    except ValueError:
        pass
    else:
        raise AssertionError("non-object payload did not fail closed")

    hooks = json.loads((ROOT / ".codex/hooks.json").read_text())
    matcher = hooks["hooks"]["PreToolUse"][0]["matcher"].split("|")
    for writable_tool in ("Bash", "Edit", "Write", "apply_patch", "shell", "exec_command", "write_stdin"):
        if writable_tool not in matcher:
            raise AssertionError(f"PreToolUse misses writable tool: {writable_tool}")
    pre_tool_command = hooks["hooks"]["PreToolUse"][0]["hooks"][0]["command"]
    blocked = subprocess.run(
        pre_tool_command,
        cwd=ROOT,
        shell=True,
        input=json.dumps(payload("shell", {"command": "ecs-harness-block-canary"})),
        capture_output=True,
        text=True,
    )
    if blocked.returncode != 2 or "expected exit 2 block" not in blocked.stderr:
        raise AssertionError(
            f"generated PreToolUse command did not block: rc={blocked.returncode} "
            f"stderr={blocked.stderr!r}"
        )
    shell_write = subprocess.run(
        pre_tool_command,
        cwd=ROOT,
        shell=True,
        input=json.dumps(
            payload("shell", {"command": "printf x > .codex/hooks.json"})
        ),
        capture_output=True,
        text=True,
    )
    if shell_write.returncode != 2 or "direct shell access" not in shell_write.stderr:
        raise AssertionError(
            "generated PreToolUse command did not block direct shell write: "
            f"rc={shell_write.returncode} stderr={shell_write.stderr!r}"
        )

    digest = re.search(r"sha256:[0-9a-f]{64}", pre_tool_command)
    if digest is None:
        raise AssertionError("generated hook command has no bundle digest")
    stale_command = pre_tool_command.replace(digest.group(), "sha256:" + "0" * 64)
    stale = subprocess.run(
        stale_command,
        cwd=ROOT,
        shell=True,
        input=json.dumps(payload("shell", {"command": "git status"})),
        capture_output=True,
        text=True,
    )
    if stale.returncode != 2 or "bundle changed" not in stale.stderr:
        raise AssertionError("stale bundle digest did not fail closed before adapter execution")

    session_command = hooks["hooks"]["SessionStart"][0]["hooks"][0]["command"]
    output = subprocess.run(
        session_command,
        cwd=ROOT,
        shell=True,
        env={**dict(os.environ), "ECS_HARNESS_OUTPUT_CANARY": "1"},
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    context = json.loads(output)["hookSpecificOutput"]["additionalContext"]
    if len(context) < 50_000:
        raise AssertionError("long-output canary is too small to cross 2500 tokens")

    with tempfile.TemporaryDirectory(prefix="ecs-harness-real-positive-") as temporary:
        fixture = pathlib.Path(temporary)
        for relative in (
            ".codex",
            ".githooks",
            "scripts/harness",
        ):
            shutil.copytree(ROOT / relative, fixture / relative)
        for relative in (
            "AGENTS.md",
            "CLAUDE.md",
            "scripts/install-harness.sh",
            "scripts/render-codex-hooks.py",
            "scripts/verify-harness.py",
        ):
            destination = fixture / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, destination)
        with (fixture / "AGENTS.md").open("a") as stream:
            stream.write("\n" + "X" * 8193)
        real_violation = subprocess.run(
            [sys.executable, "scripts/verify-harness.py"],
            cwd=fixture,
            capture_output=True,
            text=True,
        )
        if real_violation.returncode == 0 or "exceeds recursive tree budget" not in (
            real_violation.stderr + real_violation.stdout
        ):
            raise AssertionError(
                "real oversized AGENTS.md fixture did not fail the Harness gate"
            )

    with tempfile.TemporaryDirectory(prefix="ecs-installer-order-") as temporary:
        fixture = pathlib.Path(temporary)
        subprocess.run(["git", "init", "-q"], cwd=fixture, check=True)
        for relative in (
            ".codex",
            ".githooks",
            "scripts/harness",
        ):
            shutil.copytree(ROOT / relative, fixture / relative)
        for relative in (
            "AGENTS.md",
            "CLAUDE.md",
            "scripts/install-harness.sh",
            "scripts/render-codex-hooks.py",
            "scripts/verify-agents-md.py",
            "scripts/verify-harness.py",
        ):
            destination = fixture / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, destination)
        subprocess.run(["git", "add", "-A"], cwd=fixture, check=True)
        subprocess.run(
            ["git", "-c", "user.name=ECS", "-c", "user.email=ecs@example.invalid",
             "commit", "-qm", "fixture"],
            cwd=fixture,
            check=True,
        )
        canary = fixture / "candidate-ran"
        (fixture / "scripts/verify-harness.py").write_text(
            f"import pathlib; pathlib.Path({str(canary)!r}).write_text('unsafe')\n"
        )
        install = subprocess.run(
            ["bash", "scripts/install-harness.sh"],
            cwd=fixture,
            capture_output=True,
            text=True,
        )
        if install.returncode != 2 or canary.exists():
            raise AssertionError(
                "installer executed an uncommitted candidate validator: "
                f"rc={install.returncode} canary={canary.exists()}"
            )

    print("ECS compact Harness known-positive tests passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
