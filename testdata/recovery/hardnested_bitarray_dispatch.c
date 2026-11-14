#include <assert.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include "../../third_party/chameleon/src/HardnestedRecovery/hardnested/hardnested_bitarray_core.c"

static pthread_mutex_t barrier_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t barrier_ready = PTHREAD_COND_INITIALIZER;
static unsigned arrivals, generation;
static void together(void) {
    pthread_mutex_lock(&barrier_mutex);
    unsigned current = generation;
    if (++arrivals == 2) {
        arrivals = 0;
        generation++;
        pthread_cond_broadcast(&barrier_ready);
    } else {
        while (generation == current) pthread_cond_wait(&barrier_ready, &barrier_mutex);
    }
    pthread_mutex_unlock(&barrier_mutex);
}
#define WORDS (1u << 19)
static void fill(uint32_t *array, uint32_t pattern) {
    for (unsigned i = 0; i < WORDS; i++) array[i] = pattern;
}
static void *worker(void *arg) {
    (void)arg;
    together();
    uint32_t *a = malloc_bitarray(WORDS * sizeof(*a));
    uint32_t *b = malloc_bitarray(WORDS * sizeof(*b));
    uint32_t *c = malloc_bitarray(WORDS * sizeof(*c));
    uint32_t *d = malloc_bitarray(WORDS * sizeof(*d));
    assert(a != NULL && b != NULL && c != NULL && d != NULL);
    together();
    assert(bitcount(0xf0f0) == 8);
    fill(a, UINT32_MAX);
    together();
    assert(count_states(a) == WORDS * 32);
    fill(b, 0xaaaaaaaa);
    together();
    bitarray_AND(a, b);
    assert(a[0] == 0xaaaaaaaa && a[WORDS - 1] == 0xaaaaaaaa);
    fill(b, 0);
    together();
    bitarray_low20_AND(a, b);
    assert(a[0] == 0 && a[WORDS - 1] == 0);
    fill(a, UINT32_MAX); fill(b, 0xaaaaaaaa);
    together();
    assert(count_bitarray_AND(a, b) == WORDS * 16);
    fill(a, UINT32_MAX); fill(b, 0);
    together();
    assert(count_bitarray_low20_AND(a, b) == 0);
    fill(b, 0xaaaaaaaa); fill(c, 0xcccccccc); fill(d, 0xf0f0f0f0);
    together();
    bitarray_AND4(a, b, c, d);
    assert(a[0] == 0x80808080 && a[WORDS - 1] == 0x80808080);
    fill(a, 0xaaaaaaaa); fill(b, 0x55555555);
    together();
    bitarray_OR(a, b);
    assert(a[0] == UINT32_MAX && a[WORDS - 1] == UINT32_MAX);
    fill(a, 0xaaaaaaaa); fill(b, 0xcccccccc);
    together();
    assert(count_bitarray_AND2(a, b) == WORDS * 8);
    fill(c, 0xf0f0f0f0);
    together();
    assert(count_bitarray_AND3(a, b, c) == WORDS * 4);
    fill(d, UINT32_MAX);
    together();
    assert(count_bitarray_AND4(a, b, c, d) == WORDS * 4);
    together();
    free_bitarray(a); free_bitarray(b); free_bitarray(c); free_bitarray(d);
    return NULL;
}
int main(void) {
    pthread_t workers[2];
    for (unsigned i = 0; i < 2; i++) assert(pthread_create(&workers[i], NULL, worker, NULL) == 0);
    for (unsigned i = 0; i < 2; i++) assert(pthread_join(workers[i], NULL) == 0);
    return 0;
}
