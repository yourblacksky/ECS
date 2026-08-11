#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

git_dir="$(git rev-parse --git-common-dir)"
state="${git_dir}/ecs-harness"
mkdir -p "${state}/hooks"

for required in .githooks/pre-commit .githooks/pre-push; do
  if ! git cat-file -e "HEAD:${required}" 2>/dev/null; then
    echo "Harness trust root is not committed in HEAD: ${required}" >&2
    echo "Commit the Harness files before installation." >&2
    exit 2
  fi
done

if [[ $# -ne 0 ]]; then
  echo "install-harness.sh accepts no bootstrap override; it installs committed HEAD only" >&2
  exit 2
fi

controls=(
  scripts/install-harness.sh
  scripts/render-codex-hooks.py
  scripts/verify-harness.py
  .githooks/pre-commit
  .githooks/pre-push
)
if ! git diff --quiet -- "${controls[@]}" || \
   ! git diff --cached --quiet -- "${controls[@]}"; then
  echo "Harness trust-root files differ from HEAD; commit them before reinstalling" >&2
  exit 2
fi

trusted_tree="$(mktemp -d)"
cleanup() {
  find "${trusted_tree}" -depth -delete
}
trap cleanup EXIT
git archive HEAD | tar -x -C "${trusted_tree}"
(
  cd "${trusted_tree}"
  python3 scripts/render-codex-hooks.py --check
  python3 scripts/verify-harness.py
)

install_trusted() {
  local relative="$1"
  local destination="$2"
  local source
  source="$(mktemp)"
  git show "HEAD:${relative}" >"${source}"
  install -m 700 "${source}" "${destination}"
  find "${source}" -delete
}

install_trusted .githooks/pre-commit "${state}/hooks/pre-commit"
install_trusted .githooks/pre-push "${state}/hooks/pre-push"
git config core.hooksPath "${state}/hooks"

echo "ECS Git hooks installed outside the worktree: core.hooksPath=${state}/hooks"
echo "Codex users: open /hooks once to review and trust the two project hooks."
echo "A Harness bundle change updates its command hash and requires re-trust."
