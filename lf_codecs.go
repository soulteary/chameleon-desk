package chameleon

import (
	"bytes"
	"fmt"
	"math/bits"
)

type PyramidInfo struct {
	FormatLength int     `json:"format_length"`
	SentinelSpan int     `json:"sentinel_span"`
	Facility     *uint32 `json:"facility,omitempty"`
	Number       uint32  `json:"number"`
	ParityErrors int     `json:"parity_errors"`
}

func DecodePyramid(raw []byte) (out PyramidInfo, err error) {
	if len(raw) != 16 {
		return out, fmt.Errorf("Pyramid frame must contain 16 bytes")
	}
	data := make([]byte, 0, 105)
	for _, b := range raw[1:] {
		if bits.OnesCount8(b)%2 != 1 {
			out.ParityErrors++
		}
		for bit := 7; bit >= 1; bit-- {
			data = append(data, b>>uint(bit)&1)
		}
	}
	first := -1
	for i, b := range data {
		if b != 0 {
			first = i
			break
		}
	}
	if first < 0 {
		return out, fmt.Errorf("Pyramid frame has no format sentinel")
	}
	span := len(data) - first - 8
	if span < 1 {
		return out, fmt.Errorf("invalid Pyramid format sentinel")
	}
	read := func(start, count int) uint32 {
		v := uint32(0)
		for _, b := range data[start : start+count] {
			v = v<<1 | uint32(b)
		}
		return v
	}
	out.SentinelSpan = span
	out.FormatLength = span
	out.Number = read(81, 16)
	if span == 26 {
		fc := read(73, 8)
		out.Facility = &fc
	} else if span == 45 {
		out.FormatLength = 42
		fc := read(53, 10)
		out.Facility = &fc
		out.Number = read(63, 32)
	}
	return out, nil
}
func EncodePACRaw(id []byte) (HexBytes, error) {
	if len(id) != 8 {
		return nil, fmt.Errorf("PAC needs eight ASCII bytes")
	}
	for _, v := range id {
		if v < 32 || v > 126 {
			return nil, fmt.Errorf("PAC ID is not printable ASCII")
		}
	}
	payload := append([]byte{2, '2', '0'}, id...)
	checksum := byte(0)
	for _, v := range id {
		checksum ^= v
	}
	payload = append(payload, checksum)
	out := make(HexBytes, 16)
	out[0] = 255
	pos := 8
	set := func(v byte) {
		if v != 0 {
			out[pos/8] |= 0x80 >> uint(pos%8)
		}
		pos++
	}
	for _, v := range payload {
		set(0)
		ones := 0
		for i := 0; i < 7; i++ {
			b := v >> uint(i) & 1
			ones += int(b)
			set(b)
		}
		parity := byte(1)
		if ones%2 == 1 {
			parity = 0
		}
		set(parity)
		set(1)
	}
	return out, nil
}
func DecodePACRaw(raw []byte) (HexBytes, error) {
	if len(raw) != 16 || raw[0] != 255 {
		return nil, fmt.Errorf("invalid PAC sync/size")
	}
	bit := func(pos int) byte { return raw[pos/8] >> uint(7-pos%8) & 1 }
	payload := make([]byte, 12)
	for f := 0; f < 12; f++ {
		start := 8 + f*10
		if bit(start) != 0 || bit(start+9) != 1 {
			return nil, fmt.Errorf("invalid PAC UART framing")
		}
		v := byte(0)
		for i := 0; i < 7; i++ {
			v |= bit(start+1+i) << uint(i)
		}
		if (bits.OnesCount8(v)+int(bit(start+8)))%2 != 1 {
			return nil, fmt.Errorf("invalid PAC odd parity")
		}
		payload[f] = v
	}
	if !bytes.Equal(payload[:3], []byte{2, '2', '0'}) {
		return nil, fmt.Errorf("invalid PAC header")
	}
	sum := byte(0)
	for _, v := range payload[3:11] {
		sum ^= v
	}
	if sum != payload[11] {
		return nil, fmt.Errorf("PAC checksum mismatch")
	}
	return append(HexBytes(nil), payload[3:11]...), nil
}
func DecodeJablotronNumber(id []byte) (uint64, error) {
	if len(id) != 5 {
		return 0, fmt.Errorf("Jablotron ID needs five bytes")
	}
	number := uint64(0)
	for _, b := range id {
		if b>>4 > 9 || b&15 > 9 {
			return 0, fmt.Errorf("invalid Jablotron BCD digit")
		}
		number = number*100 + uint64(b>>4)*10 + uint64(b&15)
	}
	return number, nil
}
