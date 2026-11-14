#include <assert.h>
#include <errno.h>
#include <math.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int mock_create(pthread_t *, const pthread_attr_t *, void *(*)(void *), void *);
static int mock_join(pthread_t, void **);
static FILE *mock_fmemopen(void *, size_t, const char *);
#define pthread_create mock_create
#define pthread_join mock_join
#define fmemopen mock_fmemopen
#include "../../third_party/chameleon/src/HardnestedRecovery/hardnested/hardnested_bruteforce.c"
#undef pthread_create
#undef pthread_join
#undef fmemopen

static int detected_cpus;
static unsigned cpu_calls, creates, joins, visits, failed_starts;
static unsigned fail_threads;
static bool check_ids, fail_bench_file;
static unsigned seen[260];
static bool simultaneous_keys;
static unsigned ready_keys;
static pthread_mutex_t key_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t key_ready = PTHREAD_COND_INITIALIZER;

int num_CPUs(void) {
    cpu_calls++;
    return detected_cpus;
}

uint64_t msclock(void) {
    static uint64_t now;
    now += 1000;
    return now;
}

static int mock_create(pthread_t *thread, const pthread_attr_t *attr,
                       void *(*start)(void *), void *arg) {
    (void)attr;
    creates++;
    assert(creates <= MAX_BRUTE_FORCE_THREADS);
    if (fail_threads == 2 || (fail_threads == 1 && creates % 2 == 0)) {
        failed_starts++;
        return EAGAIN;
    }
    memset(thread, 0, sizeof(*thread));
    start(arg);
    return 0;
}

static int mock_join(pthread_t thread, void **result) {
    (void)thread; (void)result;
    joins++;
    return 0;
}

static FILE *mock_fmemopen(void *data, size_t length, const char *mode) {
    return fail_bench_file ? NULL : fmemopen(data, length, mode);
}

void bitslice_test_nonces(uint32_t count, uint32_t *nonce, uint8_t *parity) {
    (void)nonce; (void)parity;
    assert(count <= 256);
}

uint64_t crack_states_bitsliced(uint32_t uid, uint8_t *first, statelist_t *candidate,
                               uint32_t *found, uint64_t *tested, uint32_t count,
                               uint8_t *second, noncelist_t *nonce) {
    (void)uid; (void)first; (void)found; (void)count; (void)second; (void)nonce;
    if (simultaneous_keys) {
        pthread_mutex_lock(&key_mutex);
        ready_keys++;
        if (ready_keys == 2) pthread_cond_broadcast(&key_ready);
        while (ready_keys < 2) pthread_cond_wait(&key_ready, &key_mutex);
        pthread_mutex_unlock(&key_mutex);
        return UINT64_C(0xA0A1A2A3A4A5);
    }
    visits++;
    *tested += (uint64_t)candidate->len[0] * candidate->len[1];
    if (check_ids) {
        uint32_t id = candidate->states[0][0];
        assert(id < 260 && seen[id]++ == 0);
    }
    return UINT64_MAX;
}

void hardnested_print_progress(uint32_t count, const char *activity, float rate, uint64_t interval) {
    (void)count; (void)activity; (void)rate; (void)interval;
}

uint8_t lfsr_rollback_byte(struct Crypto1State *state, uint32_t input, int encrypted) {
    (void)state; (void)input; (void)encrypted;
    return 0;
}

uint8_t crypto1_byte(struct Crypto1State *state, uint8_t input, int encrypted) {
    (void)state; (void)input; (void)encrypted;
    return 0;
}

static void reset(int cpus, unsigned failures) {
    detected_cpus = cpus;
    cpu_calls = creates = joins = visits = failed_starts = 0;
    fail_threads = failures;
    check_ids = fail_bench_file = false;
    memset(seen, 0, sizeof(seen));
}

int main(void) {
    const int cpu_counts[] = {-1, 0, 1, 2, 128, 129, 1024};
    for (unsigned i = 0; i < sizeof(cpu_counts) / sizeof(cpu_counts[0]); i++) {
        reset(cpu_counts[i], 0);
        float rate = brute_force_benchmark();
        unsigned expected = cpu_counts[i] <= 0 ? 1 : (cpu_counts[i] > 128 ? 128 : (unsigned)cpu_counts[i]);
        assert(isfinite(rate) && rate > 0);
        assert(visits == expected && creates == expected && joins == expected);
        assert(cpu_calls == 2 && buckets == NULL && bucket_count == 0);
    }
    reset(129, 0);
    fail_bench_file = true;
    assert(brute_force_benchmark() == DEFAULT_BRUTE_FORCE_RATE);
    assert(creates == 0);

    statelist_t candidates[261] = {0};
    uint32_t states[260][2];
    for (unsigned i = 0; i < 260; i++) {
        states[i][0] = i;
        states[i][1] = UINT32_MAX;
        candidates[i].states[0] = candidates[i].states[1] = states[i];
        candidates[i].len[0] = candidates[i].len[1] = 1;
        candidates[i].next = &candidates[i + 1];
    }
    for (unsigned total = 129; total <= 260; total += 131) {
        candidates[total - 1].next = &candidates[260]; // Empty bucket must be skipped.
        for (unsigned fault = 0; fault < 3; fault++) {
            reset(129, fault);
            check_ids = true;
            float rate = 0;
            uint64_t key = 0;
            assert(!brute_force_bs(&rate, candidates, 0, 0, total, NULL, NULL, &key));
            assert(visits == total && creates == 128 && joins == creates - failed_starts);
            assert(cpu_calls == 1 && buckets == NULL && bucket_count == 0);
            for (unsigned i = 0; i < total; i++) assert(seen[i] == 1);
        }
        candidates[total - 1].next = &candidates[total];
    }
    reset(0, 0);
    check_ids = true;
    float rate = 0;
    uint64_t key = 0;
    assert(!brute_force_bs(&rate, candidates, 0, 0, 260, NULL, NULL, &key));
    assert(visits == 260 && creates == 1 && joins == 1);
    // Force both worker searches to succeed before either can claim the key.
    // Publishing a key must select one result rather than add both values.
    simultaneous_keys = true;
    keys_found = 0;
    found_bs_key = 0;
    bucket_count = 2;
    statelist_t *concurrent_buckets[] = {&candidates[0], &candidates[1]};
    buckets = concurrent_buckets;
    brute_force_args_t args[2] = {{0}};
    pthread_t concurrent[2];
    for (unsigned i = 0; i < 2; i++) {
        args[i].thread_ID = i;
        args[i].thread_count = 2;
        args[i].silent = true;
        assert(pthread_create(&concurrent[i], NULL, crack_states_thread, &args[i]) == 0);
    }
    for (unsigned i = 0; i < 2; i++) assert(pthread_join(concurrent[i], NULL) == 0);
    assert(ready_keys == 2 && keys_found == 1 && found_bs_key == UINT64_C(0xA0A1A2A3A4A5));
    buckets = NULL;
    bucket_count = 0;
    return 0;
}
