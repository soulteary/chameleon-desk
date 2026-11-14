package chameleon

import "testing"

func TestClassicAccessTransport(t *testing.T) {
	// Transport configuration FF 07 80: data blocks free, trailer condition 1.
	a, e := DecodeClassicAccessHex("FF0780")
	if e != nil {
		t.Fatal(e)
	}
	if a.Conditions != [4]int{0, 0, 0, 4} {
		t.Fatalf("conditions %v", a.Conditions)
	}
	for _, b := range a.Blocks {
		if b.Read != "A/B" || b.Write != "A/B" {
			t.Fatalf("data block %d not free: %+v", b.Block, b)
		}
	}
	if a.Trailer.KeyARead != "-" || a.Trailer.KeyAWrite != "A" || a.Trailer.KeyBRead != "A" {
		t.Fatalf("trailer %+v", a.Trailer)
	}
	// The two-byte form (bytes 7-8) decodes the same, skipping redundancy.
	short, e := DecodeClassicAccessHex("0780")
	if e != nil || short.Conditions != a.Conditions {
		t.Fatalf("%v %v", short.Conditions, e)
	}
}

func TestClassicAccessHexRedundancy(t *testing.T) {
	if _, e := DecodeClassicAccessHex("000780"); e == nil {
		t.Fatal("bad redundancy accepted")
	}
	if _, e := DecodeClassicAccessHex("FF"); e == nil {
		t.Fatal("one byte accepted")
	}
	if _, e := DecodeClassicAccessHex("zz0780"); e == nil {
		t.Fatal("non-hex accepted")
	}
}

func TestClassicAccessConditionRoundTrip(t *testing.T) {
	for data := 0; data < 8; data++ {
		for trailer := 0; trailer < 8; trailer++ {
			conditions := [4]int{data, data, data, trailer}
			bytes, e := EncodeClassicAccess(conditions)
			if e != nil {
				t.Fatal(e)
			}
			decoded, e := DecodeClassicAccess(bytes[:])
			if e != nil || decoded.Conditions != conditions {
				t.Fatalf("round trip %v -> %02x -> %v (%v)", conditions, bytes, decoded.Conditions, e)
			}
		}
	}
	if _, e := EncodeClassicAccess([4]int{0, 0, 0, 8}); e == nil {
		t.Fatal("condition 8 accepted")
	}
}
