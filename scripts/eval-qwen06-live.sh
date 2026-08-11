#!/usr/bin/env bash
set -euo pipefail

readonly script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export ECS_SMALL_MODEL="${ECS_QWEN06_MODEL:-qwen3:0.6b}"
export ECS_SMALL_CONTROLLER_PORT="${ECS_QWEN06_CONTROLLER_PORT:-18517}"
export ECS_SMALL_WORKER_PORT="${ECS_QWEN06_WORKER_PORT:-18518}"
export ECS_SMALL_EVAL_ID="qwen06"
export ECS_SMALL_ALLOW_FAILURE="true"

exec "${script_directory}/eval-qwen17-live.sh" "$@"
