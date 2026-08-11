#!/usr/bin/env bash
set -euo pipefail

repository_root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
notice="${repository_root}/THIRD_PARTY_NOTICES.md"

missing=0
while read -r module _version; do
  if ! grep -F "\`${module}\`" "${notice}" | grep -Fq "${_version}"; then
    echo "THIRD_PARTY_NOTICES.md is missing Go module/version: ${module} ${_version}" >&2
    missing=1
  fi
done < <(cd "${repository_root}" && go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all | sed '/^$/d')

for required in \
  'Eclipse Distribution License v1.0' \
  'Android Gradle Plugin 9.2.0' \
  '@ohos/hypium 1.0.25' \
  '@ohos/hamock 1.0.0' \
  'Source Han Sans CN 2.005R'; do
  if ! grep -Fq "${required}" "${notice}"; then
    echo "THIRD_PARTY_NOTICES.md is missing platform dependency: ${required}" >&2
    missing=1
  fi
done

test -s "${repository_root}/third_party/paho-edl-v1.0.txt"
test -s "${repository_root}/third_party/paho-NOTICE.md"
test -s "${repository_root}/third_party/source-han-sans-OFL-1.1.txt"
if [[ "${missing}" -ne 0 ]]; then
  exit 1
fi
echo "Third-party notices cover the current Go module graph and platform dependency declarations."
