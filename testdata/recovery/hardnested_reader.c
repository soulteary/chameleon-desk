#include <assert.h>
#include <stdio.h>
#include <string.h>

#include "../../third_party/chameleon/src/HardnestedRecovery/cmdhfmfhard.c"

static FILE *input(const char *text) {
    FILE *fp = tmpfile();
    assert(fp != NULL);
    assert(fputs(text, fp) >= 0);
    rewind(fp);
    return fp;
}

int main(int argc, char **argv) {
    assert(argc == 2);
    uint32_t nonce;
    uint8_t parity;
    FILE *fp = input("");
    assert(read_nonce_record(fp, &nonce, &parity) == 0);
    fclose(fp);
    fp = input("4294967295|15\n0|0");
    assert(read_nonce_record(fp, &nonce, &parity) == 1 && nonce == UINT32_MAX && parity == 15);
    assert(read_nonce_record(fp, &nonce, &parity) == 1 && nonce == 0 && parity == 0);
    assert(read_nonce_record(fp, &nonce, &parity) == 0);
    fclose(fp);
    const char *invalid[] = {"4294967296|0\n", "1|16\n", "-1|0\n", "abc\n", "1|0junk\n", "1\n", "\n"};
    for (unsigned i = 0; i < sizeof(invalid) / sizeof(invalid[0]); i++) {
        fp = input(invalid[i]);
        assert(read_nonce_record(fp, &nonce, &parity) == -1);
        fclose(fp);
    }
    char long_line[150];
    memset(long_line, '0', sizeof(long_line) - 1);
    long_line[sizeof(long_line) - 1] = '\0';
    fp = input(long_line);
    assert(read_nonce_record(fp, &nonce, &parity) == -1);
    fclose(fp);
    fp = fopen(argv[1], "w");
    assert(fp != NULL);
    assert(read_nonce_record(fp, &nonce, &parity) == -1 && ferror(fp));
    fclose(fp);
    return 0;
}
