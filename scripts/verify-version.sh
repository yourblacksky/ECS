#!/usr/bin/env bash
set -euo pipefail

repository_root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' <"${repository_root}/version/VERSION")"
if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][A-Za-z0-9.-]+)?$ ]]; then
  echo "version/VERSION is not semantic version: ${version}" >&2
  exit 1
fi

while IFS= read -r constraint; do
  if [[ "${constraint}" != ">=${version}" ]]; then
    echo "Skill ECS compatibility ${constraint} does not match >=${version}" >&2
    exit 1
  fi
done < <(find "${repository_root}/skills.d" -mindepth 2 -maxdepth 2 -name skill.json -print0 |
  xargs -0 jq -r '.compatibility.ecs' | sort -u)

if [[ -n "${GITHUB_REF_NAME:-}" && "${GITHUB_REF_TYPE:-}" == "tag" && \
  "${GITHUB_REF_NAME}" != "v${version}" ]]; then
  echo "tag ${GITHUB_REF_NAME} does not match version v${version}" >&2
  exit 1
fi

echo "ECS version contract verified: v${version}"
