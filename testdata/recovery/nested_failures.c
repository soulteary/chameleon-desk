#include <errno.h>
#include <pthread.h>
#include <stdint.h>
#include "crapto1.h"

/* Link these faults with each real command to verify process failure status. */
struct Crypto1State *lfsr_recovery32(uint32_t keystream, uint32_t input) {
    (void)keystream;
    (void)input;
    return NULL;
}

uint32_t lfsr_rollback_word(struct Crypto1State *state, uint32_t input, int encrypted) {
    (void)state;
    (void)input;
    (void)encrypted;
    return 0;
}

void crypto1_get_lfsr(struct Crypto1State *state, uint64_t *key) {
    (void)state;
    *key = 0;
}

uint32_t prng_successor(uint32_t state, uint32_t steps) {
    (void)state;
    (void)steps;
    return 0;
}

int recovery_pthread_create(pthread_t *thread, const pthread_attr_t *attr,
                            void *(*start)(void *), void *arg) {
    (void)thread;
    (void)attr;
    (void)start;
    (void)arg;
    return EAGAIN;
}
