#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
developer_dir="$(xcode-select -p 2>/dev/null || true)"
if [[ "$developer_dir" != */Xcode.app/Contents/Developer ]]; then
  echo "Full Xcode is required. Install Xcode, then run:" >&2
  echo "  sudo xcode-select -s /Applications/Xcode.app/Contents/Developer" >&2
  exit 1
fi

gomobile_bin="$(go env GOPATH)/bin/gomobile"
if [[ ! -x "$gomobile_bin" ]]; then
  echo "gomobile is missing; run 'make mobile-tools'" >&2
  exit 1
fi

ios_targets="${MOBILE_IOS_TARGETS:-ios,iossimulator}"
output_dir="$repo_root/ios/Frameworks"
mkdir -p "$output_dir"
"$gomobile_bin" bind \
  -target="$ios_targets" \
  -trimpath \
  -o "$output_dir/Mobile.xcframework" \
  "$repo_root/mobile"

echo "Built $output_dir/Mobile.xcframework"
