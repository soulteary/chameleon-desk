#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <inttypes.h>
#include <ctype.h>
#include "parity.h"

#if WIN32
#include "windows.h"
#else
#include "unistd.h"
#endif

#include "pthread.h"
#include "nested_util.h"


#define MEM_CHUNK               10000
#define TRY_KEYS                50


typedef struct {
    uint64_t       key;
    uint32_t       count;
} countKeys;

typedef struct {
    NtpKs1 *pNK;
    uint32_t authuid;

    uint64_t *keys;
    uint32_t keyCount;
    bool failed;

    uint32_t startPos;
    uint32_t endPos;
} RecPar;


inline static int compar_int(const void *a, const void *b) {
    if (*(uint64_t *)b == *(uint64_t *)a) return 0;
    if (*(uint64_t *)b < * (uint64_t *)a) return 1;
    return -1;
}

// Compare countKeys structure
int compar_special_int(const void *a, const void *b) {
    const countKeys *left = a, *right = b;
    if (left->count != right->count) return left->count < right->count ? 1 : -1;
    return compar_int(&left->key, &right->key);
}

// keys qsort and unique.
static bool allocation_fits(uint64_t count, size_t width) {
    return count <= SIZE_MAX / width;
}

countKeys *uniqsort(uint64_t *possibleKeys, uint32_t size, uint32_t *uniqueCount) {
    *uniqueCount = 0;
    if (size == 0 || possibleKeys == NULL || !allocation_fits(size, sizeof(countKeys))) return NULL;
    countKeys *our_counts = calloc(size, sizeof(countKeys));
    if (our_counts == NULL) {
        fprintf(stderr, "Memory allocation error for our_counts\n");
        return NULL;
    }
    qsort(possibleKeys, size, sizeof(uint64_t), compar_int);
    for (uint32_t i = 0; i < size;) {
        uint32_t start = i;
        uint64_t key = possibleKeys[i++];
        while (i < size && possibleKeys[i] == key) i++;
        our_counts[*uniqueCount].key = key;
        our_counts[*uniqueCount].count = i - start;
        (*uniqueCount)++;
    }
    qsort(our_counts, *uniqueCount, sizeof(countKeys), compar_special_int);
    return our_counts;
}

// nested decrypt
static void *nested_revover(void *args) {
    struct Crypto1State *revstate, *revstate_start = NULL;
    uint64_t lfsr = 0;
    uint32_t i, kcount = 0, capacity = 0;

    RecPar *rp = (RecPar *)args;

    rp->keyCount = 0;
    rp->keys = NULL;
    rp->failed = false;

    //printf("Start pos is %d, End pos is %d\r\n", rp->startPos, rp->endPos);

    for (i = rp->startPos; i < rp->endPos; i++) {
        uint32_t nt_probe = rp->pNK[i].ntp ^ rp->authuid;
        uint32_t ks1 = rp->pNK[i].ks1;

        /*
        printf("     ntp = %"PRIu32"\r\n", nt_probe);
        printf("     ks1 = %"PRIu32"\r\n", ks1);
        printf("\r\n");
        */

        // And finally recover the first 32 bits of the key
        revstate = lfsr_recovery32(ks1, nt_probe);
        revstate_start = revstate;
        if (revstate == NULL) {
            rp->failed = true;
            break;
        }

        while ((revstate->odd != 0x0) || (revstate->even != 0x0)) {
            lfsr_rollback_word(revstate, nt_probe, 0);
            crypto1_get_lfsr(revstate, &lfsr);
            if (kcount == capacity) {
                uint32_t next = capacity > UINT32_MAX - MEM_CHUNK ? UINT32_MAX : capacity + MEM_CHUNK;
                if (next <= kcount || !allocation_fits(next, sizeof(uint64_t))) {
                    rp->failed = true;
                    break;
                }
                void *tmp = realloc(rp->keys, next * sizeof(uint64_t));
                if (tmp == NULL) {
                    fprintf(stderr, "Memory allocation error for possible keys\n");
                    rp->failed = true;
                    break;
                }
                rp->keys = (uint64_t *)tmp;
                capacity = next;
            }
            rp->keys[kcount] = lfsr;
            kcount++;
            revstate++;
        }
        free(revstate_start);
        revstate_start = NULL;
        if (rp->failed) {
            break;
        }
    }
    if (rp->failed || kcount == 0) {
        free(rp->keys);
        rp->keys = NULL;
    } else {
        rp->keyCount = kcount;
    }
    return NULL;
}

uint64_t *nested(NtpKs1 *pNK, uint32_t sizePNK, uint32_t authuid, uint32_t *keyCount) {
#define THREAD_MAX 4
    if (keyCount == NULL) return NULL;
    *keyCount = 0;
    if (pNK == NULL || sizePNK == 0) return NULL;

    uint32_t manyThread = sizePNK < THREAD_MAX ? sizePNK : THREAD_MAX;
    pthread_t threads[THREAD_MAX];
    RecPar params[THREAD_MAX] = {0};
    uint32_t started = 0;
    bool failed = false;
    uint32_t average = sizePNK / manyThread;
    uint32_t modules = sizePNK % manyThread;

    for (uint32_t i = 0, pos = 0; i < manyThread; i++, pos += average) {
        params[i].pNK = pNK;
        params[i].authuid = authuid;
        params[i].startPos = pos;
        params[i].endPos = pos + average;
        if (i == manyThread - 1) params[i].endPos += modules;
        if (pthread_create(&threads[i], NULL, nested_revover, &params[i]) != 0) {
            fprintf(stderr, "Cannot start recovery thread\n");
            failed = true;
            break;
        }
        started++;
    }
    uint64_t total = 0;
    for (uint32_t i = 0; i < started; i++) {
        pthread_join(threads[i], NULL);
        if (params[i].failed) failed = true;
        total += params[i].keyCount;
    }

    uint64_t *keys = NULL;
    if (!failed && total > 0 && total <= UINT32_MAX && allocation_fits(total, sizeof(uint64_t))) {
        keys = malloc((size_t)total * sizeof(uint64_t));
        if (keys == NULL) fprintf(stderr, "Cannot allocate memory to merge keys\n");
        else {
            size_t pos = 0;
            for (uint32_t i = 0; i < started; i++) {
                if (params[i].keyCount == 0) continue;
                memcpy(keys + pos, params[i].keys, params[i].keyCount * sizeof(uint64_t));
                pos += params[i].keyCount;
            }
        }
    }
    for (uint32_t i = 0; i < started; i++) free(params[i].keys);
    if (keys == NULL) return NULL;

    uint32_t uniqueCount = 0;
    countKeys *counts = uniqsort(keys, (uint32_t)total, &uniqueCount);
    free(keys);
    keys = NULL;
    if (counts == NULL) return NULL;

    uint32_t limit = uniqueCount < TRY_KEYS ? uniqueCount : TRY_KEYS;
    uint32_t selected = 0;
    while (selected < limit && counts[selected].count >= 2) selected++;
    if (selected > 0) {
        keys = malloc(selected * sizeof(uint64_t));
        if (keys == NULL) fprintf(stderr, "Cannot allocate memory for selected keys\n");
        else {
            for (uint32_t i = 0; i < selected; i++) keys[i] = counts[i].key;
            *keyCount = selected;
        }
    }
    free(counts);
    return keys;
}

// Return 1 if the nonce is invalid else return 0
uint8_t valid_nonce(uint32_t Nt, uint32_t NtEnc, uint32_t Ks1, uint8_t *parity) {
    return (
               (oddparity8((Nt >> 24) & 0xFF) == ((parity[0]) ^ oddparity8((NtEnc >> 24) & 0xFF) ^ BIT(Ks1, 16))) && \
               (oddparity8((Nt >> 16) & 0xFF) == ((parity[1]) ^ oddparity8((NtEnc >> 16) & 0xFF) ^ BIT(Ks1, 8))) && \
               (oddparity8((Nt >> 8) & 0xFF) == ((parity[2]) ^ oddparity8((NtEnc >> 8) & 0xFF) ^ BIT(Ks1, 0)))
           ) ? 1 : 0;
}
