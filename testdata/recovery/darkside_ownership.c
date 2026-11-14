#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static void tracked_free(void *);
static void *tracked_realloc(void *, size_t);
#define free tracked_free
#include "../../third_party/chameleon/src/mfkey.c"
#define realloc tracked_realloc
#define main darkside_main
#include "../../third_party/chameleon/src/darkside.c"
#undef main
#undef realloc
#undef free

typedef struct {
    unsigned parity, count;
    uint64_t keys[3];
    bool fail;
} capture_t;
static capture_t captures[16];
static unsigned capture_count, processed, realloc_calls, fail_realloc;
static struct {
    void *pointer;
} allocations[32];
static unsigned used, live, peak;

static int slot(void *pointer) {
    if (pointer != NULL) {
        for (unsigned i = 0; i < used; i++) if (allocations[i].pointer == pointer) return (int)i;
    }
    return -1;
}

static void own(void *pointer) {
    assert(pointer != NULL && used < 32);
    allocations[used++].pointer = pointer;
    live++;
    if (live > peak) peak = live;
}

static void tracked_free(void *pointer) {
    if (pointer == NULL) return;
    int index = slot(pointer);
    assert(index >= 0 && live > 0);
    allocations[index].pointer = NULL;
    live--;
    free(pointer);
}

static void *tracked_realloc(void *pointer, size_t size) {
    realloc_calls++;
    if (realloc_calls == fail_realloc) {
        errno = ENOMEM;
        return NULL;
    }
    int index = slot(pointer);
    assert(pointer == NULL || index >= 0);
    void *next = realloc(pointer, size);
    assert(next != NULL);
    if (index < 0) own(next);
    else allocations[index].pointer = next;
    return next;
}

struct Crypto1State *lfsr_common_prefix(uint32_t prefix, uint32_t response,
                                      uint8_t ks[8], uint8_t parity[8][8], uint32_t no_par) {
    (void)prefix; (void)response; (void)ks; (void)parity;
    assert(processed < capture_count);
    capture_t *capture = &captures[processed++];
    assert(no_par == (capture->parity == 0));
    if (capture->fail) return NULL;
    struct Crypto1State *states = calloc(capture->count + 1, sizeof(*states));
    own(states);
    for (unsigned i = 0; i < capture->count; i++) {
        states[i].odd = capture->keys[i] >> 32;
        states[i].even = (uint32_t)capture->keys[i];
    }
    return states;
}

uint32_t lfsr_rollback_word(struct Crypto1State *state, uint32_t value, int encrypted) {
    (void)state; (void)value; (void)encrypted;
    return 0;
}

void crypto1_get_lfsr(struct Crypto1State *state, uint64_t *key) {
    *key = (uint64_t)state->odd << 32 | state->even;
}

static void reset(const capture_t *data, unsigned count) {
    assert(live == 0 && count <= 16);
    memcpy(captures, data, count * sizeof(*data));
    capture_count = count;
    processed = used = peak = realloc_calls = fail_realloc = 0;
    memset(allocations, 0, sizeof(allocations));
    errno = EIO;
}

static void command(const capture_t *data, unsigned count, int expected,
                    unsigned allocation_failure, unsigned max_live,
                    const char *first, const char *second, const char *third) {
    reset(data, count);
    fail_realloc = allocation_failure;
    char *args[2 + 5 * 16] = {"darkside", "1"};
    for (unsigned i = 0; i < count; i++) {
        args[2 + i * 5] = "2";
        args[3 + i * 5] = "0";
        args[4 + i * 5] = data[i].parity ? "1" : "0";
        args[5 + i * 5] = "3";
        args[6 + i * 5] = "4";
    }
    fflush(stdout);
    int saved = dup(STDOUT_FILENO);
    FILE *output = tmpfile();
    assert(saved >= 0 && output != NULL && dup2(fileno(output), STDOUT_FILENO) >= 0);
    int status = darkside_main(2 + 5 * count, args);
    fflush(stdout);
    assert(dup2(saved, STDOUT_FILENO) >= 0);
    close(saved);
    rewind(output);
    char text[2048] = {0};
    assert(fread(text, 1, sizeof(text) - 1, output) < sizeof(text));
    fclose(output);
    assert(status == expected && live == 0 && peak <= max_live);
    if (first) assert(strstr(text, first) != NULL);
    if (second) assert(strstr(text, second) != NULL);
    if (third) assert(strstr(text, third) != NULL);
    if (expected != 0) assert(strstr(text, "key not found") == NULL);
    if (allocation_failure) assert(processed == 0);
}

int main(void) {
    capture_t empty[] = {{1, 0, {0}, false}, {1, 0, {0}, false}, {0, 0, {0}, false}};
    reset(empty, 1);
    uint64_t *keys = NULL;
    assert(nonce2key(1, 2, 3, 4, 1, 0, &keys) == 0 && keys == NULL && errno == 0 && live == 0);
    capture_t failed[] = {{1, 0, {0}, true}};
    reset(failed, 1);
    assert(nonce2key(1, 2, 3, 4, 1, 0, &keys) == 0 && keys == NULL && errno == ENOMEM && live == 0);
    assert(nonce2key(1, 2, 3, 4, 1, 0, NULL) == 0 && errno == EINVAL);

    command(empty, 3, 0, 0, 2, "key not found", NULL, NULL);
    capture_t ordinary[] = {{1, 2, {UINT64_C(0x112233445566), UINT64_C(0x010203040506)}, false},
                            {1, 0, {0}, false}, {1, 1, {UINT64_C(0xabcdef010203)}, false}};
    command(ordinary, 3, 0, 0, 2, "112233445566", "010203040506", "ABCDEF010203");
    capture_t mixed[] = {{0, 2, {20, 10}, false}, {0, 0, {0}, false},
                        {1, 1, {99}, false}, {0, 2, {30, 20}, false},
                        {0, 2, {50, 40}, false}, {0, 2, {60, 50}, false}};
    command(mixed, 6, 0, 0, 3, "000000000063", "000000000014", "000000000032");
    command(mixed, 1, 0, 0, 2, "key not found", NULL, NULL);
    capture_t failure_after_seed[] = {{0, 2, {10, 20}, false}, {0, 0, {0}, true}};
    command(failure_after_seed, 2, 1, 0, 2, NULL, NULL, NULL);
    capture_t failure_after_key[] = {{1, 1, {11}, false}, {1, 0, {0}, true}};
    command(failure_after_key, 2, 1, 0, 2, "00000000000B", NULL, NULL);
    command(empty, 2, 1, 1, 0, NULL, NULL, NULL);
    command(empty, 2, 1, 2, 1, NULL, NULL, NULL);
    return 0;
}
