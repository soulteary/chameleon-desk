#!/bin/sh
set -eu
CHAMELEON_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$CHAMELEON_ROOT"
CARD_GO=${CARD_GO:-go}
export GOPROXY=off GOSUMDB=off
export CHAMELEON_TEST_PORT= CHAMELEON_TEST_NO_CARD=
mkdir -p reports
if ! "$CARD_GO" mod verify > reports/go-mod-verify.txt 2>&1 || ! "$CARD_GO" list -mod=readonly -deps -test ./... >> reports/go-mod-verify.txt 2>&1; then
  cat reports/go-mod-verify.txt
  echo 'Go dependencies must be cached first: go mod download' >&2
  exit 1
fi
if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1 || ! node -e 'require("jsdom")' >/dev/null 2>&1; then
  echo 'Web UI regression tests require Node.js 18+ and npm ci (test dependencies only).' >&2
  exit 1
fi
if ! "$CARD_GO" test -mod=readonly -race -count=1 -p 1 -coverpkg=./... -coverprofile=reports/coverage.out ./... > reports/unit-tests.txt 2>&1; then
  cat reports/unit-tests.txt
  exit 1
fi
"$CARD_GO" tool cover -func=reports/coverage.out > reports/coverage-functions.txt
if ! "$CARD_GO" vet -mod=readonly ./... > reports/go-vet.txt 2>&1; then
  cat reports/go-vet.txt
  exit 1
fi
printf 'go vet ./...: PASS\n' >> reports/go-vet.txt
if ! npm run test:webui > reports/webui-tests.txt 2>&1; then
  cat reports/webui-tests.txt
  exit 1
fi
CARD_GO="$CARD_GO" ./scripts/build.sh
./chameleon version
cat reports/unit-tests.txt
tail -8 reports/webui-tests.txt
