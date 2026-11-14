#include <assert.h>
#include <errno.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sched.h>
#include <unistd.h>

static int mock_create(pthread_t *, const pthread_attr_t *, void *(*)(void *), void *);
static int mock_join(pthread_t, void **);
#define pthread_create mock_create
#define pthread_join mock_join
#define main mfulc_main
#include "../../third_party/chameleon/src/mfulc_des_brute.c"
#undef main
#undef pthread_create
#undef pthread_join

static struct {
    void *(*callback)(void *);
    void *arg;
} waiting[256];
static uint32_t starts[256], ends[256];
static unsigned attempts, started, joined, fail_at;
static bool all_fail, normal_dispatch;

static void *wait_for_abort(void *arg) {
    size_t index = (size_t)(uintptr_t)arg;
    while (!load_flag(&abort_search)) sched_yield();
    return waiting[index].callback(waiting[index].arg);
}

static int mock_create(pthread_t *thread, const pthread_attr_t *attr,
                       void *(*callback)(void *), void *arg) {
    unsigned index = attempts++;
    assert(index < 256);
    thread_args_t *args = arg;
    starts[index] = args->start;
    ends[index] = args->end;
    assert(args->is_reader_mode && args->key_mode == 0);
    if (all_fail || attempts == fail_at) return EAGAIN;
    if (normal_dispatch) {
        /* Observe full keyspace assignments without searching 2^28 keys. */
        *thread = (pthread_t)(uintptr_t)(index + 1);
    } else {
        waiting[index].callback = callback;
        waiting[index].arg = arg;
        assert(pthread_create(thread, attr, wait_for_abort, (void *)(uintptr_t)index) == 0);
    }
    started++;
    return 0;
}

static int mock_join(pthread_t thread, void **result) {
    assert(joined < started);
    joined++;
    return normal_dispatch ? 0 : pthread_join(thread, result);
}

static void check_command(unsigned count, unsigned failure, bool fail_every) {
    attempts = started = joined = 0;
    fail_at = failure;
    all_fail = fail_every;
    normal_dispatch = !failure && !fail_every;
    char threads[16];
    snprintf(threads, sizeof(threads), "%u", count);
    char *args[] = {"mfulc_des_brute", "-r", "0000000000000000",
                    "0000000000000000FFFFFFFFFFFFFFFF", "00000000000000000000000000000000",
                    "1", threads};
    fflush(stdout);
    int saved_stdout = dup(STDOUT_FILENO);
    FILE *output = tmpfile();
    assert(saved_stdout >= 0 && output != NULL);
    assert(dup2(fileno(output), STDOUT_FILENO) >= 0);
    int status = mfulc_main(7, args);
    fflush(stdout);
    assert(dup2(saved_stdout, STDOUT_FILENO) >= 0);
    close(saved_stdout);
    rewind(output);
    char text[256] = {0};
    assert(fread(text, 1, sizeof(text) - 1, output) < sizeof(text));
    fclose(output);
    if (normal_dispatch) {
        assert(status == 0 && attempts == count && started == count && joined == count);
        uint32_t next = 0;
        for (unsigned i = 0; i < count; i++) {
            assert(starts[i] == next && ends[i] >= starts[i]);
            next = ends[i];
        }
        assert(next == (1U << 28) && !load_flag(&abort_search));
        assert(strstr(text, "No matching key was found.") != NULL);
    } else {
        assert(status == 1 && attempts == (fail_every ? 1 : failure));
        assert(started == attempts - 1 && joined == started);
        assert(load_flag(&abort_search) && !load_flag(&key_found));
        assert(strstr(text, "No matching key") == NULL);
    }
}

static void check_known_reader_key(void) {
    thread_args_t args = {0};
    args.end = 16;
    args.is_reader_mode = true;
    for (unsigned i = 0; i < KEY_SIZE; i++) args.base_key[i] = (unsigned char)(i * 2);
    DES_cblock first_key, second_key;
    memcpy(first_key, args.base_key, BLOCK_SIZE);
    first_key[0] = 14; first_key[1] = first_key[2] = first_key[3] = 0; // Candidate 7.
    memcpy(second_key, args.base_key + BLOCK_SIZE, BLOCK_SIZE);
    DES_key_schedule k1, k2;
    DES_set_key_unchecked(&first_key, &k1);
    DES_set_key_unchecked(&second_key, &k2);
    DES_cblock nonce = {1, 2, 3, 4, 5, 6, 7, 8}, second_plain;
    DES_ecb3_encrypt(&nonce, (DES_cblock *)args.init_ciphertext, &k1, &k2, &k1, DES_ENCRYPT);
    for (unsigned i = 0; i < BLOCK_SIZE; i++) {
        args.prev_ciphertext[i] = (unsigned char)(0x10 + i);
        second_plain[i] = nonce[(i + 1) % BLOCK_SIZE] ^ args.prev_ciphertext[i];
    }
    DES_ecb3_encrypt(&second_plain, (DES_cblock *)args.ciphertext, &k1, &k2, &k1, DES_ENCRYPT);
    store_flag(&key_found, 0);
    store_flag(&abort_search, 1);
    worker(&args);
    assert(!load_flag(&key_found));
    store_flag(&abort_search, 0);
    worker(&args);
    assert(load_flag(&key_found) && !load_flag(&abort_search));
}

int main(void) {
    check_command(1, 0, false);
    check_command(3, 0, false);
    check_command(256, 0, false);
    check_command(4, 1, false);
    check_command(4, 3, false);
    check_command(4, 4, false);
    check_command(4, 0, true);
    attempts = started = joined = 0;
    char *invalid[] = {"mfulc_des_brute", "-r", "0000000000000000",
                      "0000000000000000FFFFFFFFFFFFFFFF", "00000000000000000000000000000000", "1", "257"};
    const char *bad_threads[] = {"0", "-1", "257", "4junk", "99999999999999999999999999999"};
    for (unsigned i = 0; i < sizeof(bad_threads) / sizeof(bad_threads[0]); i++) {
        invalid[6] = (char *)bad_threads[i];
        assert(mfulc_main(7, invalid) == 1 && attempts == 0);
    }
    invalid[6] = "4";
    invalid[5] = "1junk";
    assert(mfulc_main(7, invalid) == 1 && attempts == 0);
    invalid[5] = "1";
    invalid[2] = "0g00000000000000";
    assert(mfulc_main(7, invalid) == 1 && attempts == 0);
    check_known_reader_key();
    return 0;
}
