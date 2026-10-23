#!/usr/bin/env bash
# Delete a table permanently; usage: delete-dynamodb-table.sh [--dynamodb-endpoint URL]
#   [--dynamodb-region REGION] [--table-name NAME] [-h|--help].
# Defaults: http://localhost:8000, eu-west-1, GoInstantPayments. Requires AWS CLI and
# reachable database; supply dummy AWS_ACCESS_KEY_ID=AKIAlocal AWS_SECRET_ACCESS_KEY=local
# for Local, or authorized temporary/role credentials for remote AWS.
# NO confirmation: verify endpoint/region/table first. Waits for table-not-exists;
# deletion/waiter failures exit nonzero. No app/stream coordination or recovery.
set -euo pipefail

ENDPOINT="http://localhost:8000"
REGION="eu-west-1"
TABLE_NAME="GoInstantPayments"

while (($#)); do
  case "$1" in
    --dynamodb-endpoint) ENDPOINT="${2:?missing value for --dynamodb-endpoint}"; shift 2 ;;
    --dynamodb-region) REGION="${2:?missing value for --dynamodb-region}"; shift 2 ;;
    --table-name) TABLE_NAME="${2:?missing value for --table-name}"; shift 2 ;;
    -h|--help)
      printf 'Usage: %s [--dynamodb-endpoint URL] [--dynamodb-region REGION] [--table-name NAME]\n' "$0"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

aws dynamodb delete-table \
  --endpoint-url "$ENDPOINT" \
  --region "$REGION" \
  --table-name "$TABLE_NAME"
aws dynamodb wait table-not-exists \
  --endpoint-url "$ENDPOINT" \
  --region "$REGION" \
  --table-name "$TABLE_NAME"
