#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
harmony_root="${project_root}/harmonyos"
bridge_root="${harmony_root}/bridge"
generated_root="${bridge_root}/generated/runtime"
native_sdk="${HARMONY_NATIVE_SDK:-/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony/native}"
ohos_cc="${native_sdk}/llvm/bin/aarch64-unknown-linux-ohos-clang"
ohos_go_root="${OHOS_GO_ROOT:-${project_root}/.cache/ohos-go1.25.12}"
ohos_go="${ohos_go_root}/bin/go"
ohos_go_repository="https://github.com/yourblacksky/ohos_golang_go.git"
ohos_go_revision="e7463379a44ed8573623117d4325dd95d0e2cd01"
ohos_go_version="go1.25.12"
toolchain_patch="${harmony_root}/toolchain/openharmony-go-cshared-argv.patch"

if [[ ! -x "${ohos_cc}" ]]; then
  echo "HarmonyOS ARM64 compiler not found: ${ohos_cc}" >&2
  exit 1
fi

rebuild_toolchain=false
if [[ -z "${OHOS_GO_ROOT:-}" ]]; then
  mkdir -p "$(dirname "${ohos_go_root}")"
  if [[ ! -d "${ohos_go_root}/.git" ]]; then
    git init "${ohos_go_root}"
    git -C "${ohos_go_root}" remote add origin "${ohos_go_repository}"
  fi
  if ! git -C "${ohos_go_root}" cat-file -e "${ohos_go_revision}^{commit}" 2>/dev/null; then
    git -C "${ohos_go_root}" fetch --depth=1 origin "${ohos_go_revision}"
  fi
  if [[ "$(git -C "${ohos_go_root}" rev-parse HEAD 2>/dev/null || true)" != "${ohos_go_revision}" ]]; then
    if [[ -n "$(git -C "${ohos_go_root}" status --porcelain 2>/dev/null || true)" ]]; then
      echo "Managed OpenHarmony Go cache has local changes: ${ohos_go_root}" >&2
      echo "Remove that cache or set OHOS_GO_ROOT to an intentional custom toolchain." >&2
      exit 1
    fi
    git -C "${ohos_go_root}" switch --detach "${ohos_go_revision}"
    rebuild_toolchain=true
  fi
fi

# The pinned fork inherits argc/argv from the OpenHarmony dynamic loader when a
# c-shared library is loaded. Those registers are not process arguments, and Go
# 1.25 dereferences them while reading GODEBUG during schedinit. Apply the small,
# pinned Android-style argv shim before building so managed caches are safe and
# reproducible. Custom toolchains must already contain the equivalent fix.
if git -C "${ohos_go_root}" apply --reverse --check "${toolchain_patch}" >/dev/null 2>&1; then
  : # Already patched.
elif [[ -z "${OHOS_GO_ROOT:-}" ]] && git -C "${ohos_go_root}" apply --check "${toolchain_patch}"; then
  git -C "${ohos_go_root}" apply "${toolchain_patch}"
else
  echo "OpenHarmony Go toolchain is missing the c-shared argv fix: ${toolchain_patch}" >&2
  echo "Use the managed toolchain or apply the equivalent fix to OHOS_GO_ROOT." >&2
  exit 1
fi

if [[ ! -x "${ohos_go}" ]]; then
  rebuild_toolchain=true
fi

if [[ "${rebuild_toolchain}" == true ]]; then
  (
    cd "${ohos_go_root}/src"
    GOTOOLCHAIN=local ./make.bash
  )
fi

actual_go_version="$(GOROOT="${ohos_go_root}" GOTOOLCHAIN=local "${ohos_go}" env GOVERSION)"
if [[ "${actual_go_version}" != "${ohos_go_version}" ]]; then
  echo "OpenHarmony Go must be ${ohos_go_version}; got ${actual_go_version}." >&2
  exit 1
fi
if ! GOROOT="${ohos_go_root}" GOTOOLCHAIN=local GOOS=openharmony GOARCH=arm64 \
  "${ohos_go}" env GOOS >/dev/null 2>&1; then
  echo "Go toolchain does not provide the openharmony/arm64 target: ${ohos_go_root}" >&2
  exit 1
fi

rm -rf "${generated_root}"
mkdir -p "${generated_root}"
cp "${harmony_root}/runtime/config.json" "${generated_root}/config.json"
for asset_dir in prompts tools.d skills.d agents.d pipelines.d office.d; do
  cp -R "${project_root}/${asset_dir}" "${generated_root}/${asset_dir}"
done

output_dir="${harmony_root}/entry/src/main/libs/arm64-v8a"
header_dir="${harmony_root}/entry/src/main/cpp/include"
mkdir -p "${output_dir}" "${header_dir}"

(
  cd "${project_root}"
  GOROOT="${ohos_go_root}" \
  GOTOOLCHAIN=local \
  CGO_ENABLED=1 \
  GOOS=openharmony \
  GOARCH=arm64 \
  CC="${ohos_cc}" \
  CGO_LDFLAGS="-Wl,-soname,libecs_go.so" \
    "${ohos_go}" build \
      -buildmode=c-shared \
      -trimpath \
      -o "${output_dir}/libecs_go.so" \
      ./harmonyos/bridge
)

mv "${output_dir}/libecs_go.h" "${header_dir}/ecs_go.h"
echo "Built HarmonyOS ECS runtime: ${output_dir}/libecs_go.so"
