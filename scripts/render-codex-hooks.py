#!/usr/bin/env python3
"""Render Codex hook registration with a digest-bound runtime bundle."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
OUTPUT = ROOT / ".codex/hooks.json"
BUNDLE_PATHS = (
    ".codex/hooks/pre-tool-use.py",
    ".codex/hooks/session-start.py",
    "scripts/harness/core.py",
    "scripts/harness/route_tree.py",
    "scripts/harness/context-routes.json",
)


def bundle_digest(root: pathlib.Path = ROOT) -> str:
    digest = hashlib.sha256()
    for relative in BUNDLE_PATHS:
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update((root / relative).read_bytes())
        digest.update(b"\0")
    return f"sha256:{digest.hexdigest()}"


def verified_command(adapter: str, digest: str) -> str:
    paths = repr(BUNDLE_PATHS)
    code = (
        "import hashlib,pathlib,runpy,sys;"
        "r=pathlib.Path(sys.argv[1]);e=sys.argv[2];a=sys.argv[3];"
        f"ps={paths};h=hashlib.sha256();"
        "[(h.update(p.encode()),h.update(b'\\0'),h.update((r/p).read_bytes()),"
        "h.update(b'\\0')) for p in ps];"
        "v='sha256:'+h.hexdigest();"
        "(print('ECS Harness bundle changed; regenerate hooks.json and re-trust in /hooks',"
        "file=sys.stderr),sys.exit(2)) if v!=e else None;"
        "sys.argv=[str(r/a)];runpy.run_path(str(r/a),run_name='__main__')"
    )
    return (
        f'python3 -c "{code}" "$(git rev-parse --show-toplevel)" '
        f'"{digest}" "{adapter}"'
    )


def rendered(root: pathlib.Path = ROOT) -> str:
    digest = bundle_digest(root)
    document = {
        "description": (
            "ECS 精简 Harness。Hook command 绑定共享核心摘要；摘要变化后需在 "
            "Codex /hooks 重新授信。"
        ),
        "hooks": {
            "SessionStart": [
                {
                    "matcher": "startup|resume",
                    "hooks": [
                        {
                            "type": "command",
                            "command": verified_command(
                                ".codex/hooks/session-start.py", digest
                            ),
                            "timeout": 5,
                            "additionalContextLimit": 2500,
                        }
                    ],
                }
            ],
            "PreToolUse": [
                {
                    "matcher": (
                        "Bash|Edit|Write|apply_patch|shell|exec_command|write_stdin"
                    ),
                    "hooks": [
                        {
                            "type": "command",
                            "command": verified_command(
                                ".codex/hooks/pre-tool-use.py", digest
                            ),
                            "timeout": 30,
                            "statusMessage": "Checking ECS project governance",
                        }
                    ],
                }
            ],
        },
    }
    return json.dumps(document, ensure_ascii=False, indent=2) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--check", action="store_true")
    args = parser.parse_args()
    expected = rendered()
    if args.write:
        OUTPUT.parent.mkdir(parents=True, exist_ok=True)
        OUTPUT.write_text(expected)
        print(f"rendered {OUTPUT.relative_to(ROOT)} ({bundle_digest()})")
        return 0
    actual = OUTPUT.read_text() if OUTPUT.exists() else ""
    if actual != expected:
        print(
            ".codex/hooks.json is stale; run "
            "python3 scripts/render-codex-hooks.py --write",
            file=sys.stderr,
        )
        return 1
    print(f"Codex hook registration verified ({bundle_digest()})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
