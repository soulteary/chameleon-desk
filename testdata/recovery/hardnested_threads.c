#include <assert.h>
#include <errno.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

static int create_worker(pthread_t *, const pthread_attr_t *, void *(*)(void *), void *);
static int join_worker(pthread_t, void **);
#define pthread_create create_worker
#define pthread_join join_worker
#include "../../third_party/chameleon/src/HardnestedRecovery/hardnested_threads.h"
#undef pthread_create
#undef pthread_join

static unsigned creates, joins, failures, calls, failure_mode;
static unsigned bytes_seen[256], workers_seen[HARDNESTED_MAX_THREADS];

static int create_worker(pthread_t *thread, const pthread_attr_t *attributes,
                         void *(*worker)(void *), void *argument) {
    (void)attributes;
    creates++;
    assert(creates <= HARDNESTED_MAX_THREADS);
    if (failure_mode == 2 || (failure_mode == 1 && creates % 2 == 0)) {
        failures++;
        return EAGAIN; /* Failed handles are deliberately left uninitialized. */
    }
    *thread = pthread_self();
    worker(argument);
    return 0;
}

static int join_worker(pthread_t thread, void **result) {
    (void)result;
    assert(pthread_equal(thread, pthread_self()));
    joins++;
    assert(joins <= creates - failures);
    return 0;
}

static void reset(unsigned fault) {
    creates = joins = failures = calls = 0;
    failure_mode = fault;
    memset(bytes_seen, 0, sizeof(bytes_seen));
    memset(workers_seen, 0, sizeof(workers_seen));
}

static void *check_bytes(void *argument) {
    uint8_t *range = argument;
    calls++;
    assert(range[0] <= range[1] && range[1] != 0 && range[2] == 1);
    for (unsigned i = range[0]; i <= range[1]; i++) {
        assert(bytes_seen[i]++ == 0);
    }
    range[1] = 0; /* The real callback marks stage-one completion here. */
    return NULL;
}

static void *generate(void *argument) {
    uint16_t *work = argument;
    calls++;
    assert(work[0] == 3 && work[1] == 7);
    assert(work[2] >= 1 && work[2] <= HARDNESTED_MAX_THREADS);
    assert(workers_seen[work[2] - 1]++ == 0);
    return NULL;
}

int main(void) {
    const int cpus[] = {-1, 0, 1, 2, 3, 7, 8, 16, 127, 128, 129, 1024};
    for (unsigned c = 0; c < sizeof(cpus) / sizeof(cpus[0]); c++) {
        unsigned expected = cpus[c] <= 0 ? 1 : (cpus[c] > 128 ? 128 : (unsigned)cpus[c]);
        unsigned count = hardnested_thread_count(cpus[c]);
        assert(count == expected);
        for (unsigned fault = 0; fault < 3; fault++) {
            reset(fault);
            uint8_t ranges[HARDNESTED_MAX_THREADS][3];
            unsigned next = 0;
            for (unsigned i = 0; i < count; i++) {
                hardnested_first_byte_range(i, count, ranges[i]);
                assert(ranges[i][0] == next);
                next = (unsigned)ranges[i][1] + 1;
                ranges[i][2] = 1;
            }
            assert(next == 256);
            hardnested_run_workers(count, check_bytes, ranges, sizeof(ranges[0]));
            assert(calls == count && creates == count && joins == creates - failures);
            for (unsigned i = 0; i < 256; i++) assert(bytes_seen[i] == 1);
            for (unsigned i = 0; i < count; i++) assert(ranges[i][1] == 0);

            reset(fault);
            uint16_t work[HARDNESTED_MAX_THREADS][3];
            for (unsigned i = 0; i < count; i++) {
                work[i][0] = 3;
                work[i][1] = 7;
                work[i][2] = i + 1;
            }
            hardnested_run_workers(count, generate, work, sizeof(work[0]));
            assert(calls == count && creates == count && joins == creates - failures);
            for (unsigned i = 0; i < count; i++) assert(workers_seen[i] == 1);
        }
    }
    return 0;
}
