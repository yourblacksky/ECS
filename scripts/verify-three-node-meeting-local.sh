#!/usr/bin/env bash
set -euo pipefail

# Local (Docker-free) verification of the three-node meeting follow-up loop:
# starts one mock-llm and three real ECS processes (controller, worker-a,
# worker-b) with localhost URLs derived from demo/three-node/*.json, then runs
# cmd/ecs-office-network-demo to drive scope -> extract -> verify -> dispatch
# across the workers, including the human approval gate. This mirrors what
# docker.yml does inside containers, without requiring Docker.

export LC_ALL=C
export LANG=C

script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_directory}/.." && pwd)"
cd "${repository_root}"

work_root="$(mktemp -d "${TMPDIR:-/tmp}/ecs-three-node-local.XXXXXX")"
controller_pid=""
worker_a_pid=""
worker_b_pid=""
mock_pid=""

cleanup() {
  for pid in "${controller_pid}" "${worker_a_pid}" "${worker_b_pid}" "${mock_pid}"; do
    if [[ -n "${pid}" ]]; then
      kill -TERM "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  done
  rm -rf -- "${work_root}"
}
trap cleanup EXIT

go build -trimpath -o "${work_root}/ecs" ./cmd/ecs
go build -trimpath -o "${work_root}/mock-llm" ./cmd/ecs-mock-llm

"${work_root}/mock-llm" -listen "127.0.0.1:11434" >"${work_root}/mock-llm.log" 2>&1 &
mock_pid=$!

python3 - "${work_root}" "${repository_root}" <<'PY'
import json
import os
import sys

work, root = sys.argv[1], sys.argv[2]
ports = {"controller": 18090, "worker-a": 18091, "worker-b": 18092}
names = {"controller": "ecs-controller", "worker-a": "ecs-worker-a", "worker-b": "ecs-worker-b"}

def fix(value, node):
    if not isinstance(value, str) or not value.startswith("../"):
        return value
    if value.startswith("../../data"):
        return os.path.join(work, f"data-{node}", value[len("../../data/"):])
    if value.startswith("../../"):
        return os.path.join(root, value[len("../../"):])
    return value

for node, port in ports.items():
    path = os.path.join(root, "demo", "three-node", f"{node}.json")
    with open(path) as handle:
        data = json.load(handle)
    data["listen"] = f"127.0.0.1:{port}"
    data["llm"]["base_url"] = data["llm"]["base_url"].replace(
        "http://mock-llm:11434/v1", "http://127.0.0.1:11434/v1")
    for key in ("system_prompt_file", "conversation_guidance_file"):
        data["agent"][key] = fix(data["agent"][key], node)
    for section in ("agent_roles", "agentteams", "office", "skills", "tools", "trace", "sessions", "discovery"):
        if section not in data:
            continue
        for key, value in list(data[section].items()):
            if isinstance(value, str) and value.startswith("../"):
                data[section][key] = fix(value, node)
    if data.get("discovery", {}).get("advertise_url"):
        data["discovery"]["advertise_url"] = f"http://127.0.0.1:{port}"
    for child in data.get("nested", {}).get("children", []):
        child["url"] = f"http://127.0.0.1:{ports[child['name'].removeprefix('ecs-')]}"
    for peers in data.get("groups", {}).values():
        for peer in peers:
            peer["url"] = f"http://127.0.0.1:{ports[peer['name'].removeprefix('ecs-')]}"
    with open(os.path.join(work, f"{node}.json"), "w") as handle:
        json.dump(data, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
PY

wait_for_url() {
  local url="$1"
  for _ in $(seq 1 100); do
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "timed out waiting for ${url}" >&2
  return 1
}

"${work_root}/ecs" -config "${work_root}/controller.json" >"${work_root}/controller.log" 2>&1 &
controller_pid=$!
"${work_root}/ecs" -config "${work_root}/worker-a.json" >"${work_root}/worker-a.log" 2>&1 &
worker_a_pid=$!
"${work_root}/ecs" -config "${work_root}/worker-b.json" >"${work_root}/worker-b.log" 2>&1 &
worker_b_pid=$!

wait_for_url "http://127.0.0.1:18090/healthz"
wait_for_url "http://127.0.0.1:18091/healthz"
wait_for_url "http://127.0.0.1:18092/healthz"

go run ./cmd/ecs-office-network-demo \
  -controller "http://127.0.0.1:18090" \
  -token "demo-controller-token" \
  -admin-token "demo-admin-token"
