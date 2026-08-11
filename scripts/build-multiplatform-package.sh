#!/usr/bin/env bash
set -euo pipefail

# Keep archive and checksum tools independent of an optional UTF-8 locale on
# the self-hosted release runner.
export LC_ALL=C
export LANG=C

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
deveco_root="${DEVECO_STUDIO_HOME:-/Applications/DevEco-Studio.app/Contents}"
android_sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"

if [[ -z "$android_sdk" && -d "$HOME/Library/Android/sdk" ]]; then
  android_sdk="$HOME/Library/Android/sdk"
fi
if [[ -z "$android_sdk" && -d "/opt/homebrew/share/android-commandlinetools" ]]; then
  android_sdk="/opt/homebrew/share/android-commandlinetools"
fi
if [[ -z "$android_sdk" && -d "/usr/local/share/android-commandlinetools" ]]; then
  android_sdk="/usr/local/share/android-commandlinetools"
fi

export ANDROID_HOME="$android_sdk"
export ANDROID_SDK_ROOT="$android_sdk"
export DEVECO_SDK_HOME="${DEVECO_SDK_HOME:-$deveco_root/sdk}"
export HARMONY_NATIVE_SDK="${HARMONY_NATIVE_SDK:-$deveco_root/sdk/default/openharmony/native}"
export PATH="$deveco_root/tools/node/bin:$deveco_root/tools/ohpm/bin:$deveco_root/tools/hvigor/bin:$PATH"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command is unavailable: $1" >&2
    exit 1
  fi
}

check_environment() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "The full package requires a macOS runner." >&2
    exit 1
  fi
  for command_name in git go java xcodebuild xcodegen codesign openssl ditto; do
    require_command "$command_name"
  done
  if [[ -z "${JAVA_HOME:-}" || ! -x "$JAVA_HOME/bin/jlink" ]]; then
    echo "A complete JDK 17 with jlink is required; set JAVA_HOME accordingly." >&2
    exit 1
  fi
  if [[ ! -d "$android_sdk" ]]; then
    echo "Android SDK not found; set ANDROID_HOME or ANDROID_SDK_ROOT." >&2
    exit 1
  fi
  if [[ "$(xcode-select -p 2>/dev/null || true)" != */Xcode.app/Contents/Developer ]]; then
    echo "Full Xcode must be selected with xcode-select." >&2
    exit 1
  fi
  if [[ ! -x "$deveco_root/tools/ohpm/bin/ohpm" ]]; then
    echo "DevEco ohpm not found under $deveco_root." >&2
    exit 1
  fi
  if [[ ! -x "$deveco_root/tools/hvigor/bin/hvigorw" ]]; then
    echo "DevEco hvigorw not found under $deveco_root." >&2
    exit 1
  fi
  if [[ ! -x "$HARMONY_NATIVE_SDK/llvm/bin/aarch64-unknown-linux-ohos-clang" ]]; then
    echo "HarmonyOS native ARM64 compiler not found under $HARMONY_NATIVE_SDK." >&2
    exit 1
  fi
}

output_zip=""
case "${1:-}" in
  --check)
    check_environment
    echo "Multiplatform release runner is ready."
    exit 0
    ;;
  --output)
    output_zip="${2:-}"
    ;;
  *)
    echo "Usage: $0 --check | --output /absolute/path/ECS-package.zip" >&2
    exit 2
    ;;
esac

check_environment
if [[ "$output_zip" != /* || "$output_zip" != *.zip ]]; then
  echo "--output must be an absolute .zip path." >&2
  exit 2
fi
if [[ -e "$output_zip" ]]; then
  echo "Refusing to overwrite existing package: $output_zip" >&2
  exit 2
fi
if [[ -n "$(git -C "$repo_root" status --porcelain --untracked-files=all)" ]]; then
  echo "Refusing to package a dirty checkout; commit or remove local changes first." >&2
  exit 2
fi

"$repo_root/scripts/verify-delivery-source-archive.sh"

mkdir -p "$(dirname "$output_zip")"
work_root="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ecs-package.XXXXXX")"
trap 'rm -rf -- "$work_root"' EXIT
bundle_root="$work_root/ECS-package"
build_root="$bundle_root/builds"
demo_root="$bundle_root/demo"
evidence_root="$bundle_root/evidence/meeting-followup"
agentteams_evidence_root="$bundle_root/evidence/agentteams-office"
mkdir -p "$build_root/android" "$build_root/ios" "$build_root/harmonyos" \
  "$build_root/windows/amd64" "$build_root/windows/arm64" "$build_root/windows/runtime" \
  "$build_root/linux/armv6" "$build_root/linux/arm64" "$build_root/linux/runtime" \
  "$demo_root/images" "$demo_root/reports" "$evidence_root" "$agentteams_evidence_root"

cd "$repo_root"
make test
./scripts/demo-meeting-followup.sh -out "$evidence_root"
./scripts/demo-agentteams-office-e2e.sh --out "$agentteams_evidence_root"
python3 scripts/verify-agentteams-package-evidence.py \
  "$agentteams_evidence_root" "$(git rev-parse HEAD)"
(cd "$agentteams_evidence_root" && shasum -a 256 -c SHA256SUMS)

cp docs/delivery-demo-index.md "$demo_root/README.md"
cp docs/ECS-Agent-Infra-初赛方案PPT-套模板版.pptx "$demo_root/"
cp docs/ECS-Agent-Infra-初赛方案PPT-套模板版.pdf "$demo_root/"
cp docs/demo-video/ecs-trusted-meeting-demo.mp4 "$demo_root/"
cp docs/GOAI-作品简介.md "$demo_root/"
cp docs/demo-assets/README.md "$demo_root/images/README.md"
cp docs/demo-assets/*.png "$demo_root/images/"
cp docs/qwen3-0.6b-evaluation-report.md "$demo_root/reports/"
cp docs/meeting-followup-demo.md "$demo_root/reports/"
cp docs/agentteams-office-e2e.md "$demo_root/reports/"
cp -R evidence/qwen3-1.7b-live "$bundle_root/evidence/"
cp -R evidence/qwen3-0.6b-live "$bundle_root/evidence/"

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o "$build_root/windows/amd64/ecs.exe" ./cmd/ecs
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o "$build_root/windows/arm64/ecs.exe" ./cmd/ecs
cp config.example.json "$build_root/windows/runtime/config.example.json"
for asset_dir in prompts tools.d skills.d agents.d pipelines.d office.d; do
  cp -R "$asset_dir" "$build_root/windows/runtime/$asset_dir"
done

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -trimpath -ldflags='-s -w' \
  -o "$build_root/linux/armv6/ecs" ./cmd/ecs
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o "$build_root/linux/arm64/ecs" ./cmd/ecs
cp config.example.json "$build_root/linux/runtime/config.example.json"
for asset_dir in prompts tools.d skills.d agents.d pipelines.d office.d; do
  cp -R "$asset_dir" "$build_root/linux/runtime/$asset_dir"
done

(
  cd android
  ./gradlew --no-daemon assembleDebug
)
find android/app/build/outputs/apk/debug -maxdepth 1 -type f -name '*.apk' \
  -exec cp {} "$build_root/android/" \;
if ! find "$build_root/android" -maxdepth 1 -type f -name '*.apk' | grep -q .; then
  echo "Android build completed without producing an APK." >&2
  exit 1
fi
cp android/app/libs/ecs-mobile.aar "$build_root/android/"

make mobile-xcframework
make ios-project
xcodebuild \
  -project ios/ECSApp.xcodeproj \
  -scheme ECSApp \
  -configuration Release \
  -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath "$work_root/ios-derived" \
  build
ios_simulator_app="$work_root/ios-derived/Build/Products/Release-iphonesimulator/ECS.app"
codesign --verify --deep --strict "$ios_simulator_app"
cp -R "$ios_simulator_app" \
  "$build_root/ios/ECS-simulator.app"
cp -R ios/Frameworks/Mobile.xcframework "$build_root/ios/"

(
  cd harmonyos
  "$deveco_root/tools/ohpm/bin/ohpm" install --all
)
./scripts/build-harmony-runtime.sh
(
  cd harmonyos
  "$deveco_root/tools/hvigor/bin/hvigorw" --mode module \
    -p product=default \
    -p module=entry@default \
    -p buildMode=debug \
    assembleHap
)
find harmonyos/entry/build/default/outputs/default -maxdepth 1 -type f -name '*.hap' \
  -exec cp {} "$build_root/harmonyos/" \;
if ! find "$build_root/harmonyos" -maxdepth 1 -type f -name '*.hap' | grep -q .; then
  echo "HarmonyOS build completed without producing a HAP." >&2
  exit 1
fi
cp harmonyos/entry/src/main/libs/arm64-v8a/libecs_go.so "$build_root/harmonyos/"

git archive --worktree-attributes --format=tar --prefix=ECS-source/ HEAD | tar -xf - -C "$bundle_root"

commit="$(git rev-parse HEAD)"
ref="${GITHUB_REF_NAME:-$(git branch --show-current)}"
timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
cat > "$bundle_root/BUILD-MANIFEST.txt" <<EOF
ECS curated multi-platform package
Commit: $commit
Ref: $ref
Built at: $timestamp

Contents:
- ECS-source/: tracked repository snapshot; internal reviews and speaker-preparation files excluded
- demo/: formal PPT, 58-second video, standalone runtime images, entry guide and evaluation reports
- builds/android/: debug APKs and ecs-mobile.aar
- builds/ios/: locally signed iOS Simulator app and Mobile.xcframework
- builds/harmonyos/: unsigned debug HAP and ARM64 Go runtime
- builds/windows/: amd64 and arm64 executables plus runtime assets
- builds/linux/: ARMv6 and ARM64 executables plus shared runtime assets
- evidence/meeting-followup/: success/failure run evidence, durable WorkOrders, summary and checksums
- evidence/agentteams-office/: same-commit AgentTeams Controller to four ECS Office stages evidence
- evidence/qwen3-1.7b-live/ and qwen3-0.6b-live/: real small-model dual-node evidence

This archive is governed by ECS-source/LICENSE. Copyright and third-party
attribution are recorded in ECS-source/NOTICE and ECS-source/THIRD_PARTY_NOTICES.md.
EOF

(
  cd "$bundle_root"
  : > SHA256SUMS
  while IFS= read -r -d '' artifact; do
    digest="$(openssl dgst -sha256 "$artifact" | awk '{print $NF}')"
    printf '%s  %s\n' "$digest" "${artifact#./}" >> SHA256SUMS
  done < <(find builds demo evidence -type f -print0)
)

ditto -c -k --keepParent --norsrc --noextattr "$bundle_root" "$output_zip"
echo "Created $output_zip"
