#!/bin/sh
set -eu

usage() {
  printf 'Usage: %s [all|deps|go|vet|webui|build]\n' "$0"
}

if [ "$#" -gt 1 ]; then
  usage >&2
  exit 2
fi
CHECK_SELECTION=${1:-all}
case "$CHECK_SELECTION" in
  all|deps|go|vet|webui|build) ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

CHAMELEON_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$CHAMELEON_ROOT"
CARD_GO=${CARD_GO:-go}
export GOPROXY=off GOSUMDB=off
export CHAMELEON_TEST_PORT= CHAMELEON_TEST_NO_CARD=
mkdir -p reports
CHECK_TIMINGS=reports/check-timings.tsv
: >> "$CHECK_TIMINGS"
if [ ! -s "$CHECK_TIMINGS" ]; then
  printf 'stage\tduration_seconds\tstatus\n' > "$CHECK_TIMINGS"
fi
CHECK_ACTIVE_STAGE=
CHECK_STARTED=0
CHECK_WEBUI_READY=0

record_timing() {
  check_result=$1
  check_exit=$2
  check_finished=$(date +%s)
  check_elapsed=$((check_finished - CHECK_STARTED))
  if [ "$check_elapsed" -lt 0 ]; then check_elapsed=0; fi
  printf 'Check stage %s: %ss (%s, exit %s)\n' "$CHECK_ACTIVE_STAGE" "$check_elapsed" "$check_result" "$check_exit"
  if ! printf '%s\t%s\t%s\n' "$CHECK_ACTIVE_STAGE" "$check_elapsed" "$check_result" >> "$CHECK_TIMINGS"; then
    printf 'Could not append check timings to %s\n' "$CHECK_TIMINGS" >&2
  fi
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    if ! printf -- '- `%s`: %s seconds (%s, exit %s)\n' "$CHECK_ACTIVE_STAGE" "$check_elapsed" "$check_result" "$check_exit" >> "$GITHUB_STEP_SUMMARY"; then
      printf 'Could not append check timing to GITHUB_STEP_SUMMARY\n' >&2
    fi
  fi
  CHECK_ACTIVE_STAGE=
}

interrupt_check() {
  if [ -n "$CHECK_ACTIVE_STAGE" ]; then record_timing interrupted "$1"; fi
  exit "$1"
}

finish_check() {
  check_exit=$?
  if [ -n "$CHECK_ACTIVE_STAGE" ]; then record_timing failure "$check_exit"; fi
}
trap 'interrupt_check 129' HUP
trap 'interrupt_check 130' INT
trap 'interrupt_check 143' TERM
trap finish_check 0

check_deps() {
  if "$CARD_GO" mod verify > reports/go-mod-verify.txt 2>&1; then
    :
  else
    check_exit=$?
    cat reports/go-mod-verify.txt
    printf 'Go dependencies must be cached first: go mod download\n' >&2
    return "$check_exit"
  fi
  if "$CARD_GO" list -mod=readonly -deps -test ./... >> reports/go-mod-verify.txt 2>&1; then
    :
  else
    check_exit=$?
    cat reports/go-mod-verify.txt
    printf 'Go dependencies must be cached first: go mod download\n' >&2
    return "$check_exit"
  fi
}

check_go() {
  if "$CARD_GO" test -mod=readonly -race -count=1 -p 1 -coverpkg=./... -coverprofile=reports/coverage.out ./... > reports/unit-tests.txt 2>&1; then
    :
  else
    check_exit=$?
    cat reports/unit-tests.txt
    return "$check_exit"
  fi
  "$CARD_GO" tool cover -func=reports/coverage.out > reports/coverage-functions.txt || return $?
  cat reports/unit-tests.txt
}

check_vet() {
  if "$CARD_GO" vet -mod=readonly ./... > reports/go-vet.txt 2>&1; then
    printf 'go vet ./...: PASS\n' >> reports/go-vet.txt
  else
    check_exit=$?
    cat reports/go-vet.txt
    return "$check_exit"
  fi
}

check_webui_tools() {
  if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1 || ! node -e 'require("jsdom")' >/dev/null 2>&1; then
    printf 'Web UI regression tests require Node.js 18+ and npm ci (test dependencies only).\n' >&2
    return 1
  fi
  CHECK_WEBUI_READY=1
}

check_webui() {
  if [ "$CHECK_WEBUI_READY" -ne 1 ]; then check_webui_tools || return $?; fi
  if npm run test:webui > reports/webui-tests.txt 2>&1; then
    tail -8 reports/webui-tests.txt
  else
    check_exit=$?
    cat reports/webui-tests.txt
    return "$check_exit"
  fi
}

check_build() {
  CARD_GO="$CARD_GO" ./scripts/build.sh || return $?
  ./chameleon version || return $?
}

run_stage() {
  CHECK_ACTIVE_STAGE=$1
  CHECK_STARTED=$(date +%s)
  printf 'Starting check stage: %s\n' "$CHECK_ACTIVE_STAGE"
  if "check_$CHECK_ACTIVE_STAGE"; then
    record_timing success 0
  else
    check_exit=$?
    record_timing failure "$check_exit"
    return "$check_exit"
  fi
}

if [ "$CHECK_SELECTION" = all ]; then
  run_stage deps
  # Preserve the local runner's early dependency failure before expensive Go tests.
  check_webui_tools
  for check_stage in go vet webui build; do
    run_stage "$check_stage"
  done
else
  run_stage "$CHECK_SELECTION"
fi
