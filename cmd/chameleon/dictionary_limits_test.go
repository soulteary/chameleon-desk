package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFullSizeDictionaryCLIRoundTrip(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = previous; devnull.Close() }()
	for _, width := range []int{4, 6, 16, 32} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			dir := t.TempDir()
			dict := chameleon.GUIDictionary{ID: "bulk", Name: "Synthetic keys", KeyLength: width * 2, Keys: make([]chameleon.HexBytes, 100000)}
			for i := range dict.Keys {
				key := bytes.Repeat([]byte{255}, width)
				binary.BigEndian.PutUint32(key[width-4:], uint32(i))
				dict.Keys[i] = key
			}
			input, text, output := filepath.Join(dir, "download.json"), filepath.Join(dir, "keys.txt"), filepath.Join(dir, "updated.json")
			// Dictionary downloads and conversions use this same JSON writer.
			if err := result(dict, input, false); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(input)
			if err != nil || info.Size() <= 4<<20 || info.Size() > dictionaryJSONLimit {
				t.Fatalf("fixture must exercise the expanded dictionary limit: %v %v", info, err)
			}
			if err := runCardfile([]string{"dict-to-text", "--input", input, "--output", text}); err != nil {
				t.Fatal(err)
			}
			if err := runCardfile([]string{"dict-from-text", "--input", text, "--template", input, "--output", output}); err != nil {
				t.Fatal(err)
			}
			raw, err := readDictionaryFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip chameleon.GUIDictionary
			if err := json.Unmarshal(raw, &roundTrip); err != nil || len(roundTrip.Keys) != len(dict.Keys) || roundTrip.KeyLength != dict.KeyLength || !bytes.Equal(roundTrip.Keys[99999], dict.Keys[99999]) {
				t.Fatalf("dictionary round trip lost keys: %v", err)
			}
			if width == 6 {
				keys, err := classicDictionary(output, "")
				if err != nil || len(keys) != 100000 || !bytes.Equal(keys[99999], dict.Keys[99999]) {
					t.Fatalf("Classic dictionary cannot read its CLI output: %v", err)
				}
				port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
					switch chameleon.Command(cmd) {
					case chameleon.CmdGetDeviceMode:
						return 0x68, []byte{1}
					case chameleon.CmdHf14aScan:
						return 0, []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
					case chameleon.CmdMf1CheckKeysOfSectors:
						response := make([]byte, 490)
						for bit := 0; bit < 10; bit++ {
							response[bit/8] |= 0x80 >> uint(bit%8)
							copy(response[10+bit*6:], dict.Keys[0])
						}
						return 0, response
					case chameleon.CmdMf1AuthOneKeyBlock:
						return 0, nil
					default:
						t.Errorf("unexpected simulated dictionary command %d", cmd)
						return 0x67, nil
					}
				})
				keyMapPath := filepath.Join(dir, "key-map.json")
				if err := runWorkflow([]string{"mfc", "dictionary", "--dictionary", output, "--sectors", "5", "--port", port, "--output", keyMapPath}); err != nil {
					t.Fatalf("Classic CLI rejected the full-size dictionary: %v", err)
				}
				keyMapJSON, err := os.ReadFile(keyMapPath)
				var keyMap chameleon.ClassicKeyMap
				if err != nil || json.Unmarshal(keyMapJSON, &keyMap) != nil || !keyMap.Complete {
					t.Fatalf("Classic CLI did not complete its simulated dictionary check: %v", err)
				}
			}
			if width == 4 {
				keys, err := t55Candidates(output, "")
				if err != nil || len(keys) != 100000 || !bytes.Equal(keys[99999], dict.Keys[99999]) {
					t.Fatalf("T55 dictionary cannot read its CLI output: %v", err)
				}
			}
			database, updatedDB := filepath.Join(dir, "database.json"), filepath.Join(dir, "database-updated.json")
			if err := os.WriteFile(database, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := runLibrary([]string{"database", "add", "--dictionaries", "--input", database, "--source", output, "--output", updatedDB}); err != nil {
				t.Fatal(err)
			}
			db, err := readGUIDB(updatedDB)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := db.Dictionary("bulk")
			if err != nil || len(saved.Keys) != 100000 || !bytes.Equal(saved.Keys[99999], dict.Keys[99999]) {
				t.Fatalf("database dictionary import lost keys: %v", err)
			}
		})
	}
}

func TestDictionaryFileLimitsRemainBounded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int64
		read  func(string) ([]byte, error)
	}{
		{"card", 4 << 20, readLimitedFile},
		{"dictionary-json", dictionaryJSONLimit, readDictionaryFile},
		{"dictionary-text", dictionaryTextLimit, func(path string) ([]byte, error) { return readFileWithLimit(path, dictionaryTextLimit) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "oversized")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(tc.limit + 1); err != nil {
				file.Close()
				t.Fatal(err)
			}
			file.Close()
			if _, err := tc.read(path); err == nil || !strings.Contains(err.Error(), "input exceeds") {
				t.Fatalf("oversized input accepted: %v", err)
			}
		})
	}
}
