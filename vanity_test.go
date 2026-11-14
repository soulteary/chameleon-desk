package chameleon

import "testing"

func deref(t *testing.T, b *bool) bool {
	t.Helper()
	if b == nil {
		t.Fatal("unexpected nil vanity result")
	}
	return *b
}

func TestVanitySAKFromData(t *testing.T) {
	block0 := make([]byte, 16)
	// 4-byte UID: SAK at byte 5.
	block0[5] = 0x08
	if deref(t, VanitySAKFromData([]byte{1, 2, 3, 4}, 0x08, block0)) {
		t.Fatal("plain SAK reported as vanity")
	}
	block0[5] = 0x08 | 0x80
	if !deref(t, VanitySAKFromData([]byte{1, 2, 3, 4}, 0x08, block0)) {
		t.Fatal("vanity SAK not detected")
	}
	// 7-byte UID: SAK at byte 7.
	block0[7] = 0x00 | 0x80
	if !deref(t, VanitySAKFromData([]byte{1, 2, 3, 4, 5, 6, 7}, 0x00, block0)) {
		t.Fatal("7-byte vanity SAK not detected")
	}
	// Neither plain nor vanity → indeterminate.
	block0[5] = 0x11
	if VanitySAKFromData([]byte{1, 2, 3, 4}, 0x08, block0) != nil {
		t.Fatal("unrelated byte reported")
	}
	if VanitySAKFromData([]byte{1, 2}, 0x08, block0) != nil {
		t.Fatal("bad UID length accepted")
	}
}

func TestVanitySAKFromUIDHex(t *testing.T) {
	if deref(t, VanitySAKFromUIDHex("04112233")) {
		t.Fatal("4-byte UID reported as vanity")
	}
	if !deref(t, VanitySAKFromUIDHex("04 11 22 33 44 55 66")) {
		t.Fatal("7-byte UID not vanity")
	}
	if VanitySAKFromUIDHex("0411") != nil {
		t.Fatal("2-byte UID accepted")
	}
}
