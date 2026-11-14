#include <assert.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <string.h>
#include "crapto1.h"

static unsigned calls, live;
static void *tracked_calloc(size_t count, size_t size) {
    unsigned failure = getenv("RECOVERY_FAIL_AT") ? (unsigned)atoi(getenv("RECOVERY_FAIL_AT")) : 0;
    if (++calls == failure) { errno = ENOMEM; return NULL; }
    void *p = calloc(count, size);
    assert(p != NULL);
    live++;
    return p;
}
static void tracked_free(void *p) {
    if (p != NULL) { assert(live != 0); live--; free(p); }
}
#if HELPER <= 3
static struct Crypto1State *test_recovery(uint32_t ks, uint32_t input) {
    (void)ks; (void)input;
    struct Crypto1State *states = tracked_calloc(2, sizeof(*states));
#if HELPER == 3
    if (states != NULL) states[0].odd = 1;
#endif
    return states;
}
static struct Crypto1State *test_create(uint64_t key) {
    struct Crypto1State *state = tracked_calloc(1, sizeof(*state));
    if (state != NULL) crypto1_init(state, key);
    return state;
}
#if HELPER == 3
static uint32_t test_word(struct Crypto1State *state, uint32_t input, int encrypted) {
    (void)state; (void)input; (void)encrypted;
    return 0;
}
#define crypto1_word test_word
#endif
#define lfsr_recovery32 test_recovery
#define crypto1_create test_create
#define crypto1_destroy tracked_free
#endif
static FILE *output_file;
static int io_failure(const char *stage) {
    const char *failure = getenv("RECOVERY_IO_FAILURE");
    return failure != NULL && strcmp(failure, stage) == 0;
}
static FILE *test_fopen(const char *name, const char *mode) {
    if (mode[0] == 'w' && io_failure("open")) { errno = EACCES; return NULL; }
    FILE *file = fopen(name, mode);
    if (mode[0] == 'w') output_file = file;
    return file;
}
static int test_fprintf(FILE *file, const char *format, ...) {
    if (file == output_file && io_failure("write")) { errno = ENOSPC; return -1; }
    va_list args;
    va_start(args, format);
    int result = vfprintf(file, format, args);
    va_end(args);
    return result;
}
static int test_fclose(FILE *file) {
    int is_output = file == output_file;
    int result = fclose(file);
    if (is_output) {
        output_file = NULL;
        if (io_failure("close")) { errno = EIO; return EOF; }
    }
    return result;
}
#define fopen test_fopen
#define fprintf test_fprintf
#define fclose test_fclose
#define calloc tracked_calloc
#define free tracked_free
#if HELPER == 1
#include "../../third_party/chameleon/src/mfkey32.c"
#elif HELPER == 2
#include "../../third_party/chameleon/src/mfkey32v2.c"
#elif HELPER == 3
#include "../../third_party/chameleon/src/staticnested_1nt.c"
#elif HELPER == 4
#include "../../third_party/chameleon/src/staticnested_2x1nt_rf08s.c"
#elif HELPER == 5
#include "../../third_party/chameleon/src/staticnested_2x1nt_rf08s_1key.c"
#else
#error Unknown helper
#endif
__attribute__((destructor)) static void verify_cleanup(void) { assert(live == 0); }
