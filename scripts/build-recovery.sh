#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SRC="$ROOT/third_party/chameleon/src"
OUT="$ROOT/tools/bin"
mkdir -p "$OUT"
CC=${CC:-cc}
set -- "$SRC/common.c" "$SRC/crapto1.c" "$SRC/crypto1.c" "$SRC/bucketsort.c" "$SRC/parity.c" "$SRC/mfkey.c" "$SRC/nested_util.c"
for name in nested staticnested darkside mfkey32 mfkey32v2 mfkey64 staticnested_1nt staticnested_2x1nt_rf08s staticnested_2x1nt_rf08s_1key; do
  echo "Building $name"
  "$CC" -O2 -D_GNU_SOURCE -I"$SRC" "$SRC/$name.c" "$@" -pthread -o "$OUT/$name"
done
if [ -d /opt/homebrew/opt/openssl@3 ]; then
  echo 'Building mfulc_des_brute (Homebrew OpenSSL)'
  "$CC" -O2 -Wno-deprecated-declarations -I/opt/homebrew/opt/openssl@3/include "$SRC/mfulc_des_brute.c" /opt/homebrew/opt/openssl@3/lib/libcrypto.a -pthread -o "$OUT/mfulc_des_brute"
else
  echo 'Building mfulc_des_brute (system OpenSSL)'
  "$CC" -O2 -Wno-deprecated-declarations "$SRC/mfulc_des_brute.c" -lcrypto -pthread -o "$OUT/mfulc_des_brute"
fi
H="$SRC/HardnestedRecovery"
echo 'Building hardnested'
if [ -d /opt/homebrew/opt/xz ]; then
  LZMA_INCLUDE=/opt/homebrew/opt/xz/include
  LZMA_LIB=/opt/homebrew/opt/xz/lib/liblzma.a
else
  LZMA_INCLUDE=/usr/include
  LZMA_LIB=-llzma
fi
"$CC" -O2 -D_GNU_SOURCE -I"$SRC" -I"$H" -I"$H/pm3" -I"$H/hardnested" -I"$LZMA_INCLUDE" \
 "$SRC/common.c" "$SRC/crapto1.c" "$SRC/crypto1.c" "$SRC/bucketsort.c" "$SRC/parity.c" \
 "$H/hardnested_main.c" "$H/pm3/ui.c" "$H/pm3/util.c" "$H/cmdhfmfhard.c" "$H/pm3/commonutil.c" "$H/pm3/util_posix.c" \
 "$H/hardnested/hardnested_bf_core.c" "$H/hardnested/hardnested_bruteforce.c" "$H/hardnested/hardnested_bitarray_core.c" "$H/hardnested/tables.c" \
 "$LZMA_LIB" -pthread -lm -o "$OUT/hardnested"
echo 'Recovery workers built in tools/bin'
