#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-$HOME/apps/durable-flow}"
COMPOSE_FILE="deploy/docker-compose.prod.yml"
ENV_FILE=".env.prod"

cd "$APP_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $APP_DIR/$ENV_FILE is missing."
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERROR: Working tree is not clean."
  echo "Commit or safely back up changes before deploying."
  git status --short
  exit 1
fi

echo "==> Updating main"
git fetch origin main
git pull --ff-only origin main

echo "==> Checking Compose configuration"
sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" config -q

echo "==> Building images sequentially"
for service in migrate engine demo-services worker-a; do
  sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" build "$service"
done

echo "==> Starting PostgreSQL"
sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d postgres

echo "==> Applying database migrations"
sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" run --rm migrate

echo "==> Deploying services"
sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d --remove-orphans

echo "==> Current container status"
sudo docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps

echo "==> Deployment command completed"
echo "Review health status and logs before declaring the deployment healthy."
