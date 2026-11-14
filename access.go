package chameleon

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// MIFARE Classic access-condition decoding mirrors the upstream GUI dump
// analyzer: it turns the three access bytes (bytes 6-8 of a sector trailer)
// into the per-block read/write/increment/decrement permissions, and encodes
// four C1/C2/C3 conditions back into the redundant byte form. Pure data.

// ClassicAccess is the decoded access condition of one sector.
type ClassicAccess struct {
	C1         byte                 `json:"c1"`
	C2         byte                 `json:"c2"`
	C3         byte                 `json:"c3"`
	Conditions [4]int               `json:"conditions"` // blocks 0-2 then trailer
	Blocks     []ClassicBlockAccess `json:"blocks"`
	Trailer    ClassicTrailerAccess `json:"trailer"`
}

// ClassicBlockAccess is one data block's permissions, each "A/B", "A", "B" or "-".
type ClassicBlockAccess struct {
	Block     int    `json:"block"`
	Condition int    `json:"condition"`
	Read      string `json:"read"`
	Write     string `json:"write"`
	Increment string `json:"increment"`
	Decrement string `json:"decrement"`
}

// ClassicTrailerAccess is the sector trailer's key and access-byte permissions.
type ClassicTrailerAccess struct {
	Condition int    `json:"condition"`
	KeyARead  string `json:"key_a_read"`
	KeyAWrite string `json:"key_a_write"`
	AccessR   string `json:"access_read"`
	AccessW   string `json:"access_write"`
	KeyBRead  string `json:"key_b_read"`
	KeyBWrite string `json:"key_b_write"`
}

// dataAccess[condition] = {read, write, increment, decrement}.
var dataAccess = [8][4]string{
	{"A/B", "A/B", "A/B", "A/B"},
	{"A/B", "B", "-", "-"},
	{"A/B", "-", "-", "-"},
	{"A/B", "B", "B", "A/B"},
	{"A/B", "-", "-", "A/B"},
	{"B", "-", "-", "-"},
	{"B", "B", "-", "-"},
	{"-", "-", "-", "-"},
}

// trailerAccess[condition] = {keyA{read,write}, access{read,write}, keyB{read,write}}.
var trailerAccess = [8][3][2]string{
	{{"-", "A"}, {"A", "-"}, {"A", "A"}},
	{{"-", "B"}, {"A/B", "-"}, {"-", "B"}},
	{{"-", "-"}, {"A", "-"}, {"A", "-"}},
	{{"-", "-"}, {"A/B", "-"}, {"-", "-"}},
	{{"-", "A"}, {"A", "A"}, {"A", "A"}},
	{{"-", "B"}, {"A/B", "B"}, {"-", "B"}},
	{{"-", "-"}, {"A/B", "B"}, {"-", "-"}},
	{{"-", "-"}, {"A/B", "-"}, {"-", "-"}},
}

// DecodeClassicAccess decodes access bytes. Three bytes (6,7,8) also verify
// the inverted-bit redundancy; two bytes (7,8) skip that check.
func DecodeClassicAccess(bytes []byte) (ClassicAccess, error) {
	var out ClassicAccess
	if len(bytes) != 2 && len(bytes) != 3 {
		return out, fmt.Errorf("access data must be 2 (bytes 7-8) or 3 (bytes 6-8) bytes")
	}
	offset := 0
	if len(bytes) == 3 {
		offset = 1
	}
	c1 := (bytes[offset] >> 4) & 0x0F
	c2 := bytes[offset+1] & 0x0F
	c3 := (bytes[offset+1] >> 4) & 0x0F
	if len(bytes) == 3 {
		invC1 := bytes[0] & 0x0F
		invC2 := (bytes[0] >> 4) & 0x0F
		invC3 := bytes[1] & 0x0F
		if invC1 != (^c1&0x0F) || invC2 != (^c2&0x0F) || invC3 != (^c3&0x0F) {
			return out, fmt.Errorf("invalid inverted access-bit redundancy")
		}
	}
	out.C1, out.C2, out.C3 = c1, c2, c3
	for block := 0; block < 3; block++ {
		bits := ((c1 >> block) & 1) | (((c2 >> block) & 1) << 1) | (((c3 >> block) & 1) << 2)
		out.Conditions[block] = int(bits)
		p := dataAccess[bits]
		out.Blocks = append(out.Blocks, ClassicBlockAccess{Block: block, Condition: int(bits), Read: p[0], Write: p[1], Increment: p[2], Decrement: p[3]})
	}
	tb := ((c1 >> 3) & 1) | (((c2 >> 3) & 1) << 1) | (((c3 >> 3) & 1) << 2)
	out.Conditions[3] = int(tb)
	t := trailerAccess[tb]
	out.Trailer = ClassicTrailerAccess{Condition: int(tb), KeyARead: t[0][0], KeyAWrite: t[0][1], AccessR: t[1][0], AccessW: t[1][1], KeyBRead: t[2][0], KeyBWrite: t[2][1]}
	return out, nil
}

// DecodeClassicAccessHex decodes a 4- or 6-hex-digit access string.
func DecodeClassicAccessHex(s string) (ClassicAccess, error) {
	b, e := hex.DecodeString(strings.TrimSpace(s))
	if e != nil {
		return ClassicAccess{}, fmt.Errorf("access must be hexadecimal")
	}
	return DecodeClassicAccess(b)
}

// EncodeClassicAccess builds access bytes 6-8 (with inverted redundancy) from
// four C1/C2/C3 conditions: data blocks 0-2 then the sector trailer.
func EncodeClassicAccess(conditions [4]int) ([3]byte, error) {
	var c1, c2, c3 byte
	for block := 0; block < 4; block++ {
		v := conditions[block]
		if v < 0 || v > 7 {
			return [3]byte{}, fmt.Errorf("each access condition must be 0..7")
		}
		c1 |= byte(v&1) << block
		c2 |= byte((v>>1)&1) << block
		c3 |= byte((v>>2)&1) << block
	}
	byte6 := ((^c2 & 0x0F) << 4) | (^c1 & 0x0F)
	byte7 := (c1 << 4) | (^c3 & 0x0F)
	byte8 := (c3 << 4) | c2
	return [3]byte{byte6, byte7, byte8}, nil
}
