#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if ! command -v xcodegen >/dev/null 2>&1; then
  echo "XcodeGen is required to generate the checked-in project definition." >&2
  echo "Install it with 'brew install xcodegen', then rerun 'make ios-project'." >&2
  exit 1
fi

cd "$repo_root/ios"
xcodegen generate
echo "Generated $repo_root/ios/ECSApp.xcodeproj"
