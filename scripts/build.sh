#!/bin/sh
set -eu
CHAMELEON_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$CHAMELEON_ROOT"
CARD_GO=${CARD_GO:-go}
export CGO_ENABLED=${CGO_ENABLED:-0}
"$CARD_GO" build -mod=readonly -trimpath -o chameleon ./cmd/chameleon
