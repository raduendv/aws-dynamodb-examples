#!/usr/bin/env bash
# Build/start Compose app + DynamoDB Local; usage: run-app-docker.sh
#   [--dynamodb-client-type high-level|low-level] [--stop] [-h|--help].
# Requires Docker/Compose; client defaults high-level, --stop defaults off.
# Compose defaults: eu-west-1, GoInstantPayments, internal http://extenddb:18080;
# DYNAMODB_PROVISIONING_CREATERESOURCES defaults true (creates table/demo accounts).
# Dummy credentials; no AWS account needed. Detached start does not imply HTTP readiness:
# container waits for signed database readiness, then check /actuator/health and app logs.
# --stop tears down both services and destroys in-memory data; CLI/build failures exit nonzero.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLIENT_TYPE="high-level"
STOP=false

while (($#)); do
  case "$1" in
    --dynamodb-client-type) CLIENT_TYPE="${2:?missing value for --dynamodb-client-type}"; shift 2 ;;
    --stop) STOP=true; shift ;;
    -h|--help)
      printf 'Usage: %s [--dynamodb-client-type high-level|low-level] [--stop]\n' "$0"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ "$CLIENT_TYPE" != "high-level" && "$CLIENT_TYPE" != "low-level" ]]; then
  printf 'DynamoDB client type must be high-level or low-level\n' >&2
  exit 2
fi

cd "$PROJECT_DIR"
export DYNAMODB_CLIENTTYPE="$CLIENT_TYPE"
if [[ "$STOP" == true ]]; then
  docker compose --profile app down
else
  export DYNAMODB_PROVISIONING_CREATERESOURCES="${DYNAMODB_PROVISIONING_CREATERESOURCES:-true}"
  docker compose --profile app up --build -d app
fi
