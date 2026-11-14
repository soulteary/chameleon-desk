package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type desDictionaryAttempt struct {
	key       []byte
	algorithm string
	match     bool
}

func desDictionarySimulator(t *testing.T, attempts []desDictionaryAttempt) string {
	t.Helper()
	var scans, completed atomic.Int32
	var block cipher.Block
	var rndB, encryptedB []byte
	var current int
	port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
		switch cmd {
		case 1002:
			return 0x68, []byte{1}
		case 2016:
			current = int(scans.Add(1)) - 1
			return 0, []byte{4, 1, 2, 3, 4, 4, 0, 0x20, 2, 2, 5}
		case 2101:
			completed.Add(1)
			return 0x68, nil
		case 2010:
			if current >= len(attempts) || len(payload) < 11 {
				t.Error("unexpected DESFire exchange")
				return 0x67, nil
			}
			frame := payload[5:]
			apdu := frame[1:]
			n := int(apdu[4])
			if len(apdu) < 6+n {
				t.Error("truncated DESFire request")
				return 0x67, nil
			}
			data := apdu[5 : 5+n]
			reply := func(data []byte, status byte) (uint16, []byte) {
				out := append([]byte{2 | frame[0]&1}, data...)
				return 0, append(out, 0x91, status)
			}
			attempt := attempts[current]
			switch apdu[1] {
			case 0x5a:
				if !bytes.Equal(data, []byte{1, 2, 3}) {
					t.Errorf("AID changed: %X", data)
				}
				return reply(nil, 0)
			case 0xaa, 0x0a, 0x1a:
				wantOp := map[string]byte{"aes": 0xaa, "des": 0x0a, "3des": 0x1a}[attempt.algorithm]
				if apdu[1] != wantOp || !bytes.Equal(data, []byte{2}) {
					t.Errorf("wrong authentication algorithm/key number: %X", apdu)
				}
				var err error
				if attempt.algorithm == "aes" {
					block, err = aes.NewCipher(attempt.key)
				} else {
					key := append([]byte(nil), attempt.key...)
					if len(key) == 8 {
						key = bytes.Repeat(key, 3)
					} else if len(key) == 16 {
						key = append(key, key[:8]...)
					}
					block, err = des.NewTripleDESCipher(key)
				}
				if err != nil {
					t.Error(err)
					return 0x67, nil
				}
				size := block.BlockSize()
				if attempt.algorithm == "3des" {
					size = 16
				}
				rndB = bytes.Repeat([]byte{0x5a}, size)
				encryptedB = make([]byte, size)
				cipher.NewCBCEncrypter(block, make([]byte, block.BlockSize())).CryptBlocks(encryptedB, rndB)
				return reply(encryptedB, 0xaf)
			case 0xaf:
				if block == nil || len(data) != 2*len(rndB) {
					t.Error("invalid authentication continuation")
					return 0x67, nil
				}
				plain := make([]byte, len(data))
				cipher.NewCBCDecrypter(block, encryptedB[len(encryptedB)-block.BlockSize():]).CryptBlocks(plain, data)
				rotatedB := append(append([]byte(nil), rndB[1:]...), rndB[0])
				if !bytes.Equal(plain[len(rndB):], rotatedB) {
					t.Error("candidate keys were not checked in the expected order")
				}
				if !attempt.match {
					return reply(nil, 0xae)
				}
				rndA := plain[:len(rndB)]
				rotatedA := append(append([]byte(nil), rndA[1:]...), rndA[0])
				encryptedA := make([]byte, len(rotatedA))
				cipher.NewCBCEncrypter(block, data[len(data)-block.BlockSize():]).CryptBlocks(encryptedA, rotatedA)
				return reply(encryptedA, 0)
			}
		}
		t.Errorf("unexpected simulated DESFire command %d", cmd)
		return 0x67, nil
	})
	t.Cleanup(func() {
		if scans.Load() != int32(len(attempts)) || completed.Load() != int32(len(attempts)) {
			t.Errorf("DESFire attempts/cleanups: %d/%d, want %d", scans.Load(), completed.Load(), len(attempts))
		}
	})
	return port
}

func TestDESFireCheckAcceptsExportedGUIDictionary(t *testing.T) {
	dir := t.TempDir()
	first, second, last := bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x22}, 16), bytes.Repeat([]byte{0x33}, 16)
	template, text, dictionary := filepath.Join(dir, "template.json"), filepath.Join(dir, "keys.txt"), filepath.Join(dir, "dictionary.json")
	writeJSONFile(t, template, map[string]any{"id": "aes", "name": "AES keys", "keyLength": 32, "keys": []any{}, "custom": "preserved"})
	if err := os.WriteFile(text, []byte(fmt.Sprintf("%X\n%X\n%X\n", first, second, last)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCardfile([]string{"dict-from-text", "--template", template, "--input", text, "--output", dictionary}); err != nil {
		t.Fatal(err)
	}
	empty, database, exported := filepath.Join(dir, "empty.json"), filepath.Join(dir, "database.json"), filepath.Join(dir, "exported.json")
	if err := os.WriteFile(empty, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runLibrary([]string{"database", "add", "--dictionaries", "--input", empty, "--source", dictionary, "--output", database}); err != nil {
		t.Fatal(err)
	}
	if err := runLibrary([]string{"database", "show", "--dictionaries", "--input", database, "--id", "aes", "--output", exported}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(exported)
	if err != nil || !bytes.Contains(before, []byte(`"custom": "preserved"`)) {
		t.Fatalf("dictionary metadata was lost: %v", err)
	}
	attempts := []desDictionaryAttempt{{first, "aes", false}, {first, "des", false}, {second, "aes", false}, {second, "des", false}, {last, "aes", true}}
	port := desDictionarySimulator(t, attempts)
	output := filepath.Join(dir, "matches.json")
	if err := runWorkflow([]string{"des", "check", "--dictionary", exported, "--key", fmt.Sprintf("%X", first), "--aid", "010203", "--key-number", "2", "--port", port, "--output", output}); err != nil {
		t.Fatal(err)
	}
	assertDESDictionaryMatch(t, output, last, "aes")
	after, err := os.ReadFile(exported)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("DESFire check modified the source dictionary", err)
	}
}

func assertDESDictionaryMatch(t *testing.T, path string, key []byte, algorithm string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	var matches []chameleon.DESFireKeyMatch
	if err != nil || json.Unmarshal(raw, &matches) != nil || len(matches) != 1 || !bytes.Equal(matches[0].Key, key) || matches[0].Algorithm != algorithm || matches[0].UID.String() != "01020304" || matches[0].AID.String() != "010203" || matches[0].KeyNumber != 2 {
		t.Fatalf("invalid DESFire check result %s: %v", raw, err)
	}
}

func TestDESFireCheckPreservesTextAndOtherKeyWidths(t *testing.T) {
	for _, width := range []int{8, 16, 24} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			dir := t.TempDir()
			key := bytes.Repeat([]byte{0x44}, width)
			algorithm := "des"
			if width == 16 {
				algorithm = "aes"
			} else if width == 24 {
				algorithm = "3des"
			}
			for _, format := range []string{"text", "json"} {
				t.Run(format, func(t *testing.T) {
					dictionary := filepath.Join(dir, format)
					if format == "text" {
						if err := os.WriteFile(dictionary, []byte(fmt.Sprintf("# comment\n%X # key\n%X\n", key, key)), 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						writeJSONFile(t, dictionary, chameleon.GUIDictionary{ID: "key", Name: "Key", KeyLength: width * 2, Keys: []chameleon.HexBytes{key, key}})
					}
					port := desDictionarySimulator(t, []desDictionaryAttempt{{key, algorithm, true}})
					output := filepath.Join(dir, format+"-matches.json")
					if err := runWorkflow([]string{"des", "check", "--dictionary", dictionary, "--aid", "010203", "--key-number", "2", "--port", port, "--output", output}); err != nil {
						t.Fatal(err)
					}
					assertDESDictionaryMatch(t, output, key, algorithm)
				})
			}
		})
	}
}

func TestDESFireDictionaryRejectsInvalidInputsBeforeOpening(t *testing.T) {
	for name, data := range map[string]string{
		"invalid-json":   `{"id":`,
		"invalid-schema": `{"id":"d","name":"x","keyLength":32,"keys":[[1,2]]}`,
		"invalid-width":  `{"id":"d","name":"x","keyLength":4,"keys":[[1,2]]}`,
		"invalid-text":   "not-a-key\n",
		"empty":          "# no keys\n",
	} {
		t.Run(name, func(t *testing.T) {
			dictionary := filepath.Join(t.TempDir(), "dictionary")
			if err := os.WriteFile(dictionary, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			err := runWorkflow([]string{"des", "check", "--dictionary", dictionary, "--port", "tcp:127.0.0.1:0"})
			if err == nil || strings.Contains(err.Error(), "dial tcp") {
				t.Fatalf("invalid dictionary reached transport setup: %v", err)
			}
		})
	}
	for _, size := range []int64{dictionaryTextLimit + 1, dictionaryJSONLimit + 1} {
		path := filepath.Join(t.TempDir(), "oversized")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(size); err != nil {
			file.Close()
			t.Fatal(err)
		}
		file.Close()
		if _, err := desfireDictionary(path, nil); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized dictionary accepted: %v", err)
		}
	}
	var text strings.Builder
	for i := 0; i < 10001; i++ {
		fmt.Fprintf(&text, "%016X\n", i)
	}
	path := filepath.Join(t.TempDir(), "too-many.txt")
	if err := os.WriteFile(path, []byte(text.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := desfireDictionary(path, nil); err == nil || !strings.Contains(err.Error(), "1..10000") {
		t.Fatalf("too many DESFire keys accepted: %v", err)
	}
}

func TestDESFireDictionaryTextPreservesMixedKeyOrder(t *testing.T) {
	first := bytes.Repeat([]byte{0x11}, 8)
	explicit := bytes.Repeat([]byte{0x22}, 16)
	last := bytes.Repeat([]byte{0x33}, 24)
	path := filepath.Join(t.TempDir(), "mixed.txt")
	text := fmt.Sprintf("# %X\n%X\n%X # duplicate explicit key\n%X\n%X\n", last, first, explicit, last, first)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := desfireDictionary(path, explicit)
	if err != nil || len(keys) != 3 || !bytes.Equal(keys[0], explicit) || !bytes.Equal(keys[1], first) || !bytes.Equal(keys[2], last) {
		t.Fatalf("mixed-width dictionary order changed: %X %v", keys, err)
	}
}
