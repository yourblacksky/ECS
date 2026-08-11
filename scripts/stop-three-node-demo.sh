#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose_file="$repository_root/demo/three-node/compose.yml"
demo_image=${ECS_DEMO_IMAGE:-ecs:demo}

if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose is required; install Docker Desktop or the Compose plugin" >&2
  exit 1
fi
ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" down --volumes
