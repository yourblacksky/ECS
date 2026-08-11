#!/usr/bin/env bash
set -euo pipefail

export LC_ALL=C
export LANG=C

readonly script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly repository_root="$(CDPATH= cd -- "${script_directory}/.." && pwd)"
readonly model="${ECS_SMALL_MODEL:-${ECS_QWEN17_MODEL:-qwen3:1.7b}}"
readonly ollama_url="${ECS_OLLAMA_URL:-http://127.0.0.1:11434}"
readonly controller_port="${ECS_SMALL_CONTROLLER_PORT:-${ECS_QWEN17_CONTROLLER_PORT:-18417}}"
readonly worker_port="${ECS_SMALL_WORKER_PORT:-${ECS_QWEN17_WORKER_PORT:-18418}}"
readonly evaluation_id="${ECS_SMALL_EVAL_ID:-qwen17}"
readonly allow_failure="${ECS_SMALL_ALLOW_FAILURE:-false}"
readonly controller_url="http://127.0.0.1:${controller_port}"
readonly worker_url="http://127.0.0.1:${worker_port}"
readonly controller_token="${evaluation_id}-eval-controller-token"
readonly controller_admin_token="${evaluation_id}-eval-controller-admin"
readonly worker_token="${evaluation_id}-eval-worker-token"
readonly worker_admin_token="${evaluation_id}-eval-worker-admin"
readonly cases_file="${repository_root}/testdata/qwen3-1.7b-live-cases.json"
cd "${repository_root}"

output_directory="${repository_root}/data/evidence/qwen3-1.7b-live"
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

for command in curl jq go ollama; do
  command -v "${command}" >/dev/null || {
    echo "required command is missing: ${command}" >&2
    exit 1
  }
done
if [[ -e "${output_directory}" && -n "$(find "${output_directory}" -type f -print -quit)" ]]; then
  echo "output directory is not empty: ${output_directory}" >&2
  exit 1
fi

readonly work_root="$(mktemp -d "${TMPDIR:-/tmp}/ecs-${evaluation_id}-live.XXXXXX")"
controller_pid=""
worker_pid=""
ollama_pid=""

stop_ecs_nodes() {
  for role in controller worker; do
    local pid=""
    if [[ "${role}" == "controller" ]]; then
      pid="${controller_pid}"
    else
      pid="${worker_pid}"
    fi
    if [[ -n "${pid}" ]]; then
      kill -TERM "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  done
  controller_pid=""
  worker_pid=""
}

cleanup() {
  stop_ecs_nodes
  if [[ -n "${ollama_pid}" ]]; then
    kill -TERM "${ollama_pid}" 2>/dev/null || true
    wait "${ollama_pid}" 2>/dev/null || true
  fi
  rm -rf -- "${work_root}"
}
trap cleanup EXIT

mkdir -p "${output_directory}/raw/replies" "${output_directory}/traces/controller" \
  "${output_directory}/traces/worker" "${work_root}/controller" "${work_root}/worker"

if ! curl --fail --silent "${ollama_url}/api/tags" >/dev/null 2>&1; then
  OLLAMA_HOST="${ollama_url#http://}" ollama serve >"${output_directory}/ollama.log" 2>&1 &
  ollama_pid=$!
fi

wait_for_url() {
  local url="$1"
  for _ in $(seq 1 200); do
    if curl --fail --silent "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "timed out waiting for ${url}" >&2
  return 1
}

wait_for_url "${ollama_url}/api/tags"
if ! curl --fail --silent "${ollama_url}/api/tags" | jq -e --arg model "${model}" \
  '.models[] | select(.name == $model or .model == $model)' >/dev/null; then
  echo "Ollama model is not installed: ${model}; run: ollama pull ${model}" >&2
  exit 1
fi

go build -trimpath -o "${work_root}/ecs" "${repository_root}/cmd/ecs"

write_config() {
  local role="$1"
  local port="$2"
  local token="$3"
  local admin_token="$4"
  local target="${work_root}/${role}/config.json"
  local groups='{}'
  if [[ "${role}" == "controller" ]]; then
    groups="$(jq -cn --arg name "${evaluation_id}-worker" --arg url "${worker_url}" --arg token "${worker_token}" \
      '{office:[{name:$name,url:$url,token:$token}]}')"
  fi
  jq -n \
    --arg node "${evaluation_id}-${role}" --arg listen "127.0.0.1:${port}" \
    --arg token "${token}" --arg admin_token "${admin_token}" \
    --arg base_url "${ollama_url}/v1" --arg model "${model}" \
    --arg root "${repository_root}" --arg state "${work_root}/${role}" \
    --argjson groups "${groups}" \
    '{
      node_name:$node,listen:$listen,auth_token:$token,admin_token:$admin_token,log_level:"info",
      llm:{base_url:$base_url,api_key:"",model:$model,timeout:"90s",native_tools:false,reasoning_effort:"none"},
      agent:{system_prompt_file:($root+"/prompts/system.small.zh.txt"),conversation_guidance_file:($root+"/prompts/conversation.default.zh.txt"),conversation_guidance:true,max_system_prompt_chars:8000,max_agent_context_chars:2200,max_conversation_guide_chars:900,max_tool_catalog_chars:3200,max_tool_rounds:3,max_prompt_tools:12,approval_mode:"high_risk",group_synthesis:true,group_dispatch_mode:"single",group_race_fanout:2},
      agent_roles:{directory:($root+"/agents.d"),pipeline_directory:($root+"/pipelines.d"),default_pipeline:"default"},
      agentteams:{enabled:false,controller_url:"",worker_name:"ecs-worker-1",runtime_config_file:"",task_directory:($state+"/agentteams"),max_task_prompt_chars:3000,heartbeat_interval:"30s",matrix_url:"",matrix_token_env:"AGENTTEAMS_WORKER_MATRIX_TOKEN",matrix_sync_timeout:"30s",matrix_since_file:($state+"/matrix-since")},
      office:{directory:($root+"/office.d"),order_directory:($state+"/orders"),max_stages:16,max_context_entries:64,approval_ttl:"15m",attempt_lease:"2m",reaper_interval:"30s"},
      tools:{directory:($root+"/tools.d"),default_timeout:"10s",max_output_bytes:65536},
      skills:{directory:($root+"/skills.d"),knowledge_directory:($root+"/skills.d/_knowledge"),max_prompt_skills:5,max_steps:4,documentation_required:true,grounding_enabled:true,max_document_chars:12000,max_grounding_chars:1800,max_knowledge_documents:3},
      trace:{enabled:true,directory:($state+"/traces"),max_value_chars:12000,max_traces:1000,retention:"168h"},
      admin:{enabled:true,log_capacity:1000},
      sessions:{directory:($state+"/sessions"),max_messages:0,context_messages:20,max_context_chars:4000},
      discovery:{enabled:false,service_type:"_ecs._tcp",advertise_url:("http://"+$listen),groups:["eval"],interval:"5s",expiry:"20s",registry:{enabled:false,serve:false,controller_urls:[],token_env:"ECS_DISCOVERY_REGISTRY_TOKEN",heartbeat_interval:"15s",lease_timeout:"45s",request_timeout:"5s"}},
      nested:{enabled:false,default_max_depth:8,discovery_groups:[],children:[]},
      network_auth:{enabled:false,directory:($state+"/network"),route_overrides:{},parent:{url:"",node_id:"",auto_discover:false,discovery_groups:[],pairing_code_env:"ECS_PARENT_PAIRING_CODE"},heartbeat_interval:"30s",lease_timeout:"90s",enrollment_ttl:"10m"},
      iot:{max_response_bytes:65536,http:{},mqtt:{}},
      http:{read_timeout:"15s",write_timeout:"180s",max_body_bytes:1048576,max_concurrent:16,peer_timeout:"180s",peer_retries:1,retry_backoff:"500ms"},
      groups:$groups
    }' >"${target}"
}

write_config worker "${worker_port}" "${worker_token}" "${worker_admin_token}"
write_config controller "${controller_port}" "${controller_token}" "${controller_admin_token}"

"${work_root}/ecs" -config "${work_root}/worker/config.json" >"${output_directory}/worker.log" 2>&1 &
worker_pid=$!
"${work_root}/ecs" -config "${work_root}/controller/config.json" >"${output_directory}/controller.log" 2>&1 &
controller_pid=$!
wait_for_url "${worker_url}/healthz"
wait_for_url "${controller_url}/healthz"

curl --fail --silent "${ollama_url}/api/show" -d "$(jq -cn --arg model "${model}" '{model:$model}')" |
  jq '{license,details,model_info,parameters}' >"${output_directory}/model.json"
curl --fail --silent "${ollama_url}/api/ps" | jq . >"${output_directory}/ollama-process.json"

results_file="${work_root}/case-results.ndjson"
while IFS= read -r case_json; do
  case_id="$(jq -r .id <<<"${case_json}")"
  prompt="$(jq -r .prompt <<<"${case_json}")"
  assertion="$(jq -r .assertion <<<"${case_json}")"
  request_file="${output_directory}/raw/replies/${case_id}.request.json"
  response_file="${output_directory}/raw/replies/${case_id}.response.json"
  jq -n --arg request_id "${evaluation_id}-${case_id}" --arg session_id "${evaluation_id}-${case_id}" \
    --arg prompt "${prompt}" '{request_id:$request_id,session_id:$session_id,prompt:$prompt}' >"${request_file}"
  curl_metrics="$(curl --silent --show-error --max-time 180 -w '%{http_code} %{time_total}' \
    -o "${response_file}" -H "Authorization: Bearer ${controller_token}" \
    -H 'Content-Type: application/json' --data-binary "@${request_file}" "${controller_url}/v1/reply")"
  http_status="${curl_metrics%% *}"
  duration="${curl_metrics##* }"
  passed=false
  if [[ "${http_status}" == 2* ]]; then case "${assertion}" in
    accepted_text)
      if jq -e '.verification.status == "accepted" and (.message.content | length > 0)' "${response_file}" >/dev/null; then passed=true; fi
      ;;
    skill)
      expected_skill="$(jq -r .expected_skill <<<"${case_json}")"
      if jq -e --arg skill "${expected_skill}" '.verification.status == "accepted" and any(.skill_runs[]?; .name == $skill and .status == "success")' "${response_file}" >/dev/null; then passed=true; fi
      ;;
    skill_contains)
      expected_skill="$(jq -r .expected_skill <<<"${case_json}")"
      expected_text="$(jq -r .expected_text <<<"${case_json}")"
      if jq -e --arg skill "${expected_skill}" --arg text "${expected_text}" '.verification.status == "accepted" and (.message.content | contains($text)) and any(.skill_runs[]?; .name == $skill and .status == "success")' "${response_file}" >/dev/null; then passed=true; fi
      ;;
    manifest)
      if jq -e '.verification.status == "accepted" and (.message.content | contains("已注册")) and (.message.content | contains("当前可调用"))' "${response_file}" >/dev/null; then passed=true; fi
      ;;
  esac; fi
  jq -cn --arg id "${case_id}" --arg prompt "${prompt}" --arg assertion "${assertion}" \
    --argjson passed "${passed}" --argjson http_status "${http_status}" --argjson duration_seconds "${duration}" \
    '{id:$id,prompt:$prompt,assertion:$assertion,passed:$passed,http_status:$http_status,duration_seconds:$duration_seconds,response:("raw/replies/"+$id+".response.json")}' >>"${results_file}"
done < <(jq -c '.[]' "${cases_file}")

meeting_request="${output_directory}/raw/meeting-create.request.json"
jq -n --arg requested_by "${evaluation_id}-live-eval" '{requested_by:$requested_by,input:{meeting_title:"ECS 竞赛交付会",source:"决定：本周完成可信会议督办演示\n行动：整理五分钟演示清单 | owner=张三 | due=2026-08-08\n行动：核对回执证据 | owner=李四 | due=2026-08-09",participants:["张三","李四","王经理"],timezone:"Asia/Shanghai"}}' >"${meeting_request}"
curl --fail --silent --show-error -H "Authorization: Bearer ${controller_token}" \
  -H 'Content-Type: application/json' --data-binary "@${meeting_request}" \
  "${controller_url}/v1/office/components/meeting_followup/work-orders" \
  >"${output_directory}/raw/meeting-created.json"
order_id="$(jq -r .id "${output_directory}/raw/meeting-created.json")"

dispatch_body="$(jq -cn --arg node "${evaluation_id}-worker" '{group:"office",node:$node}')"
first_duration="$(curl --fail --silent --show-error --max-time 240 -w '%{time_total}' \
  -o "${output_directory}/raw/meeting-before-approval.json" \
  -H "Authorization: Bearer ${controller_token}" -H 'Content-Type: application/json' \
  --data-binary "${dispatch_body}" "${controller_url}/v1/office/work-orders/${order_id}/dispatch")"
approval_observed=false
second_duration=0
if [[ "$(jq -r .status "${output_directory}/raw/meeting-before-approval.json")" == "pending_approval" ]]; then
  approval_observed=true
  approval_id="$(jq -r '.tasks[]|select(.stage_id=="dispatch")|.approval_record.id' "${output_directory}/raw/meeting-before-approval.json")"
  jq -n --arg approval_id "${approval_id}" --arg actor "${evaluation_id}-live-eval" \
    '{approval_id:$approval_id,decision:"approve",actor:$actor,opinion:"synthetic regression data approved"}' \
    >"${output_directory}/raw/meeting-approval.request.json"
  curl --fail --silent --show-error -H "Authorization: Bearer ${controller_admin_token}" \
    -H 'Content-Type: application/json' --data-binary "@${output_directory}/raw/meeting-approval.request.json" \
    "${controller_url}/v1/admin/office/work-orders/${order_id}/tasks/dispatch/approval" \
    >"${output_directory}/raw/meeting-approved.json"
  second_duration="$(curl --fail --silent --show-error --max-time 240 -w '%{time_total}' \
    -o "${output_directory}/raw/meeting-completed.json" \
    -H "Authorization: Bearer ${controller_token}" -H 'Content-Type: application/json' \
    --data-binary "${dispatch_body}" "${controller_url}/v1/office/work-orders/${order_id}/dispatch")"
else
  cp "${output_directory}/raw/meeting-before-approval.json" "${output_directory}/raw/meeting-completed.json"
fi

meeting_passed=false
if jq -e '.status == "completed" and ([.tasks[].status] | all(. == "completed")) and ([.tasks[]|select(.stage_id=="dispatch")|.receipts|length][0] > 0) and ([.tasks[]|select(.stage_id=="dispatch")|.receipt_verification.status][0] == "verified")' \
  "${output_directory}/raw/meeting-completed.json" >/dev/null; then
  meeting_passed=true
fi

while IFS=$'\t' read -r role trace_id; do
  source_file="${work_root}/${role}/traces/${trace_id}.json"
  if [[ -f "${source_file}" ]]; then
    cp "${source_file}" "${output_directory}/traces/${role}/${trace_id}.json"
  fi
done < <(jq -r '.tasks[].attempts[]? | "worker\t" + .trace_id' "${output_directory}/raw/meeting-completed.json")
find "${work_root}/controller/traces" -type f -name '*.json' -exec cp {} "${output_directory}/traces/controller/" \;

worktree_dirty=false
if [[ -n "$(git -C "${repository_root}" status --porcelain)" ]]; then
  worktree_dirty=true
fi
jq -n --slurpfile cases "${results_file}" --slurpfile meeting "${output_directory}/raw/meeting-completed.json" \
  --arg model "${model}" --arg base_commit "$(git -C "${repository_root}" rev-parse HEAD)" --argjson worktree_dirty "${worktree_dirty}" \
  --arg order_id "${order_id}" --argjson meeting_passed "${meeting_passed}" \
  --argjson approval_observed "${approval_observed}" --argjson before_approval_seconds "${first_duration}" --argjson after_approval_seconds "${second_duration}" '
  {
    schema_version:"ecs.small-model-eval/v1",generated_at:(now|todate),model:$model,reasoning_effort:"none",native_tools:false,base_commit:$base_commit,worktree_dirty:$worktree_dirty,
    fixed_cases:{passed:($cases|map(select(.passed))|length),total:($cases|length),all_passed:($cases|all(.passed)),cases:$cases},
    meeting_flow:{passed:$meeting_passed,order_id:$order_id,before_approval_seconds:$before_approval_seconds,after_approval_seconds:$after_approval_seconds,final_status:$meeting[0].status,approval_gate_observed:$approval_observed,receipt_count:(([$meeting[0].tasks[]|select(.stage_id=="dispatch")|.receipts|length][0])//0),receipt_verification:([$meeting[0].tasks[]|select(.stage_id=="dispatch")|.receipt_verification.status][0]),failure_stage:([$meeting[0].tasks[]|select(.status != "completed" and .status != "pending")|.stage_id][0]),failure_error:([$meeting[0].tasks[]|select(.error != null and .error != "")|.error][0])}
  }' >"${output_directory}/summary.json"

cp "${cases_file}" "${output_directory}/cases.json"
stop_ecs_nodes
(
  cd "${output_directory}"
  find . -type f ! -name SHA256SUMS -print | LC_ALL=C sort | xargs shasum -a 256 >SHA256SUMS
)

if ! jq -e '.fixed_cases.all_passed and .meeting_flow.passed' "${output_directory}/summary.json" >/dev/null; then
  echo "${model} live evaluation found failures; see ${output_directory}/summary.json" >&2
  if [[ "${allow_failure}" == "true" ]]; then
    exit 0
  fi
  exit 1
fi
echo "${model} live evaluation passed: ${output_directory}"
