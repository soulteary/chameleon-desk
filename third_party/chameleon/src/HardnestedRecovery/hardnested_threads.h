/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef HARDNESTED_THREADS_H
#define HARDNESTED_THREADS_H

#include <pthread.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define HARDNESTED_MAX_THREADS 128

static inline uint32_t hardnested_thread_count(int detected) {
    if (detected <= 0) return 1;
    return detected > HARDNESTED_MAX_THREADS ? HARDNESTED_MAX_THREADS : (uint32_t)detected;
}

/* Inclusive ranges cover all 256 first bytes once. count is in 1..128. */
static inline void hardnested_first_byte_range(uint32_t index, uint32_t count, uint8_t *range) {
    range[0] = (uint8_t)(256U * index / count);
    range[1] = (uint8_t)(256U * (index + 1) / count - 1);
}

/* count is in 1..128. Arguments remain live until every worker completes. */
static inline void hardnested_run_workers(uint32_t count, void *(*worker)(void *),
                                          void *arguments, size_t argument_size) {
    pthread_t threads[HARDNESTED_MAX_THREADS];
    bool started[HARDNESTED_MAX_THREADS] = {false};
    for (uint32_t i = 0; i < count; i++) {
        void *arg = (char *)arguments + i * argument_size;
        if (pthread_create(&threads[i], NULL, worker, arg) == 0) {
            started[i] = true;
        } else {
            worker(arg);
        }
    }
    for (uint32_t i = 0; i < count; i++) {
        if (started[i]) pthread_join(threads[i], NULL);
    }
}

#endif
