#include <assert.h>
#include <stdbool.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static bool fail_calloc;
static bool fail_realloc;
static void *checked_calloc(size_t n, size_t width) {
    return fail_calloc ? NULL : calloc(n, width);
}
static void *checked_realloc(void *p, size_t size) {
    return fail_realloc ? NULL : realloc(p, size);
}

#define calloc checked_calloc
#define realloc checked_realloc
#define crypto1_create checked_crypto1_create
#include "../../third_party/chameleon/src/HardnestedRecovery/cmdhfmfhard.c"
#undef crypto1_create
#undef realloc
#undef calloc

struct Crypto1State *checked_crypto1_create(uint64_t key) {
    (void)key;
    return NULL;
}
void PrintAndLogEx(logLevel_t level, const char *fmt, ...) {
    (void)level;
    va_list args;
    va_start(args, fmt);
    vfprintf(stderr, fmt, args);
    va_end(args);
}

static statelist_t prior;
static bool expect_prior;
static void check_candidates_unchanged(void) {
    assert(candidates == (expect_prior ? &prior : NULL));
    assert(prior.next == NULL);
}

static void check_nonce_allocation(uint32_t nonce, unsigned expected_count) {
    noncelistentry_t *head = nonces[1].first;
    noncelistentry_t *tail = head;
    while (tail != NULL && tail->next != NULL) tail = tail->next;
    unsigned classes = first_byte_num;
    unsigned sum = first_byte_Sum;
    fail_calloc = true;
    errno = 0;
    assert(add_nonce(nonce, 0) == -1 && errno == ENOMEM);
    assert(nonces[1].first == head && nonces[1].num == expected_count);
    assert(first_byte_num == classes && first_byte_Sum == sum);
    assert(tail == NULL || tail->next == NULL);
    fail_calloc = false;
    assert(add_nonce(nonce, 0) == 1);
    assert(nonces[1].num == expected_count + 1);
}

int main(int argc, char **argv) {
    assert(argc == 2);
    if (strcmp(argv[1], "candidate-first") == 0 || strcmp(argv[1], "candidate-append") == 0) {
        expect_prior = strcmp(argv[1], "candidate-append") == 0;
        candidates = expect_prior ? &prior : NULL;
        atexit(check_candidates_unchanged);
        fail_calloc = true;
        add_more_candidates();
        assert(!"candidate allocation failure must terminate recovery");
    }
    if (strcmp(argv[1], "crypto-state") == 0) {
        set_test_state(0);
        assert(!"cipher allocation failure must terminate recovery");
    }
    assert(strcmp(argv[1], "success") == 0);

    // Exercise empty, tail, head and middle insertions with failed allocations.
    check_nonce_allocation(0x01200000, 0);
    assert(first_byte_num == 1);
    check_nonce_allocation(0x01400000, 1);
    check_nonce_allocation(0x01100000, 2);
    check_nonce_allocation(0x01300000, 3);
    fail_calloc = true;
    assert(add_nonce(0x0120ffff, 15) == 0); // Duplicate must not allocate.
    fail_calloc = false;
    const uint32_t expected[] = {0x01100000, 0x01200000, 0x01300000, 0x01400000};
    noncelistentry_t *entry = nonces[1].first;
    for (unsigned i = 0; i < 4; i++) {
        assert(entry != NULL && entry->nonce_enc == expected[i]);
        noncelistentry_t *next = entry->next;
        free(entry);
        entry = next;
    }
    assert(entry == NULL && nonces[1].num == 4 && first_byte_num == 1);
    nonces[1].first = NULL;

    statelist_t list = {0};
    for (odd_even_t side = EVEN_STATE; side <= ODD_STATE; side++) {
        list.states[side] = malloc(64 * sizeof(uint32_t));
        assert(list.states[side] != NULL);
        list.len[side] = 2;
        list.states[side][0] = 123;
        list.states[side][1] = 456;
        list.states[side][2] = UINT32_MAX;
        uint32_t *original = list.states[side];
        fail_realloc = true;
        shrink_state_list(&list, side);
        assert(list.states[side] == original && list.len[side] == 2);
        assert(original[0] == 123 && original[1] == 456 && original[2] == UINT32_MAX);
        fail_realloc = false;
        shrink_state_list(&list, side);
        assert(list.states[side] != NULL && list.len[side] == 2);
        assert(list.states[side][0] == 123 && list.states[side][1] == 456 && list.states[side][2] == UINT32_MAX);
        free(list.states[side]);
    }

    statelist_t *first = add_more_candidates();
    statelist_t *second = add_more_candidates();
    assert(candidates == first && first->next == second && second->next == NULL);
    assert(first->len[0] == 0 && first->len[1] == 0 && first->states[0] == NULL && first->states[1] == NULL);
    assert(second->len[0] == 0 && second->len[1] == 0 && second->states[0] == NULL && second->states[1] == NULL);
    free(second);
    free(first);
    candidates = NULL;
    return 0;
}
