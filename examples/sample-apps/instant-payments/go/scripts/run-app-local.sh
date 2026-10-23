#!/usr/bin/env bash
# Run the host app; usage: run-app-local.sh [--dynamodb-endpoint URL]
#   [--dynamodb-region REGION] [--dynamodb-client-type high-level|low-level] [-h|--help].
# Defaults: http://localhost:8000, eu-west-1, high-level; requires Go and a ready database.
# Local endpoints use dummy credentials; remote AWS requires SDK-chain credentials.
# Provisioning defaults true unless DYNAMODB_PROVISIONING_CREATERESOURCES overrides it:
# creates resources/demo accounts (AWS charges possible). Never deletes tables.
# No readiness wait; startup errors/invalid options fail nonzero; Ctrl-C stops the foreground app.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENDPOINT="http://localhost:8000"
REGION="eu-west-1"
CLIENT_TYPE="high-level"

while (($#)); do
  case "$1" in
    --dynamodb-endpoint) ENDPOINT="${2:?missing value for --dynamodb-endpoint}"; shift 2 ;;
    --dynamodb-region) REGION="${2:?missing value for --dynamodb-region}"; shift 2 ;;
    --dynamodb-client-type) CLIENT_TYPE="${2:?missing value for --dynamodb-client-type}"; shift 2 ;;
    -h|--help)
      printf 'Usage: %s [--dynamodb-endpoint URL] [--dynamodb-region REGION] [--dynamodb-client-type high-level|low-level]\n' "$0"
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
export DYNAMODB_ENDPOINT="$ENDPOINT"
export DYNAMODB_REGION="$REGION"
export DYNAMODB_CLIENTTYPE="$CLIENT_TYPE"
export DYNAMODB_PROVISIONING_CREATERESOURCES="${DYNAMODB_PROVISIONING_CREATERESOURCES:-true}"
exec go run ./cmd/main
