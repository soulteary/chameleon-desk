package chameleon

// Vanity SAK detection mirrors the upstream GUI helper. A "vanity SAK" card
// stores the SAK with bit 0x80 set in block 0, which some readers need for a
// 7-byte UID to be treated like a 4-byte one. These are pure checks.

// VanitySAKFromData reports whether block 0 stores the SAK with the vanity
// bit (0x80) set. It returns nil when the inputs are the wrong size or block 0
// holds neither the plain nor the vanity SAK at the expected position.
func VanitySAKFromData(uid []byte, sak byte, block0 []byte) *bool {
	if len(block0) != 16 || (len(uid) != 4 && len(uid) != 7) {
		return nil
	}
	index := 5
	if len(uid) == 7 {
		index = 7
	}
	stored := block0[index]
	switch stored {
	case sak | 0x80:
		return boolPtr(true)
	case sak:
		return boolPtr(false)
	}
	return nil
}

// VanitySAKFromUIDHex infers the vanity flag from a UID hex string alone: a
// 7-byte (14 hex digit) UID is vanity, a 4-byte one is not. nil for any other
// length. This matches the GUI's edit-dialog default.
func VanitySAKFromUIDHex(uidHex string) *bool {
	clean := 0
	for _, r := range uidHex {
		if r != ' ' {
			clean++
		}
	}
	switch clean {
	case 8:
		return boolPtr(false)
	case 14:
		return boolPtr(true)
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }
