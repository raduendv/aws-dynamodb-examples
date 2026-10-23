#!/usr/bin/env bash
# Start the Compose extenddb service; usage: start-dynamodb-local.sh (no arguments).
# Requires Docker/Compose and curl with --aws-sigv4. Endpoint http://localhost:8000;
# signed ListTables uses eu-west-1 and dummy AKIAlocal:local credentials, no AWS account.
# Waits up to 60 attempts (2s request timeout + 1s failed-attempt delay); failure prints logs
# and exits nonzero. In-memory shared storage loses all data when the database stops.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENDPOINT="http://localhost:8000"

cd "$PROJECT_DIR"
docker compose up -d extenddb
for attempt in {1..60}; do
  if curl --silent --fail --max-time 2 --output /dev/null \
    --aws-sigv4 'aws:amz:eu-west-1:dynamodb' --user 'AKIAlocal:local' \
    --header 'Content-Type: application/x-amz-json-1.0' \
    --header 'X-Amz-Target: DynamoDB_20120810.ListTables' \
    --data '{}' "$ENDPOINT"; then
    printf 'DynamoDB Local is ready at %s\n' "$ENDPOINT"
    exit 0
  fi
  sleep 1
done

printf 'DynamoDB Local did not become ready at %s\n' "$ENDPOINT" >&2
docker compose logs extenddb >&2
exit 1
