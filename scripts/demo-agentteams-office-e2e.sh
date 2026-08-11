#!/usr/bin/env bash
set -euo pipefail

# AgentTeams -> ECS Worker -> Office: complete four-stage meeting follow-up
# loop (scope -> extract -> verify -> dispatch, including the human approval
# gate, deterministic receipt verification, and durable outbox state) on real
# processes. The Controller, Matrix, and LLM boundaries are the deterministic
# ecs-agentteams-testbed; the ECS node executes the Office stages for real.

# Keep git/tar/build helpers independent of the host's optional UTF-8 locale.
export LC_ALL=C
export LANG=C

readonly upstream_repository="${AGENTTEAMS_REPOSITORY:-https://github.com/agentscope-ai/AgentTeams.git}"
readonly upstream_commit="${AGENTTEAMS_COMMIT:-2540c968a642845c4b9382afd75d8c80ed861137}"
readonly script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly repository_root="$(CDPATH= cd -- "${script_directory}/.." && pwd)"
output_directory="${repository_root}/data/demo/agentteams-office-e2e"
cd "${repository_root}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -out|--out)
      output_directory="$2"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

readonly scope_task="agentteams-office-e2e-scope"
readonly extract_task="agentteams-office-e2e-extract"
readonly verify_task="agentteams-office-e2e-verify"
readonly dispatch_task="agentteams-office-e2e-dispatch"
readonly all_tasks="${scope_task},${extract_task},${verify_task},${dispatch_task}"
readonly admin_token="agentteams-e2e-admin"

# This source must stay byte-identical with the e2eSource fixture in
# cmd/ecs-agentteams-testbed/main.go so the deterministic model output lines
# up with the real meeting.fact.extract execution inside ECS.
readonly meeting_source="决定：本周完成可信会议督办演示
行动：整理五分钟演示清单 | owner=张三 | due=2026-08-03 | evidence=会议记录第2页
行动：核对回执证据 | owner=李四 | due=2026-08-04 | evidence=会议记录第3页"

readonly work_root="$(mktemp -d "${TMPDIR:-/tmp}/ecs-agentteams-e2e.XXXXXX")"
readonly upstream_checkout="${work_root}/AgentTeams"
readonly runtime_path="${work_root}/runtime.yaml"
readonly state_directory="${work_root}/state"
readonly controller_port="${ECS_AGENTTEAMS_TESTBED_PORT:-18320}"
readonly ecs_port="${ECS_AGENTTEAMS_ECS_PORT:-18321}"
readonly controller_url="http://127.0.0.1:${controller_port}"
readonly ecs_url="http://127.0.0.1:${ecs_port}"
testbed_pid=""
ecs_pid=""

cleanup() {
	if [[ -f "${work_root}/ecs.log" ]]; then
		cp "${work_root}/ecs.log" "${output_directory}/ecs.log" 2>/dev/null || true
	fi
	if [[ -f "${work_root}/testbed.log" ]]; then
		cp "${work_root}/testbed.log" "${output_directory}/testbed.log" 2>/dev/null || true
	fi
	if [[ -f "${state_directory}/controller-state.json" && \
		! -f "${output_directory}/controller-state.json" ]]; then
		cp "${state_directory}/controller-state.json" "${output_directory}/controller-state.json" 2>/dev/null || true
	fi
  if [[ -n "${ecs_pid}" ]]; then
    kill -TERM "${ecs_pid}" 2>/dev/null || true
    wait "${ecs_pid}" 2>/dev/null || true
  fi
  if [[ -n "${testbed_pid}" ]]; then
    kill -TERM "${testbed_pid}" 2>/dev/null || true
    wait "${testbed_pid}" 2>/dev/null || true
  fi
  rm -rf -- "${work_root}"
}
trap cleanup EXIT

mkdir -p "${state_directory}" "${output_directory}"
git init --quiet "${upstream_checkout}"
git -C "${upstream_checkout}" remote add origin "${upstream_repository}"
git -C "${upstream_checkout}" fetch --quiet --depth=1 origin "${upstream_commit}"
git -C "${upstream_checkout}" checkout --quiet --detach FETCH_HEAD
git -C "${upstream_checkout}" apply --check "${repository_root}/integrations/agentteams/0001-add-ecs-edge-runtime.patch"
git -C "${upstream_checkout}" apply "${repository_root}/integrations/agentteams/0001-add-ecs-edge-runtime.patch"
cp "${repository_root}/integrations/agentteams/e2e/controller_projection_test.go" \
  "${upstream_checkout}/agentteams-controller/internal/service/ecs_runtime_projection_e2e_test.go"

(
  cd "${upstream_checkout}/agentteams-controller"
  ECS_AGENTTEAMS_RUNTIME_OUTPUT="${runtime_path}" \
    go test -tags agentteams_upstream_e2e ./internal/service \
      -run '^TestECSRuntimeProjectionEvidence$' -count=1
)

go build -trimpath -o "${work_root}/ecs" ./cmd/ecs
go build -trimpath -o "${work_root}/ecs-agentteams-testbed" ./cmd/ecs-agentteams-testbed

"${work_root}/ecs-agentteams-testbed" \
  -listen "127.0.0.1:${controller_port}" \
  -state-dir "${state_directory}" \
  -task-id "${all_tasks}" \
  >"${work_root}/testbed.log" 2>&1 &
testbed_pid=$!

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

wait_for_url "${controller_url}/healthz"

cat >"${work_root}/config.json" <<EOF
{
  "node_name": "ecs-agentteams-worker-1",
  "listen": "127.0.0.1:${ecs_port}",
  "auth_token": "",
  "admin_token": "${admin_token}",
  "log_level": "info",
  "llm": {
    "base_url": "${controller_url}/v1",
    "api_key": "",
    "model": "ecs-e2e-model",
    "timeout": "10s",
    "native_tools": false,
    "reasoning_effort": "none"
  },
  "agent": {
    "system_prompt_file": "${repository_root}/prompts/system.small.zh.txt",
    "conversation_guidance_file": "${repository_root}/prompts/conversation.default.zh.txt",
    "conversation_guidance": true,
    "max_system_prompt_chars": 8000,
    "max_agent_context_chars": 2200,
    "max_conversation_guide_chars": 900,
    "max_tool_catalog_chars": 3200,
    "max_tool_rounds": 3,
    "max_prompt_tools": 12,
    "approval_mode": "high_risk",
    "group_synthesis": true,
    "group_dispatch_mode": "single",
    "group_race_fanout": 2
  },
  "agent_roles": {
    "directory": "${repository_root}/agents.d",
    "pipeline_directory": "${repository_root}/pipelines.d",
    "default_pipeline": "default"
  },
  "agentteams": {
    "enabled": true,
    "controller_url": "${controller_url}",
    "worker_name": "ecs-worker-1",
    "runtime_config_file": "${runtime_path}",
    "task_directory": "${state_directory}/shared",
    "max_task_prompt_chars": 3000,
    "heartbeat_interval": "200ms",
    "matrix_url": "${controller_url}",
    "matrix_token_env": "AGENTTEAMS_WORKER_MATRIX_TOKEN",
    "matrix_sync_timeout": "1s",
    "matrix_since_file": "${state_directory}/matrix-since"
  },
  "office": {
    "directory": "${repository_root}/office.d",
    "order_directory": "${state_directory}/office/orders",
    "max_stages": 16,
    "max_context_entries": 64,
    "approval_ttl": "15m",
    "attempt_lease": "2m",
    "reaper_interval": "30s"
  },
  "tools": {
    "directory": "${repository_root}/tools.d",
    "default_timeout": "10s",
    "max_output_bytes": 65536
  },
  "skills": {
    "directory": "${repository_root}/skills.d",
    "knowledge_directory": "${repository_root}/skills.d/_knowledge",
    "max_prompt_skills": 5,
    "max_steps": 4,
    "documentation_required": true,
    "grounding_enabled": true,
    "max_document_chars": 12000,
    "max_grounding_chars": 1800,
    "max_knowledge_documents": 3
  },
  "trace": {
    "enabled": true,
    "directory": "${state_directory}/traces",
    "max_value_chars": 4000,
    "max_traces": 1000,
    "retention": "168h"
  },
  "admin": {"enabled": true, "log_capacity": 1000},
  "sessions": {
    "directory": "${state_directory}/sessions",
    "max_messages": 0,
    "context_messages": 20,
    "max_context_chars": 4000
  },
  "discovery": {"enabled": false, "service_type": "_ecs._tcp", "interval": "5s", "expiry": "20s"},
  "network_auth": {"enabled": false, "directory": "${state_directory}/network", "heartbeat_interval": "30s", "lease_timeout": "90s", "enrollment_ttl": "10m"},
  "http": {
    "read_timeout": "10s",
    "write_timeout": "30s",
    "max_body_bytes": 1048576,
    "max_concurrent": 16,
    "peer_timeout": "30s",
    "peer_retries": 0,
    "retry_backoff": "100ms"
  },
  "groups": {}
}
EOF

AGENTTEAMS_WORKER_MATRIX_TOKEN="ecs-agentteams-e2e-token" \
  "${work_root}/ecs" -config "${work_root}/config.json" >"${work_root}/ecs.log" 2>&1 &
ecs_pid=$!
wait_for_url "${ecs_url}/healthz"

source_json="$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1], ensure_ascii=False))' "${meeting_source}")"
curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  -d "{\"requested_by\":\"agentteams-e2e\",\"input\":{\"meeting_title\":\"AgentTeams ECS E2E\",\"source\":${source_json}}}" \
  "${ecs_url}/v1/office/components/meeting_followup/work-orders" \
  >"${work_root}/work-order-created.json"
work_order_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${work_root}/work-order-created.json")"

assigned_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
create_task() {
  local task_id="$1" stage="$2" depends_on="$3" title="$4"
  local task_directory="${state_directory}/shared/tasks/${task_id}"
  mkdir -p "${task_directory}"
  cat >"${task_directory}/spec.md" <<EOF
# Task ${task_id}

Complete the ${stage} stage of Office work order ${work_order_id}.

## Acceptance Criteria
- AgentTeams task is mapped to Office stage ${stage}.
- ECS persists an execution trace and reports completion through Matrix.
EOF
  cat >"${task_directory}/meta.json" <<EOF
{
  "task_id": "${task_id}",
  "project_id": "ecs-office-team",
  "assigned_to": "@ecs-worker-1:matrix.test",
  "status": "assigned",
  "depends_on": [${depends_on}],
  "assigned_at": "${assigned_at}",
  "task_title": "${title}",
  "spec_path": "shared/tasks/${task_id}/spec.md",
  "result_path": "shared/tasks/${task_id}/result.md",
  "office": {"work_order_id": "${work_order_id}", "stage_id": "${stage}"}
}
EOF
}

create_task "${scope_task}" "scope" "" "Map AgentTeams task to ECS Office scope"
create_task "${extract_task}" "extract" "\"${scope_task}\"" "Extract meeting facts into minutes draft"
create_task "${verify_task}" "verify" "\"${extract_task}\"" "Verify meeting conclusions against evidence"
create_task "${dispatch_task}" "dispatch" "\"${verify_task}\"" "Dispatch approved follow-up actions"

wait_task_result() {
  local task_id="$1"
  for _ in $(seq 1 300); do
    if [[ -s "${state_directory}/shared/tasks/${task_id}/result.json" ]]; then
      return 0
    fi
    sleep 0.1
  done
  echo "AgentTeams task ${task_id} did not produce result.json" >&2
  return 1
}

# scope, extract and verify run purely through Matrix dispatch; dispatch pauses
# at the human approval gate until an admin approves it, then the testbed
# re-assigns the same task and ECS resumes it in place.
wait_task_result "${scope_task}"
wait_task_result "${extract_task}"
wait_task_result "${verify_task}"

approval_id=""
for _ in $(seq 1 300); do
  approval_id="$(curl --fail --silent "${ecs_url}/v1/office/work-orders/${work_order_id}" | python3 -c '
import json,sys
order = json.load(sys.stdin)
for task in order.get("tasks", []):
    record = task.get("approval_record") or {}
    if task.get("stage_id") == "dispatch" and record.get("id"):
        print(record["id"])
        break
')"
  if [[ -n "${approval_id}" ]]; then
    break
  fi
  sleep 0.1
done
if [[ -z "${approval_id}" ]]; then
  echo "dispatch approval did not materialize" >&2
  exit 1
fi

curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${admin_token}" \
  -d "{\"approval_id\":\"${approval_id}\",\"decision\":\"approve\",\"actor\":\"QA负责人\",\"opinion\":\"固定成功路径批准\"}" \
  "${ecs_url}/v1/admin/office/work-orders/${work_order_id}/tasks/dispatch/approval" \
  >"${work_root}/approval-decision.json"

wait_task_result "${dispatch_task}"

for _ in $(seq 1 100); do
  ready_reports="$(curl --fail --silent "${controller_url}/state" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ready_reports",0))')"
  if [[ "${ready_reports}" -ge 2 ]]; then
    break
  fi
  sleep 0.1
done

cp "${runtime_path}" "${output_directory}/controller-runtime.yaml"
for task_id in "${scope_task}" "${extract_task}" "${verify_task}" "${dispatch_task}"; do
  cp "${state_directory}/shared/tasks/${task_id}/meta.json" "${output_directory}/${task_id}-meta.json"
  cp "${state_directory}/shared/tasks/${task_id}/result.json" "${output_directory}/${task_id}-result.json"
  cp "${state_directory}/shared/tasks/${task_id}/result.md" "${output_directory}/${task_id}-result.md"
done
cp "${work_root}/ecs.log" "${output_directory}/ecs.log"
cp "${work_root}/testbed.log" "${output_directory}/testbed.log"
curl --fail --silent "${controller_url}/state" >"${output_directory}/controller-state.json"
curl --fail --silent "${ecs_url}/v1/office/work-orders/${work_order_id}" >"${output_directory}/work-order.json"
for stage in scope extract verify dispatch; do
  # resolve the per-stage task id from the fixed naming scheme
  task_id="agentteams-office-e2e-${stage}"
  execution_trace_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["execution_trace_id"])' "${state_directory}/shared/tasks/${task_id}/result.json")"
  curl --fail --silent "${ecs_url}/v1/traces/${execution_trace_id}" >"${output_directory}/execution-trace-${stage}.json"
done

source_revision="$(git -C "${repository_root}" rev-parse HEAD)"
if ! git -C "${repository_root}" diff --quiet --ignore-submodules -- || \
  ! git -C "${repository_root}" diff --cached --quiet --ignore-submodules --; then
  source_revision="${source_revision}+working-tree"
fi
SOURCE_REVISION="${source_revision}" \
UPSTREAM_REPOSITORY="${upstream_repository}" \
UPSTREAM_COMMIT="${upstream_commit}" \
python3 - "${output_directory}" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import sys

root = Path(sys.argv[1])
order = json.loads((root / "work-order.json").read_text())
controller = json.loads((root / "controller-state.json").read_text())

def spans(node):
    yield node
    for child in node.get("children", []):
        yield from spans(child)

stage_tasks = {task["stage_id"]: task for task in order["tasks"]}

def stage_result(stage):
    path = root / f"agentteams-office-e2e-{stage}-result.json"
    return json.loads(path.read_text())

def stage_trace(stage):
    path = root / f"execution-trace-{stage}.json"
    return json.loads(path.read_text())

def office_stage_spans(trace):
    return [span for span in spans(trace["root"])
            if span.get("kind") == "office_stage" and span.get("status") == "completed"]

scope_trace = stage_trace("scope")
stage_trace_ok = {
    stage: bool(stage_result(stage).get("execution_trace_id"))
    and stage_trace(stage).get("status") == "success"
    and len(office_stage_spans(stage_trace(stage))) >= 1
    for stage in ("scope", "extract", "verify", "dispatch")
}

checks = {
    "controller_projected_ecs_runtime": "runtime: ecs" in (root / "controller-runtime.yaml").read_text(),
    "worker_reported_ready_and_heartbeat": controller.get("ready_reports", 0) >= 2,
    "four_stage_tasks_assigned": controller.get("assignments_sent", 0) >= 4,
    "scope_task_succeeded": stage_result("scope").get("status") == "SUCCESS",
    "extract_task_succeeded": stage_result("extract").get("status") == "SUCCESS",
    "verify_task_succeeded": stage_result("verify").get("status") == "SUCCESS",
    "dispatch_task_succeeded": stage_result("dispatch").get("status") == "SUCCESS",
    "four_stages_completed": all(
        stage_tasks[stage].get("status") == "completed"
        for stage in ("scope", "extract", "verify", "dispatch")
    ),
    "work_order_completed": order.get("status") == "completed",
    "scope_skill_executed": any(
        run.get("name") == "meeting.scope.plan" and run.get("status") == "success"
        for run in stage_result("scope").get("skill_runs", [])
    ),
    "extract_skill_executed": any(
        run.get("name") == "meeting.fact.extract" and run.get("status") == "success"
        for run in stage_result("extract").get("skill_runs", [])
    ),
    "verify_skill_executed": any(
        run.get("name") == "meeting.evidence.verify" and run.get("status") == "success"
        for run in stage_result("verify").get("skill_runs", [])
    ),
    "dispatch_skill_executed": any(
        run.get("name") == "office.action.dispatch" and run.get("status") == "success"
        for run in stage_result("dispatch").get("skill_runs", [])
    ),
    "dispatch_receipt_verified": any(
        run.get("name") == "execution.receipt.verify" and run.get("status") == "success"
        for run in stage_result("dispatch").get("skill_runs", [])
    ),
    "external_action_receipts": bool(stage_tasks["dispatch"].get("receipts")),
    "approval_gate_recorded": (
        (stage_tasks["dispatch"].get("approval_record") or {}).get("status") == "approved"
        and (stage_tasks["dispatch"].get("approval_record") or {}).get("decided_by") == "QA负责人"
    ),
    "execution_trace_persisted": (
        bool(stage_result("scope").get("execution_trace_id"))
        and scope_trace.get("status") == "success"
        and bool(spans(scope_trace["root"]))
    ),
    "four_stage_trace_spans": all(stage_trace_ok.values()),
    "durable_attempts_and_outbox_confirmed": all(
        stage_tasks[stage].get("attempts")
        and stage_tasks[stage]["attempts"][-1].get("status") == "succeeded"
        and any(event.get("status") == "confirmed" for event in order.get("outbox", []))
        for stage in ("scope", "extract", "verify", "dispatch")
    ),
    "matrix_completion_reported": "TASK_COMPLETED" in json.dumps(controller.get("completion_report") or {}, ensure_ascii=False),
}
if not all(checks.values()):
    raise SystemExit("E2E checks failed: " + json.dumps(checks, ensure_ascii=False))
runtime_digest = hashlib.sha256((root / "controller-runtime.yaml").read_bytes()).hexdigest()
summary = {
    "schema_version": "ecs.agentteams-office-e2e/v1",
    "source_revision": os.environ["SOURCE_REVISION"],
    "agentteams_upstream": os.environ["UPSTREAM_REPOSITORY"],
    "agentteams_commit": os.environ["UPSTREAM_COMMIT"],
    "runtime_sha256": runtime_digest,
    "work_order_id": order["id"],
    "task_ids": [f"agentteams-office-e2e-{stage}" for stage in ("scope", "extract", "verify", "dispatch")],
    "execution_trace_id": stage_result("scope").get("execution_trace_id"),
    "checks": checks,
}
(root / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
PY

(
  cd "${output_directory}"
  : >SHA256SUMS
  for evidence_file in controller-runtime.yaml controller-state.json \
    ${scope_task}-meta.json ${scope_task}-result.json ${scope_task}-result.md \
    ${extract_task}-meta.json ${extract_task}-result.json ${extract_task}-result.md \
    ${verify_task}-meta.json ${verify_task}-result.json ${verify_task}-result.md \
    ${dispatch_task}-meta.json ${dispatch_task}-result.json ${dispatch_task}-result.md \
    work-order.json execution-trace-scope.json execution-trace-extract.json \
    execution-trace-verify.json execution-trace-dispatch.json summary.json; do
    shasum -a 256 "${evidence_file}" >>SHA256SUMS
  done
)

echo "AgentTeams -> ECS Worker -> Office four-stage meeting follow-up E2E passed"
echo "Evidence: ${output_directory}"
