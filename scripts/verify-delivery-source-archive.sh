#!/usr/bin/env bash
set -euo pipefail

readonly script_directory="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly repository_root="$(CDPATH= cd -- "${script_directory}/.." && pwd)"
readonly temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/ecs-delivery-archive.XXXXXX")"

cleanup() {
  rm -rf -- "${temporary_root}"
}
trap cleanup EXIT

git -C "${repository_root}" archive --worktree-attributes --format=tar HEAD |
  tar -xf - -C "${temporary_root}"

for forbidden_path in \
  "docs/reviews" \
  "docs/ECS-路演提纲与答辩备忘.txt" \
  "docs/ECS-Agent-Infra-可信会议督办闭环-路演备注版.pptx"; do
  if [[ -e "${temporary_root}/${forbidden_path}" ]]; then
    echo "internal-only material is present in the delivery archive: ${forbidden_path}" >&2
    exit 1
  fi
done

for required_path in \
  "README.md" \
  "LICENSE" \
  "NOTICE" \
  "THIRD_PARTY_NOTICES.md" \
  "third_party/paho-edl-v1.0.txt" \
  "third_party/source-han-sans-OFL-1.1.txt" \
  "docs/configuration-reference.md" \
  "docs/ECS-Agent-Infra-初赛方案PPT-套模板版.pdf" \
  "docs/ECS-Agent-Infra-初赛方案PPT-套模板版.pptx" \
  "docs/ECS-Agent-Infra-可信会议督办闭环.pptx" \
  "docs/GOAI-作品简介.md" \
  "docs/delivery-demo-index.md" \
  "docs/demo-video/ecs-trusted-meeting-demo.mp4" \
  "docs/demo-assets/multi-platform-hosts.png" \
  "docs/demo-assets/office-control-console.png" \
  "docs/demo-assets/approval-and-handoff.png" \
  "docs/demo-assets/evidence-timeline.png" \
  "docs/demo-assets/cross-node-trace.png" \
  "docs/manual-multiplatform-package.md" \
  "integrations/agentteams/README.md" \
  "evidence/agentteams-office/reference/summary.json" \
  "evidence/qwen3-1.7b-live/reference/summary.json" \
  "evidence/qwen3-0.6b-live/reference/summary.json"; do
  if [[ ! -e "${temporary_root}/${required_path}" ]]; then
    # Before a new file is committed, git archive cannot include it yet. Still
    # verify that the working-tree file exists and is not marked export-ignore;
    # CI repeats this check after commit against the real archive.
    export_attribute="$(git -C "${repository_root}" check-attr export-ignore -- "${required_path}" | awk '{print $3}')"
    if [[ -e "${repository_root}/${required_path}" && "${export_attribute}" != "set" ]]; then
      continue
    fi
    echo "required delivery material is missing from the archive: ${required_path}" >&2
    exit 1
  fi
done

echo "Delivery source archive contains delivery-facing materials and excludes internal-only files."
