package chameleon

import (
	"fmt"
	"strconv"
	"strings"
)

func RecoveryUsage(tool string) string {
	m := map[string]string{
		"nested":                        "UID(dec) DIST(dec) NT(dec) NT_ENC(dec) PARITY(dec) [more NT/NT_ENC/PARITY triples]",
		"staticnested":                  "UID(dec) KEY_TYPE(dec:96|97) NT(dec) NT_ENC(dec) [more NT/NT_ENC pairs]",
		"darkside":                      "UID(dec) NT(dec) KS_LIST(dec) PAR_LIST(dec) NR(dec) AR(dec) [more groups of five]",
		"mfkey32":                       "UID(hex) NT(hex) NR0_ENC(hex) AR0_ENC(hex) NR1_ENC(hex) AR1_ENC(hex)",
		"mfkey32v2":                     "UID(hex) NT0(hex) NR0_ENC(hex) AR0_ENC(hex) NT1(hex) NR1_ENC(hex) AR1_ENC(hex)",
		"mfkey64":                       "UID(hex) NT(hex) NR_ENC(hex) AR_ENC(hex) AT_ENC(hex) [encrypted data hex]",
		"staticnested_1nt":              "UID(hex) SECTOR(dec) NT(hex) NT_ENC(hex) PARITY(4 binary digits)",
		"staticnested_2x1nt_rf08s":      "DICTIONARY_FILE_1 DICTIONARY_FILE_2",
		"staticnested_2x1nt_rf08s_1key": "NT1(hex) KEY1(hex) DICTIONARY_FILE_2",
		"mfulc_des_brute":               "-c|-r ERNDB(hex) SECOND_CIPHERTEXT(hex) BASE_KEY(hex) SEGMENT(dec) THREADS(dec)",
		"hardnested":                    "BINARY_NONCE_FILE"}
	return m[tool]
}
func validateRecoveryArgs(tool string, args []string) error {
	valid := false
	switch tool {
	case "nested":
		valid = len(args) >= 5 && (len(args)-2)%3 == 0
	case "staticnested":
		valid = len(args) >= 4 && (len(args)-2)%2 == 0
	case "darkside":
		valid = len(args) >= 6 && (len(args)-1)%5 == 0
	case "mfkey32":
		valid = len(args) == 6
	case "mfkey32v2":
		valid = len(args) == 7
	case "mfkey64":
		valid = len(args) >= 5
	case "staticnested_1nt":
		valid = len(args) == 5
	case "staticnested_2x1nt_rf08s":
		valid = len(args) == 2
	case "staticnested_2x1nt_rf08s_1key":
		valid = len(args) == 3
	case "mfulc_des_brute":
		valid = len(args) == 6 && (args[0] == "-c" || args[0] == "-r")
	case "hardnested":
		valid = len(args) == 1
	}
	if !valid || len(args) > 1024 {
		return fmt.Errorf("usage: %s %s", tool, RecoveryUsage(tool))
	}
	for _, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 || len(arg) > 65536 {
			return fmt.Errorf("invalid recovery argument")
		}
	}
	if tool == "nested" || tool == "staticnested" || tool == "darkside" {
		for _, a := range args {
			if _, e := strconv.ParseUint(a, 10, 64); e != nil {
				return fmt.Errorf("%s expects decimal integers: %w", tool, e)
			}
		}
	}
	if tool == "nested" {
		dist, _ := strconv.ParseUint(args[1], 10, 64)
		if dist < 14 || dist > 1<<20 {
			return fmt.Errorf("nested distance must be 14..1048576")
		}
	}
	if tool == "staticnested" && args[1] != "96" && args[1] != "97" {
		return fmt.Errorf("staticnested key type must be decimal 96 or 97")
	}
	if tool == "mfkey32" || tool == "mfkey32v2" || tool == "mfkey64" {
		n := len(args)
		if tool == "mfkey64" {
			n = 5
		}
		for _, a := range args[:n] {
			if len(a) > 8 {
				return fmt.Errorf("authentication fields must fit 32 bits")
			}
			if _, e := strconv.ParseUint(a, 16, 32); e != nil {
				return fmt.Errorf("authentication fields require hexadecimal: %w", e)
			}
		}
	}
	return nil
}
