#include <assert.h>
#include <stdio.h>
#include "cmdhfmfhard.h"

/* Verify that the real command preserves a complete capture's binary layout. */
int mfnestedhard(uint8_t block, uint8_t type, uint8_t *key, uint8_t target,
                uint8_t target_type, uint8_t *target_key, bool read_file,
                bool write_file, bool slow, uint64_t *found, char *filename,
                uint32_t uid, char *path) {
    (void)key; (void)target; (void)target_type; (void)target_key;
    (void)read_file; (void)write_file; (void)slow; (void)filename;
    assert(block == 7 && type == 1 && uid == 0x01020304);
    FILE *fp = fopen(path, "r");
    assert(fp != NULL);
    for (unsigned i = 0; i < 256; i++) {
        unsigned nonce, parity;
        assert(fscanf(fp, "%u|%u\n", &nonce, &parity) == 2);
        assert(nonce == (uint32_t)i << 24);
        assert(parity == (i % 2 == 0 ? 10 : 5));
    }
    assert(fgetc(fp) == EOF && !ferror(fp));
    assert(fclose(fp) == 0);
    *found = UINT64_C(0x974c262b9278);
    return 1;
}
