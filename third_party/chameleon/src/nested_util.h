#ifndef NESTED_H__
#define NESTED_H__

#include "crapto1.h"

typedef struct {
    uint32_t ntp;
    uint32_t ks1;
} NtpKs1;

uint8_t valid_nonce(uint32_t Nt, uint32_t NtEnc, uint32_t Ks1, uint8_t *parity);
/* Empty results have keyCount == 0 and errno == 0. Failures return NULL,
 * set keyCount to zero, and report an errno value. The caller owns the result. */
uint64_t *nested(NtpKs1 *pNK, uint32_t sizePNK, uint32_t authuid, uint32_t *keyCount);

#endif
