#include <assert.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <pthread.h>
#include <stdlib.h>
#ifndef __APPLE__
#include <malloc.h>
#endif

static unsigned allocations, live, fail_at;
static void *tracked_calloc(size_t n, size_t size) {
    if (__sync_add_and_fetch(&allocations, 1) == fail_at) return NULL;
    void *p = calloc(n, size);
    assert(p != NULL);
    __sync_add_and_fetch(&live, 1);
    return p;
}
static int tracked_posix_memalign(void **p, size_t alignment, size_t size) {
    if (__sync_add_and_fetch(&allocations, 1) == fail_at) return 12;
    int result = posix_memalign(p, alignment, size);
    assert(result == 0 && *p != NULL);
    __sync_add_and_fetch(&live, 1);
    return result;
}
#ifndef __APPLE__
static void *tracked_memalign(size_t alignment, size_t size) {
    void *p = NULL;
    return tracked_posix_memalign(&p, alignment, size) == 0 ? p : NULL;
}
#endif
static void tracked_free(void *p) {
    if (p != NULL) { assert(__sync_fetch_and_sub(&live, 1) > 0); free(p); }
}
#define calloc tracked_calloc
#define posix_memalign tracked_posix_memalign
#define memalign tracked_memalign
#define free tracked_free
#include "../../third_party/chameleon/src/HardnestedRecovery/hardnested/hardnested_bf_core.c"
#undef free
#undef memalign
#undef posix_memalign
#undef calloc

static _Thread_local uint64_t visits;
static _Thread_local uint32_t expected_even, target_even;
static _Thread_local bool find_key;
bool verify_key(uint32_t uid, noncelist_t *nonces, const uint8_t *first, uint32_t odd, uint32_t even) {
    (void)uid; (void)nonces; (void)first;
    assert(odd == 123 && even == expected_even++);
    visits++;
    return find_key && even == target_even;
}
uint8_t trailing_zeros(uint8_t byte) { (void)byte; return 0; }
uint8_t lfsr_rollback_byte(struct Crypto1State *state, uint32_t input, int encrypted) {
    (void)state; (void)input; (void)encrypted;
    return 0;
}
void crypto1_get_lfsr(struct Crypto1State *state, uint64_t *key) {
    (void)state;
    *key = UINT64_C(0xA0A1A2A3A4A5);
}
void PrintAndLogEx(logLevel_t level, const char *format, ...) {
    (void)level;
    va_list args;
    va_start(args, format);
    vfprintf(stderr, format, args);
    va_end(args);
}
static void check_cleanup(void) { assert(live == 0); }

static void check(uint32_t count, bool early_abort, bool found) {
    statelist_t candidate = {0};
    uint32_t odd = 123;
    candidate.states[ODD_STATE] = &odd;
    candidate.len[ODD_STATE] = 1;
    // Allocate exactly count entries, so tail access cannot rely on a sentinel.
    uint32_t failure_even[65];
    assert(fail_at == 0 || count == 65);
    uint32_t *even = fail_at ? failure_even : (count ? malloc((size_t)count * sizeof(*even)) : NULL);
    assert(count == 0 || even != NULL);
    for (uint32_t i = 0; i < count; i++) even[i] = i;
    candidate.states[EVEN_STATE] = even;
    candidate.len[EVEN_STATE] = count;
    uint32_t keys_found = early_abort;
    uint64_t tested = 0;
    visits = expected_even = 0;
    target_even = count ? count - 1 : 0;
    find_key = found;
    uint64_t key = crack_states_bitsliced_NOSIMD(0, NULL, &candidate, &keys_found, &tested, 0, NULL, NULL);
    if (found && count) {
        assert(key == UINT64_C(0xA0A1A2A3A4A5) && visits == count && tested == count - 1);
    } else {
        assert(key == UINT64_MAX);
        assert(visits == (early_abort ? 0 : count));
        assert(tested == (early_abort ? 0 : count));
    }
    assert(live == 0);
    if (fail_at == 0) free(even);
}

struct parallel_search {
    statelist_t candidate;
    uint32_t found;
    uint64_t tested;
};
static void *parallel_worker(void *arg) {
    struct parallel_search *search = arg;
    visits = expected_even = 0;
    find_key = false;
    assert(crack_states_bitsliced_NOSIMD(0, NULL, &search->candidate, &search->found, &search->tested, 0, NULL, NULL) == UINT64_MAX);
    assert(visits == 65);
    return NULL;
}
static void parallel_search(void) {
    uint32_t odd = 123, even[65];
    for (unsigned i = 0; i < 65; i++) even[i] = i;
    struct parallel_search search = {0};
    search.candidate.states[ODD_STATE] = &odd;
    search.candidate.len[ODD_STATE] = 1;
    search.candidate.states[EVEN_STATE] = even;
    search.candidate.len[EVEN_STATE] = 65;
    pthread_t workers[2];
    for (unsigned i = 0; i < 2; i++) assert(pthread_create(&workers[i], NULL, parallel_worker, &search) == 0);
    for (unsigned i = 0; i < 2; i++) assert(pthread_join(workers[i], NULL) == 0);
    assert(search.tested == 130 && live == 0);
}

int main(int argc, char **argv) {
    assert(argc == 2);
    if (strcmp(argv[1], "parallel") == 0) { parallel_search(); return 0; }
    fail_at = (unsigned)strtoul(argv[1], NULL, 10);
    atexit(check_cleanup);
    if (fail_at != 0) {
        // Two blocks: two metadata arrays, feedback, and each aligned state block.
        check(65, false, false);
        assert(!"injected allocation failure must return a failure status");
    }
    statelist_t empty_odd = {0};
    uint32_t even = 0, keys_found = 0;
    uint64_t tested = 0;
    empty_odd.states[EVEN_STATE] = &even;
    empty_odd.len[EVEN_STATE] = 1;
    assert(crack_states_bitsliced_NOSIMD(0, NULL, &empty_odd, &keys_found, &tested, 0, NULL, NULL) == UINT64_MAX);
    assert(tested == 0 && live == 0);
    const uint32_t counts[] = {0, 1, 63, 64, 65, 127, 128, 129, 1048576, 1048577};
    for (unsigned i = 0; i < sizeof(counts) / sizeof(counts[0]); i++) check(counts[i], false, false);
    check(1048577, true, false);
    check(65, false, true);
    parallel_search();
    return 0;
}
