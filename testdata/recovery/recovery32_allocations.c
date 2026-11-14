#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

static void *tracked_malloc(size_t);
static void tracked_free(void *);
#define malloc tracked_malloc
#define free tracked_free
#include "../../third_party/chameleon/src/crapto1.c"
#undef malloc
#undef free

#define RECOVERY_ALLOCATIONS (3 + 2 * 256)
static struct {
    void *pointer;
    bool live;
} allocations[RECOVERY_ALLOCATIONS];
static unsigned calls, used, live;
#ifdef RECOVERY_ALLOC_COMMAND
static unsigned fail_at = 5;
#else
static unsigned fail_at;
#endif

static void *tracked_malloc(size_t size) {
    calls++;
    if (calls == fail_at) {
        errno = ENOMEM;
        return NULL;
    }
    /* Failure exits precede table recovery. Keep the first state writable,
     * without allocating tens of gigabytes across the 515 fault positions. */
    void *pointer = malloc(fail_at ? sizeof(struct Crypto1State) : size);
    assert(pointer != NULL && used < RECOVERY_ALLOCATIONS);
    allocations[used].pointer = pointer;
    allocations[used++].live = true;
    live++;
    return pointer;
}

static void tracked_free(void *pointer) {
    if (pointer == NULL) return;
    for (unsigned i = 0; i < used; i++) {
        if (allocations[i].pointer == pointer) {
            assert(allocations[i].live);
            allocations[i].live = false;
            allocations[i].pointer = NULL;
            live--;
            free(pointer);
            return;
        }
    }
    assert(!"free of an untracked allocation");
}

#ifdef RECOVERY_ALLOC_COMMAND
__attribute__((destructor)) static void verify_command_cleanup(void) {
    assert(live == 0);
}
#else
static void reset(unsigned failure) {
    assert(live == 0);
    for (unsigned i = 0; i < used; i++) {
        allocations[i].pointer = NULL;
        allocations[i].live = false;
    }
    calls = used = 0;
    fail_at = failure;
    errno = EIO;
}

static void check_success(void) {
    reset(0);
    struct Crypto1State *states = lfsr_recovery32(0x89abcdef, 0x01234567);
    assert(states != NULL && errno == 0 && live == 1 && calls == RECOVERY_ALLOCATIONS);
    unsigned count = 0;
    uint64_t digest = UINT64_C(14695981039346656037);
    while (count < (1U << 18) && (states[count].odd || states[count].even)) {
        digest = (digest ^ states[count].odd) * UINT64_C(1099511628211);
        digest = (digest ^ states[count].even) * UINT64_C(1099511628211);
        count++;
    }
    assert(count == 70243 && digest == UINT64_C(0x57decff45e2821d9));
    tracked_free(states);
    assert(live == 0);
}

static void check_failure(unsigned failure) {
    reset(failure);
    assert(lfsr_recovery32(0x89abcdef, 0x01234567) == NULL);
    assert(errno == ENOMEM && live == 0);
    assert(calls == (failure < 3 ? 3 : failure));
}

int main(void) {
    check_success(); // Populate stack storage before failing the second bucket.
    check_failure(5);
    for (unsigned failure = 1; failure <= RECOVERY_ALLOCATIONS; failure++) check_failure(failure);
    check_success();
    return 0;
}
#endif
