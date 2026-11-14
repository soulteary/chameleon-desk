#!/bin/sh
set -eu
CHAMELEON_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$CHAMELEON_ROOT"
CARD_GO=${CARD_GO:-go}
if [ "$("$CARD_GO" env GOOS)" = darwin ] && [ "$("$CARD_GO" env CGO_ENABLED)" = 1 ]; then
 "$CARD_GO" build -mod=readonly -trimpath -ldflags "-extldflags '-Wl,-sectcreate,__TEXT,__info_plist,$CHAMELEON_ROOT/cmd/chameleon/Info.plist'" -o chameleon ./cmd/chameleon
else
 "$CARD_GO" build -mod=readonly -trimpath -o chameleon ./cmd/chameleon
fi
