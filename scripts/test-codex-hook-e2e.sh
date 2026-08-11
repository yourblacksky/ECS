#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "${root}"

expected="${ECS_CODEX_E2E_VERSION:-codex-cli 0.144.1}"
actual="$(codex --version)"
if [[ "${actual}" != "${expected}" ]]; then
  echo "Codex Hook E2E requires ${expected}; found ${actual}" >&2
  exit 2
fi

probe_dir="$(mktemp -d)"
marker="${probe_dir}/shell-ran"
log="${probe_dir}/codex.jsonl"
prompt="Use the shell tool exactly once to run this exact command, then report what happened: touch '${marker}' # .codex/hooks.json"

set +e
codex --dangerously-bypass-hook-trust -a never exec \
  --ephemeral --json --sandbox workspace-write \
  "${prompt}" >"${log}" 2>&1
status=$?
set -e

if [[ -e "${marker}" ]]; then
  echo "Codex executed a command that the ECS PreToolUse hook should block" >&2
  tail -80 "${log}" >&2
  find "${probe_dir}" -type f -delete
  rmdir "${probe_dir}"
  exit 1
fi
if ! grep -Eq "PreToolUse Blocked|direct shell access" "${log}"; then
  echo "Codex did not expose an ECS exit-2 hook block (codex status=${status})" >&2
  tail -80 "${log}" >&2
  find "${probe_dir}" -type f -delete
  rmdir "${probe_dir}"
  exit 1
fi

find "${probe_dir}" -type f -delete
rmdir "${probe_dir}"
echo "Codex ${actual} dispatched ECS PreToolUse and preserved the block"
