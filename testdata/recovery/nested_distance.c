#include <assert.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "nested_util.h"

static unsigned prng_calls, probes, recovery_calls;
static uint32_t first_distance, first_nonce, last_nonce;
uint32_t prng_successor(uint32_t nonce, uint32_t distance) {
    if (prng_calls++ == 0) first_distance = distance;
    else assert(distance == 1);
    assert(prng_calls <= 30); // Bound the real command even if its range counter wraps.
    return nonce + distance;
}
uint8_t valid_nonce(uint32_t nonce, uint32_t encrypted, uint32_t stream, uint8_t *parity) {
    assert(encrypted == 0 && stream == nonce);
    assert(parity[0] == 0 && parity[1] == 0 && parity[2] == 0);
    if (probes == 0) first_nonce = nonce;
    else assert(nonce == last_nonce + 1);
    last_nonce = nonce;
    probes++;
    return 0;
}
uint64_t *nested(NtpKs1 *samples, uint32_t count, uint32_t uid, uint32_t *key_count) {
    assert(samples == NULL && count == 0 && uid == 1);
    recovery_calls++;
    *key_count = 0;
    errno = 0;
    return NULL;
}
#define main nested_command
// Replace exit only in the included main so successful commands can be checked repeatedly.
#define exit(status) return (status)
#include "../../third_party/chameleon/src/nested.c"
#undef exit
#undef main

static void check(const char *distance, int expected, uint32_t initial) {
    char *args[] = {"nested", "1", (char *)distance, "305419896", "0", "0", NULL};
    prng_calls = probes = recovery_calls = 0;
    assert(nested_command(6, args) == expected);
    if (expected != 0) {
        assert(prng_calls == 0 && probes == 0 && recovery_calls == 0);
    } else {
        assert(prng_calls == 30 && probes == 29 && recovery_calls == 1);
        assert(first_distance == initial);
        assert(first_nonce == UINT32_C(305419896) + initial);
        assert(last_nonce == first_nonce + 28);
    }
}
int main(void) {
    const char *invalid[] = {"", "0", "13", "4294967282", "4294967295", "4294967296", "18446744073709551776", "-160", "+160", "160x", " 160", "0xa0"};
    for (unsigned i = 0; i < sizeof(invalid)/sizeof(invalid[0]); i++) check(invalid[i], 1, 0);
    check("14", 0, 0);
    check("15", 0, 1);
    check("160", 0, 146);
    check("000160", 0, 146);
    check("4294967280", 0, UINT32_MAX - 29);
    check("4294967281", 0, UINT32_MAX - 28);
    return 0;
}
