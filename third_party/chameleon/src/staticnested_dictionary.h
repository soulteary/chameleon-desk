// SPDX-License-Identifier: GPL-3.0-or-later
// Local dictionary input checks shared by the static-nested filters.
#ifndef STATICNESTED_DICTIONARY_H
#define STATICNESTED_DICTIONARY_H

#include <errno.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int dictionary_key(FILE *input, uint64_t *key) {
    char token[14];
    int result = fscanf(input, "%13s", token);
    if (result == EOF) return ferror(input) ? -1 : 0;
    if (result != 1 || strlen(token) != 12) {
        errno = EINVAL;
        return -1;
    }
    *key = 0;
    for (unsigned i = 0; i < 12; i++) {
        unsigned digit;
        if (token[i] >= '0' && token[i] <= '9') digit = token[i] - '0';
        else if (token[i] >= 'a' && token[i] <= 'f') digit = token[i] - 'a' + 10;
        else if (token[i] >= 'A' && token[i] <= 'F') digit = token[i] - 'A' + 10;
        else { errno = EINVAL; return -1; }
        *key = (*key << 4) | digit;
    }
    return 1;
}

static int load_key_dictionary(const char *name, uint64_t **keys, uint32_t *count) {
    *keys = NULL;
    *count = 0;
    FILE *input = fopen(name, "r");
    if (input == NULL) return -1;
    uint64_t key;
    int result;
    while ((result = dictionary_key(input, &key)) == 1) {
        if (*count == UINT32_MAX || (size_t)*count >= SIZE_MAX / sizeof(**keys)) {
            errno = EOVERFLOW;
            goto failure;
        }
        (*count)++;
    }
    if (result < 0 || fseek(input, 0, SEEK_SET) != 0) goto failure;
    *keys = calloc(*count ? *count : 1, sizeof(**keys));
    if (*keys == NULL) goto failure;
    for (uint32_t i = 0; i < *count; i++) {
        if (dictionary_key(input, &(*keys)[i]) != 1) goto failure;
    }
    if (dictionary_key(input, &key) != 0) goto failure;
    if (fclose(input) != 0) {
        free(*keys);
        *keys = NULL;
        return -1;
    }
    return 0;
failure:
    {
        int error = errno ? errno : EIO;
        fclose(input);
        free(*keys);
        *keys = NULL;
        errno = error;
        return -1;
    }
}
#endif
