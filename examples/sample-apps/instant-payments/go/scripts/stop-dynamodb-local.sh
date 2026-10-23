#!/usr/bin/env bash
# Stop only the Compose extenddb service; usage: stop-dynamodb-local.sh (no arguments).
# Requires Docker/Compose; waits for Compose stop completion, not an HTTP readiness probe.
# Destructive: in-memory database contents are discarded. App service is not stopped;
# requests fail until the database restarts. Compose errors exit nonzero.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"
docker compose stop extenddb
