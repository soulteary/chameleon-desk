#include <assert.h>
#include <pthread.h>
#include <sched.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "../../third_party/chameleon/src/HardnestedRecovery/cmdhfmfhard.c"

static uint64_t mock_time;
static bool tick_clock;
static unsigned active_logs, messages;
static FILE *capture;
uint64_t msclock(void) {
    return tick_clock ? __sync_add_and_fetch(&mock_time, 1) : mock_time;
}
void PrintAndLogEx(logLevel_t level, const char *fmt, ...) {
    assert(level == INFO);
    assert(__sync_add_and_fetch(&active_logs, 1) == 1);
    char buffer[512];
    va_list args;
    va_start(args, fmt);
    int size = vsnprintf(buffer, sizeof(buffer), fmt, args);
    va_end(args);
    assert(size > 0 && (unsigned)size < sizeof(buffer));
    assert(strstr(buffer, "activity-") != NULL);
    assert(strstr(buffer, "10s") != NULL);
    for (unsigned i = 0; i < 20; i++) sched_yield();
    assert(fputs(buffer, capture) >= 0 && fputc('\n', capture) != EOF);
    messages++;
    assert(__sync_sub_and_fetch(&active_logs, 1) == 0);
}
struct progress_args { unsigned id, repeats; uint64_t interval; };
static void *progress_worker(void *value) {
    struct progress_args *arg = value;
    char activity[32];
    snprintf(activity, sizeof(activity), "activity-%u", arg->id);
    for (unsigned i = 0; i < arg->repeats; i++) hardnested_print_progress(arg->id + 1, activity, 10000, arg->interval);
    return NULL;
}
static void concurrent_progress(unsigned repeats, uint64_t interval) {
    pthread_t threads[8];
    struct progress_args args[8];
    for (unsigned i = 0; i < 8; i++) {
        args[i] = (struct progress_args){i, repeats, interval};
        assert(pthread_create(&threads[i], NULL, progress_worker, &args[i]) == 0);
    }
    for (unsigned i = 0; i < 8; i++) assert(pthread_join(threads[i], NULL) == 0);
}
int main(void) {
    capture = tmpfile();
    assert(capture != NULL);
    start_time = 0;
    brute_force_per_second = 1000;
    mock_time = 1000;
    concurrent_progress(20, 500);
    assert(messages == 1); // One shared interval, regardless of worker count.
    concurrent_progress(20, 500);
    assert(messages == 1); // Suppressed calls must release the lock.
    mock_time = 2000;
    concurrent_progress(20, 500);
    assert(messages == 2);
    tick_clock = true;
    concurrent_progress(20, 0);
    assert(messages == 162 && active_logs == 0);
    assert(fflush(capture) == 0 && fseek(capture, 0, SEEK_SET) == 0);
    char line[512];
    unsigned lines = 0;
    while (fgets(line, sizeof(line), capture) != NULL) {
        assert(strstr(line, "activity-") != NULL && strchr(line, '\n') != NULL);
        lines++;
    }
    assert(lines == messages && !ferror(capture));
    assert(fclose(capture) == 0);
    return 0;
}
