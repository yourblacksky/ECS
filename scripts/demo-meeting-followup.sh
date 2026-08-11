#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"
source_revision=${ECS_SOURCE_REVISION:-$(git rev-parse HEAD 2>/dev/null || true)}
exec go run ./cmd/ecs-meeting-demo -root "$repository_root" -repeat 5 \
  -source-revision "$source_revision" "$@"
