#!/usr/bin/env bash
set -euo pipefail

readonly upstream_repository="${AGENTTEAMS_REPOSITORY:-https://github.com/agentscope-ai/AgentTeams.git}"
readonly upstream_commit="${AGENTTEAMS_COMMIT:-2540c968a642845c4b9382afd75d8c80ed861137}"
readonly script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly repository_root="$(CDPATH= cd -- "${script_directory}/.." && pwd)"
readonly patch_path="${repository_root}/integrations/agentteams/0001-add-ecs-edge-runtime.patch"
readonly temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/ecs-agentteams.XXXXXX")"
readonly upstream_checkout="${temporary_root}/AgentTeams"

cleanup() {
  rm -rf -- "${temporary_root}"
}
trap cleanup EXIT

for command_name in git go; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

echo "Fetching AgentTeams ${upstream_commit} from ${upstream_repository}"
git init --quiet "${upstream_checkout}"
git -C "${upstream_checkout}" remote add origin "${upstream_repository}"
git -C "${upstream_checkout}" fetch --quiet --depth=1 origin "${upstream_commit}"
git -C "${upstream_checkout}" checkout --quiet --detach FETCH_HEAD

actual_commit="$(git -C "${upstream_checkout}" rev-parse HEAD)"
if [[ "${actual_commit}" != "${upstream_commit}" ]]; then
  echo "unexpected AgentTeams commit: got ${actual_commit}, want ${upstream_commit}" >&2
  exit 1
fi

echo "Checking and applying ECS runtime patch"
git -C "${upstream_checkout}" apply --check "${patch_path}"
git -C "${upstream_checkout}" apply "${patch_path}"

echo "Running AgentTeams controller integration tests"
(
  cd "${upstream_checkout}/agentteams-controller"
  go test ./internal/backend ./internal/controller ./internal/service
)

echo "AgentTeams ECS runtime integration verified at ${upstream_commit}"
