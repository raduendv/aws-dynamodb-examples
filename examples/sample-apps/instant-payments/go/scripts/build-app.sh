#!/usr/bin/env bash
# Build bin/instantpayments; usage: build-app.sh [--skip-tests] [-h|--help].
# Requires Go from go.mod; default runs unit tests before compiling.
# --skip-tests defaults off. Test/build failure exits nonzero; no services or table deletion.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_TESTS=false

for arg in "$@"; do
  case "$arg" in
    --skip-tests) SKIP_TESTS=true ;;
    -h|--help)
      printf 'Usage: %s [--skip-tests]\n' "$0"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$arg" >&2; exit 2 ;;
  esac
done

cd "$PROJECT_DIR"
if [[ "$SKIP_TESTS" != true ]]; then
  go test ./pkg/... ./cmd/main/...
fi
mkdir -p "$PROJECT_DIR/bin"
go build -trimpath -o "$PROJECT_DIR/bin/instantpayments" ./cmd/main
