#!/usr/bin/env python3
"""Shared project-governance decisions for coding-agent adapters."""

from __future__ import annotations

import json
import importlib.util
import pathlib
import re
import shlex
import subprocess
import sys
from dataclasses import dataclass
from typing import Any


PR_CREATE = re.compile(r"(?:^|[;&|]\s*|\s)gh\s+pr\s+create\b")
COMMIT_WORD = re.compile(r"(?<![\w-])commit(?![\w-])")
GIT_WORD = re.compile(r"(?<![\w-])git(?![\w-])")
GENERATED_HOOKS = ".codex/hooks.json"
MUTATING_SUBCOMMANDS = {"commit", "push", "merge", "rebase"}
PREFLIGHT_SUBCOMMANDS = {"commit", "merge", "rebase"}
GIT_OPTIONS_WITH_VALUE = {
    "-C",
    "-c",
    "--exec-path",
    "--git-dir",
    "--namespace",
    "--super-prefix",
    "--work-tree",
}
SAFE_GENERATED_FILE_GIT_COMMANDS = {"add", "diff", "show", "status"}
SHELL_CONTROL = re.compile(r"&&|\|\||[;|\n<>]")
SHELL_INTERPRETERS = {"sh", "bash", "zsh", "dash", "ksh"}
DYNAMIC_COMMAND = re.compile(
    r"(?:^|[;&|]\s*)\s*(?:[A-Za-z_][A-Za-z0-9_]*=[^;|&\s]+\s+)*(?:\$|`)"
)
PATH_FRAGMENT = re.compile(r"(?<![A-Za-z0-9_.-])((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.@*?+-]+)")
READ_ONLY_PROGRAMS = {"cat", "find", "grep", "head", "ls", "pwd", "rg", "stat", "tail", "wc"}
READ_ONLY_GIT = {"blame", "cat-file", "describe", "diff", "grep", "log", "ls-files", "ls-tree", "rev-list", "rev-parse", "show", "show-ref", "status"}
READ_ONLY_GO = {"env", "list", "test", "version", "vet"}
COMMIT_OPTIONS_WITH_VALUE = {
    "-m", "--message", "-F", "--file", "-C", "-c", "--reuse-message",
    "--reedit-message", "--author", "--date", "--cleanup", "--fixup",
    "--squash", "--pathspec-from-file", "--trailer",
}


@dataclass(frozen=True)
class Decision:
    allowed: bool
    message: str = ""


def repository_root() -> pathlib.Path:
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        check=True,
        capture_output=True,
        text=True,
    )
    return pathlib.Path(result.stdout.strip()).resolve()


def load_payload(stream: Any = sys.stdin) -> dict[str, Any]:
    raw = stream.read()
    if not raw.strip():
        return {}
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ValueError("hook payload must be a JSON object")
    return value


def tool_input(payload: dict[str, Any]) -> dict[str, Any]:
    value = payload.get("tool_input")
    return value if isinstance(value, dict) else {}


def command_text(payload: dict[str, Any]) -> str:
    values = tool_input(payload)
    for key in ("command", "cmd", "input"):
        value = values.get(key)
        if isinstance(value, str):
            return value
    return ""


def shell_tokens(command: str) -> tuple[str, ...]:
    try:
        lexer = shlex.shlex(command, posix=True, punctuation_chars=";&|<>")
        lexer.whitespace_split = True
        return tuple(lexer)
    except ValueError:
        return ()


def git_invocations(command: str) -> list[tuple[str, tuple[str, ...], tuple[str, ...]]]:
    """Return (subcommand, global options, args) for literal git invocations."""
    invocations: list[tuple[str, tuple[str, ...], tuple[str, ...]]] = []
    for segment in re.split(r"(?:&&|\|\||[;|\n])", command):
        try:
            tokens = shlex.split(segment)
        except ValueError:
            continue
        git_index = next(
            (
                index
                for index, token in enumerate(tokens)
                if pathlib.PurePath(token).name == "git"
            ),
            None,
        )
        if git_index is None:
            continue
        index = git_index + 1
        global_options: list[str] = []
        while index < len(tokens) and tokens[index].startswith("-"):
            option = tokens[index]
            global_options.append(option)
            index += 1
            name = option.split("=", 1)[0]
            if "=" not in option and name in GIT_OPTIONS_WITH_VALUE and index < len(tokens):
                global_options.append(tokens[index])
                index += 1
        if index < len(tokens):
            invocations.append(
                (tokens[index], tuple(global_options), tuple(tokens[index + 1 :]))
            )
    return invocations


def uses_shell_command_wrapper(command: str) -> bool:
    """Fail closed for nested command strings that cannot be parsed safely here."""
    candidates = [command, *re.split(r"(?:&&|\|\||[;|\n])", command)]
    for segment in candidates:
        try:
            tokens = shlex.split(segment)
        except ValueError:
            continue
        for index, token in enumerate(tokens):
            if pathlib.PurePath(token).name in SHELL_INTERPRETERS and "-c" in tokens[index + 1 :]:
                return True
    return False


def has_hook_bypass(call: tuple[str, tuple[str, ...], tuple[str, ...]]) -> bool:
    subcommand, _global_options, args = call
    if subcommand != "commit":
        return "--no-verify" in args or "-n" in args
    index = 0
    while index < len(args):
        argument = args[index]
        if argument == "--":
            return False
        if argument in {"--no-verify", "-n"}:
            return True
        option = argument.split("=", 1)[0]
        if option in COMMIT_OPTIONS_WITH_VALUE and "=" not in argument:
            index += 2
        else:
            index += 1
    return False


def safe_generated_file_reference(
    command: str, git_calls: list[tuple[str, tuple[str, ...], tuple[str, ...]]]
) -> bool:
    """Allow only one unchained, read/stage-oriented Git command for hooks.json."""
    return (
        len(git_calls) == 1
        and git_calls[0][0] in SAFE_GENERATED_FILE_GIT_COMMANDS
        and not SHELL_CONTROL.search(command)
    )


def edited_paths(payload: dict[str, Any]) -> list[str]:
    values = tool_input(payload)
    paths: list[str] = []
    for key in ("file_path", "path"):
        value = values.get(key)
        if isinstance(value, str) and value.strip():
            paths.append(value.strip())
    patch = next(
        (
            value
            for key in ("patch", "input", "command")
            if isinstance((value := values.get(key)), str)
        ),
        "",
    )
    paths.extend(
        match.group(1).strip()
        for match in re.finditer(
            r"^\*\*\* (?:Add|Update|Delete) File: (.+)$", patch, re.MULTILINE
        )
    )
    return paths


def normalize_path(value: str, root: pathlib.Path) -> str:
    candidate = pathlib.Path(value)
    if candidate.is_absolute():
        try:
            candidate = candidate.resolve().relative_to(root)
        except ValueError:
            return candidate.as_posix()
    normalized = candidate.as_posix()
    while normalized.startswith("./"):
        normalized = normalized[2:]
    return normalized


def referenced_paths(command: str, root: pathlib.Path) -> set[str]:
    return {
        normalize_path(token, root)
        for token in shell_tokens(command)
        if token not in {";", "&&", "||", "|", "<", ">", ">>", "<<"}
    }


def command_path_candidates(command: str, root: pathlib.Path) -> set[str]:
    candidates = referenced_paths(command, root)
    candidates.update(normalize_path(match.group(1), root) for match in PATH_FRAGMENT.finditer(command))
    for token in shell_tokens(command):
        if "=" in token:
            candidates.add(normalize_path(token.split("=", 1)[1], root))
    return candidates


def shell_is_provably_read_only(command: str) -> bool:
    segments = re.split(r"(?:&&|\|\||[;|\n])", command)
    saw_command = False
    for segment in segments:
        try:
            tokens = shlex.split(segment)
        except ValueError:
            return False
        tokens = [token for token in tokens if token not in {"(", ")"}]
        while tokens and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", tokens[0]):
            tokens.pop(0)
        if not tokens:
            continue
        saw_command = True
        program = pathlib.PurePath(tokens[0]).name
        if program in READ_ONLY_PROGRAMS:
            continue
        if program == "git" and len(tokens) > 1 and tokens[1] in READ_ONLY_GIT:
            continue
        if program == "go" and len(tokens) > 1 and tokens[1] in READ_ONLY_GO:
            continue
        return False
    return saw_command


def load_route_module(root: pathlib.Path) -> Any:
    source = root / "scripts/harness/route_tree.py"
    spec = importlib.util.spec_from_file_location("ecs_context_route_tree", source)
    if spec is None or spec.loader is None:
        raise ValueError("cannot load ECS Harness route tree")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def context_hint(payload: dict[str, Any], root: pathlib.Path) -> str:
    command = command_text(payload)
    candidates = [normalize_path(path, root) for path in edited_paths(payload)]
    if command:
        candidates.extend(sorted(command_path_candidates(command, root)))
    paths = tuple(dict.fromkeys(candidates))
    if not paths and (not command or shell_is_provably_read_only(command)):
        return ""
    route = load_route_module(root)
    raw = (root / "scripts/harness/context-routes.json").read_bytes()
    matches = route.select(raw, paths)
    _, _, budgets = route.parse(raw)
    lines = ["ECS 递归上下文树命中；操作前只读取下列分支所需资料："]
    for match in matches:
        if not match.contexts:
            continue
        lines.append(f"- {'/'.join(match.ancestry)}")
        lines.extend(f"  - {context}" for context in match.contexts)
    if len(lines) == 1:
        if command and not shell_is_provably_read_only(command):
            return (
                "ECS 无法从这条 shell 写入中确定目标分支；请改用带显式仓库路径的命令，"
                "或先按 AGENTS.md 路由读取对应资料。"
            )
        return ""
    hint = "\n".join(lines)
    if len(hint.encode()) > budgets["runtime_hint_max_bytes"]:
        raise ValueError("recursive context hint exceeds runtime budget")
    return hint


def run_check(root: pathlib.Path, command: list[str]) -> Decision:
    result = subprocess.run(command, cwd=root, capture_output=True, text=True)
    if result.returncode == 0:
        return Decision(True)
    detail = (result.stderr or result.stdout).strip()
    return Decision(False, detail or f"check failed: {' '.join(command)}")


def fast_checks(root: pathlib.Path) -> Decision:
    checks = [
        ["git", "diff", "--check"],
        ["git", "diff", "--cached", "--check"],
        [sys.executable, "scripts/render-codex-hooks.py", "--check"],
        [sys.executable, "scripts/verify-agents-md.py"],
    ]
    for command in checks:
        decision = run_check(root, command)
        if not decision.allowed:
            return decision
    return Decision(True)


def evaluate(payload: dict[str, Any], root: pathlib.Path) -> Decision:
    command = command_text(payload)
    sensitive_command = bool(
        GIT_WORD.search(command)
        or COMMIT_WORD.search(command)
        or "--no-verify" in command
        or "core.hookspath" in command.lower()
        or GENERATED_HOOKS in command
    )
    if DYNAMIC_COMMAND.search(command) and sensitive_command:
        return Decision(
            False,
            "ECS Harness cannot inspect a dynamically expanded sensitive command; invoke it directly",
        )
    if uses_shell_command_wrapper(command) and sensitive_command:
        return Decision(
            False,
            "ECS Harness cannot inspect a nested sensitive shell command; run it directly",
        )
    git_calls = git_invocations(command)
    if "ecs-harness-block-canary" in command:
        return Decision(False, "ECS Harness canary: expected exit 2 block")
    if any("$" in call[0] or "`" in call[0] for call in git_calls):
        return Decision(
            False,
            "ECS Harness forbids a dynamically expanded Git subcommand; use a direct Git command",
        )
    mutating_calls = [call for call in git_calls if call[0] in MUTATING_SUBCOMMANDS]
    if any(
        any("$" in token or "`" in token for token in (*call[1], *call[2]))
        for call in mutating_calls
    ):
        return Decision(
            False,
            "ECS Harness forbids dynamic expansion in mutating Git commands",
        )
    if any(
        any(
            option == "-c" or option.startswith("-c=") or option.startswith("--config-env")
            for option in call[1]
        )
        for call in git_calls
    ):
        return Decision(
            False,
            "ECS Harness forbids temporary Git configuration because it can inject aliases",
        )
    commit_like = bool(COMMIT_WORD.search(command))
    if (
        commit_like
        and GIT_WORD.search(command)
        and not any(call[0] == "commit" for call in git_calls)
    ):
        return Decision(
            False,
            "ECS Harness cannot safely parse a wrapped git commit; run git commit directly",
        )
    if any(has_hook_bypass(call) for call in git_calls):
        return Decision(False, "ECS Harness forbids bypassing Git hooks with --no-verify")
    configures_hooks = any(
        call[0] == "config"
        and any(argument.lower() == "core.hookspath" for argument in call[2])
        for call in git_calls
    )
    if configures_hooks or "core.hookspath" in command.lower():
        return Decision(
            False,
            "ECS Harness forbids direct core.hooksPath changes; use "
            "scripts/install-harness.sh",
        )

    shell_paths = referenced_paths(command, root)
    if (GENERATED_HOOKS in command or GENERATED_HOOKS in shell_paths) and not safe_generated_file_reference(
        command, git_calls
    ):
        return Decision(
            False,
            "ECS Harness: direct shell access to .codex/hooks.json is blocked; "
            "regenerate it with python3 scripts/render-codex-hooks.py --write",
        )

    targets = {normalize_path(path, root) for path in edited_paths(payload)}
    if GENERATED_HOOKS in targets:
        return Decision(
            False,
            "ECS Harness: .codex/hooks.json is generated; run "
            "python3 scripts/render-codex-hooks.py --write",
        )
    if any(
        call[0] in PREFLIGHT_SUBCOMMANDS for call in mutating_calls
    ) or PR_CREATE.search(command):
        decision = fast_checks(root)
        if not decision.allowed:
            return Decision(False, f"ECS Harness preflight failed:\n{decision.message}")
    return Decision(True)


def pre_tool_use_main() -> int:
    try:
        payload = load_payload()
        root = repository_root()
        decision = evaluate(payload, root)
        hint = context_hint(payload, root) if decision.allowed else ""
    except Exception as exc:  # A broken guard must not silently fail open.
        print(f"ECS Harness internal failure: {exc}", file=sys.stderr)
        return 2
    if decision.allowed:
        if hint:
            print(
                json.dumps(
                    {
                        "hookSpecificOutput": {
                            "hookEventName": "PreToolUse",
                            "additionalContext": hint,
                        }
                    },
                    ensure_ascii=False,
                )
            )
        return 0
    print(decision.message, file=sys.stderr)
    return 2
