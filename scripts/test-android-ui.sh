#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
android_root="$repo_root/android"

if [[ -z "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}" ]]; then
  if [[ -d "$HOME/Library/Android/sdk" ]]; then
    export ANDROID_HOME="$HOME/Library/Android/sdk"
  elif [[ -d "/opt/homebrew/share/android-commandlinetools" ]]; then
    export ANDROID_HOME="/opt/homebrew/share/android-commandlinetools"
  else
    echo "Android SDK not found; set ANDROID_HOME or ANDROID_SDK_ROOT" >&2
    exit 1
  fi
fi

if [[ -z "${JAVA_HOME:-}" && -d "/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home" ]]; then
  export JAVA_HOME="/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home"
fi

if ! adb get-state >/dev/null 2>&1; then
  echo "No authorized Android emulator or device is connected" >&2
  exit 1
fi

abi="$(adb shell getprop ro.product.cpu.abi | tr -d '\r')"
case "$abi" in
  arm64-v8a | armeabi-v7a | x86_64) ;;
  *)
    echo "Unsupported test device ABI: $abi" >&2
    exit 1
    ;;
esac

(
  cd "$android_root"
  ./gradlew assembleDebug assembleDebugAndroidTest
)

adb install -r "$android_root/app/build/outputs/apk/debug/app-$abi-debug.apk"
adb install -r "$android_root/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk"

sdk="$(adb shell getprop ro.build.version.sdk | tr -d '\r')"
if (( sdk >= 33 )); then
  adb shell pm grant io.github.yourblacksky.ecs android.permission.POST_NOTIFICATIONS
  adb shell pm grant io.github.yourblacksky.ecs android.permission.NEARBY_WIFI_DEVICES
fi

instrumentation_output="$(
  adb shell am instrument -w -r \
    io.github.yourblacksky.ecs.test/androidx.test.runner.AndroidJUnitRunner |
    tr -d '\r'
)"
printf '%s\n' "${instrumentation_output}"

# `adb shell am instrument` may exit zero even when AndroidJUnitRunner reports
# failures. Require its explicit success footer so local and CI runs fail closed.
if grep -Eq '^(FAILURES!!!|INSTRUMENTATION_FAILED:)' \
  <<<"${instrumentation_output}" ||
  ! grep -Eq '^OK \([0-9]+ tests?\)$' <<<"${instrumentation_output}"; then
  echo "Android instrumentation did not complete successfully" >&2
  exit 1
fi
