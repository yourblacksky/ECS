#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose_file="$repository_root/demo/three-node/compose.yml"
demo_image=${ECS_DEMO_IMAGE:-ecs:demo}

cd "$repository_root"
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose is required; install Docker Desktop or the Compose plugin" >&2
  exit 1
fi
if [ "${ECS_DEMO_SKIP_BUILD:-0}" != "1" ]; then
  docker build --target runtime --tag "$demo_image" .
fi
ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" up --detach --build --wait

if ! go run ./cmd/ecs-network-smoke; then
  ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" logs
  ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" down --volumes
  exit 1
fi

if ! go run ./cmd/ecs-office-network-demo \
  -controller "http://127.0.0.1:18090" \
  -token "demo-controller-token" \
  -admin-token "demo-admin-token"; then
  ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" logs
  ECS_DEMO_IMAGE="$demo_image" docker compose --file "$compose_file" down --volumes
  exit 1
fi

echo "ECS controller: http://127.0.0.1:18090"
echo "ECS worker A:   http://127.0.0.1:18091"
echo "ECS worker B:   http://127.0.0.1:18092"
echo "Stop with: make demo-network-down"
