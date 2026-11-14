package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"strings"
)

func desfireDictionary(path string, explicit []byte) ([]chameleon.HexBytes, error) {
	keys := []chameleon.HexBytes{}
	seen := map[string]bool{}
	add := func(key []byte) error {
		if len(key) != 8 && len(key) != 16 && len(key) != 24 {
			return fmt.Errorf("DESFire keys must have 8,16,24 bytes")
		}
		if !seen[string(key)] {
			if len(keys) == 10000 {
				return fmt.Errorf("DESFire dictionary requires 1..10000 distinct keys")
			}
			seen[string(key)] = true
			keys = append(keys, append(chameleon.HexBytes(nil), key...))
		}
		return nil
	}
	if len(explicit) > 0 {
		if err := add(explicit); err != nil {
			return nil, err
		}
	}
	if path != "" {
		raw, err := readDictionaryFile(path)
		if err != nil {
			return nil, err
		}
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
			var dictionary chameleon.GUIDictionary
			if err := json.Unmarshal(raw, &dictionary); err != nil {
				return nil, fmt.Errorf("invalid GUI dictionary: %w", err)
			}
			for _, key := range dictionary.Keys {
				if err := add(key); err != nil {
					return nil, err
				}
			}
		} else {
			if len(raw) > dictionaryTextLimit {
				return nil, fmt.Errorf("dictionary text exceeds 8MiB")
			}
			for _, line := range strings.Split(string(raw), "\n") {
				line, _, _ = strings.Cut(line, "#")
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				key, err := decodeHex(line)
				if err != nil {
					return nil, err
				}
				if err := add(key); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("DESFire dictionary requires 1..10000 explicit keys")
	}
	return keys, nil
}
