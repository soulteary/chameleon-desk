#include <assert.h>
#include <stdint.h>
#include <stdlib.h>

#include "../../third_party/chameleon/src/nested_util.c"

/* Deterministic recovery states isolate candidate accounting from cryptography. */
struct Crypto1State *lfsr_recovery32(uint32_t count, uint32_t input) {
    (void)input;
    if (count == UINT32_MAX) return NULL;
    assert(count <= 2);
    struct Crypto1State *states = calloc(count + 1, sizeof(*states));
    assert(states != NULL);
    for (uint32_t i = 0; i < count; i++) {
        states[i].odd = (i + 1) * 17;
        states[i].even = 1;
    }
    return states;
}

uint32_t lfsr_rollback_word(struct Crypto1State *state, uint32_t input, int encrypted) {
    (void)state;
    (void)input;
    (void)encrypted;
    return 0;
}

void crypto1_get_lfsr(struct Crypto1State *state, uint64_t *key) {
    *key = state->odd;
}

int main(void) {
    uint32_t count = 99;
    assert(uniqsort(NULL, 0, &count) == NULL && count == 0);

    uint64_t one[] = {9};
    countKeys *groups = uniqsort(one, 1, &count);
    assert(groups != NULL && count == 1 && groups[0].key == 9 && groups[0].count == 1);
    free(groups);

    uint64_t distinct[] = {3, 1, 2};
    groups = uniqsort(distinct, 3, &count);
    assert(groups != NULL && count == 3);
    for (uint32_t i = 0; i < count; i++) {
        assert(groups[i].key == i + 1 && groups[i].count == 1);
    }
    free(groups);

    uint64_t duplicates[] = {9, 7, 9, 5, 7, 9};
    groups = uniqsort(duplicates, 6, &count);
    assert(groups != NULL && count == 3);
    assert(groups[0].key == 9 && groups[0].count == 3);
    assert(groups[1].key == 7 && groups[1].count == 2);
    assert(groups[2].key == 5 && groups[2].count == 1);
    free(groups);

    assert(nested(NULL, 0, 0, &count) == NULL && count == 0);
    NtpKs1 empty[] = {{0, 0}};
    assert(nested(empty, 1, 0, &count) == NULL && count == 0);
    NtpKs1 single[] = {{0, 1}};
    assert(nested(single, 1, 0, &count) == NULL && count == 0);

    NtpKs1 repeated[] = {{0, 2}, {0, 2}};
    uint64_t *keys = nested(repeated, 2, 0, &count);
    assert(keys != NULL && count == 2 && keys[0] == 17 && keys[1] == 34);
    free(keys);

    NtpKs1 mixed[] = {{0, 0}, {0, 1}, {0, 2}};
    keys = nested(mixed, 3, 0, &count);
    assert(keys != NULL && count == 1 && keys[0] == 17);
    free(keys);

    NtpKs1 failed[] = {{0, 1}, {0, UINT32_MAX}};
    assert(nested(failed, 2, 0, &count) == NULL && count == 0);
    return 0;
}
