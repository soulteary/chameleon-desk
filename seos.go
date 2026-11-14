package chameleon

import (
	"context"
	"encoding/binary"
	"fmt"
)

type SEOSData struct {
	Data                HexBytes `json:"data"`
	OID                 HexBytes `json:"oid"`
	Tag                 HexBytes `json:"tag"`
	Diversifier         HexBytes `json:"diversifier"`
	HashAlgorithm       byte     `json:"hash_algorithm"`
	EncryptionAlgorithm byte     `json:"encryption_algorithm"`
}

func (v SEOSData) payload() ([]byte, error) {
	out := []byte{}
	for i, b := range []HexBytes{v.Data, v.OID, v.Tag, v.Diversifier} {
		if len(b) > []int{255, 32, 2, 16}[i] {
			return nil, fmt.Errorf("SEOS field %d exceeds firmware capacity", i)
		}
		out = append(out, byte(len(b)))
		out = append(out, b...)
	}
	return append(out, v.HashAlgorithm, v.EncryptionAlgorithm), nil
}
func ParseSEOSData(b []byte) (SEOSData, error) {
	fields := make([]HexBytes, 4)
	for i := range fields {
		if len(b) < 1 || int(b[0]) > len(b)-1 {
			return SEOSData{}, fmt.Errorf("truncated SEOS field")
		}
		n := int(b[0])
		fields[i] = append(HexBytes(nil), b[1:1+n]...)
		b = b[1+n:]
	}
	if len(b) != 2 {
		return SEOSData{}, fmt.Errorf("invalid SEOS algorithm suffix")
	}
	v := SEOSData{fields[0], fields[1], fields[2], fields[3], b[0], b[1]}
	_, e := v.payload()
	return v, e
}
func activeSEOS(ctx context.Context, s *ReaderSession) error {
	a, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
	if e != nil {
		return e
	}
	b, e := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
	if e != nil {
		return e
	}
	if len(a) != 1 || a[0] > 7 || len(b) != 32 || binary.BigEndian.Uint16(b[int(a[0])*4:]) != 3001 {
		return fmt.Errorf("active HF slot must already be SEOS type 3001")
	}
	return nil
}
func (c *Client) SEOSData(ctx context.Context) (out SEOSData, err error) {
	err = c.Emulator(ctx, func(s *ReaderSession) error {
		if e := activeSEOS(ctx, s); e != nil {
			return e
		}
		b, e := s.expect(ctx, CmdSeosReadEmuData, nil, 0x68)
		if e != nil {
			return e
		}
		out, e = ParseSEOSData(b)
		return e
	})
	return
}
func (c *Client) SetSEOSData(ctx context.Context, v SEOSData) error {
	p, e := v.payload()
	if e != nil {
		return e
	}
	return c.Emulator(ctx, func(s *ReaderSession) error {
		if e := activeSEOS(ctx, s); e != nil {
			return e
		}
		_, e := s.expect(ctx, CmdSeosWriteEmuData, p, 0x68)
		return e
	})
}
func (c *Client) SetSEOSKeys(ctx context.Context, authentication, encryption, mac []byte) error {
	if len(authentication) != 16 || len(encryption) != 16 || len(mac) != 16 {
		return fmt.Errorf("SEOS requires three explicit 16-byte keys")
	}
	p := append(append(append([]byte(nil), authentication...), encryption...), mac...)
	return c.Emulator(ctx, func(s *ReaderSession) error {
		if e := activeSEOS(ctx, s); e != nil {
			return e
		}
		_, e := s.expect(ctx, CmdSeosWriteEmuKeys, p, 0x68)
		return e
	})
}
