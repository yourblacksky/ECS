#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
android_sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [[ -z "$android_sdk" && -d "$HOME/Library/Android/sdk" ]]; then
  android_sdk="$HOME/Library/Android/sdk"
fi
if [[ -z "$android_sdk" || ! -d "$android_sdk" ]]; then
  echo "Android SDK not found; set ANDROID_HOME or ANDROID_SDK_ROOT" >&2
  exit 1
fi

gomobile_bin="$(go env GOPATH)/bin/gomobile"
if [[ ! -x "$gomobile_bin" ]]; then
  echo "gomobile is missing; run 'make mobile-tools'" >&2
  exit 1
fi

export ANDROID_HOME="$android_sdk"
android_targets="${MOBILE_ANDROID_TARGETS:-android/arm,android/arm64,android/amd64}"
mkdir -p "$repo_root/android/app/libs"
"$gomobile_bin" bind \
  -target="$android_targets" \
  -androidapi=26 \
  -javapkg=io.github.yourblacksky.ecs \
  -trimpath \
  -o "$repo_root/android/app/libs/ecs-mobile.aar" \
  "$repo_root/mobile"
