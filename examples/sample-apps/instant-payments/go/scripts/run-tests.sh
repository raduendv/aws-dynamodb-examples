#!/usr/bin/env bash
# Test the app; usage: run-tests.sh [--unit] [--integration] [--smoke] [--coverage] [-h|--help].
# Default: all tiers; selectors may be combined. Requires Go; tagged integration/smoke
# activate the module's DynamoDB Local container unless a loopback test endpoint is supplied.
# Requires Docker/Compose for automatic activation and curl for the endpoint probe.
# AWS_ENDPOINT_URL_DYNAMODB defaults http://localhost:8000 for tagged runs;
# unreachable databases fail with startup guidance, never silently skip.
# --coverage defaults off; standalone uses fixtures, measures pkg/ + cmd/main/, requires >=90%.
# Unit fixtures need no AWS credentials; database tests use dummy local credentials.
# Container tests create/delete disposable test tables: never target production resources.
# Smoke timer release deletes an expired timer explicitly; Local has no managed TTL scheduler.
# Test/coverage/invalid-option failures exit nonzero; normal unit/build paths delete no tables.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_UNIT=false
RUN_INTEGRATION=false
RUN_SMOKE=false
RUN_ALL=false
RUN_COVERAGE=false

if (($# == 0)); then
  RUN_ALL=true
fi

while (($#)); do
  case "$1" in
    --unit) RUN_UNIT=true ;;
    --integration) RUN_INTEGRATION=true ;;
    --smoke) RUN_SMOKE=true ;;
    --coverage) RUN_COVERAGE=true ;;
    -h|--help)
      printf 'Usage: %s [--unit] [--integration] [--smoke] [--coverage]\n' "$0"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
  shift
done

cd "$PROJECT_DIR"
if [[ "$RUN_COVERAGE" == true ]]; then
  COVERAGE_PROFILE="$(mktemp)"
  trap 'rm -f "$COVERAGE_PROFILE"' EXIT
  go test -count=1 ./pkg/... ./cmd/main/... \
    -coverpkg=./pkg/...,./cmd/main/... -coverprofile="$COVERAGE_PROFILE"
  go tool cover -func="$COVERAGE_PROFILE"
  awk '
    NR > 1 { statements[$1] = $2; hits[$1] += $3 }
    END {
      for (block in statements) {
        total += statements[block]
        if (hits[block] > 0) covered += statements[block]
      }
      if (total == 0) { print "No coverage statements measured" > "/dev/stderr"; exit 1 }
      coverage = 100 * covered / total
      printf "Application coverage: %.2f%% (minimum: 90%%)\n", coverage
      if (coverage < 90) exit 1
    }
  ' "$COVERAGE_PROFILE"
fi
TEST_TAGS=()
if [[ "$RUN_ALL" == true || "$RUN_INTEGRATION" == true ]]; then
  TEST_TAGS+=("integration")
fi
if [[ "$RUN_ALL" == true || "$RUN_SMOKE" == true ]]; then
  TEST_TAGS+=("smoke")
fi
if ((${#TEST_TAGS[@]})); then
  if [[ -z "${AWS_ENDPOINT_URL_DYNAMODB:-}" ]]; then
    "$PROJECT_DIR/scripts/start-dynamodb-local.sh"
  fi
  export AWS_ENDPOINT_URL_DYNAMODB="${AWS_ENDPOINT_URL_DYNAMODB:-http://localhost:8000}"
  if ! curl --silent --output /dev/null --connect-timeout 2 --max-time 3 "$AWS_ENDPOINT_URL_DYNAMODB"; then
    printf 'DynamoDB Local is unavailable at %s. Start it with scripts/start-dynamodb-local.sh or set AWS_ENDPOINT_URL_DYNAMODB.\n' \
      "$AWS_ENDPOINT_URL_DYNAMODB" >&2
    exit 1
  fi
  TAGS="$(IFS=,; printf '%s' "${TEST_TAGS[*]}")"
  go test -count=1 -tags="$TAGS" ./...
elif [[ "$RUN_UNIT" == true ]]; then
  go test ./pkg/... ./cmd/main/...
fi
