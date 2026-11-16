package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type HIDFormatInfo struct {
	ID          byte   `json:"id"`
	Name        string `json:"name"`
	MaxFacility uint32 `json:"max_facility"`
	MaxNumber   uint64 `json:"max_number"`
	MaxIssue    byte   `json:"max_issue"`
	MaxOEM      uint16 `json:"max_oem"`
}

func HIDFormats() []HIDFormatInfo {
	out := []HIDFormatInfo{}
	for _, v := range hidFormats {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func ParseHIDFormat(name string) (byte, error) {
	for id, v := range hidFormats {
		if strings.EqualFold(name, v.Name) {
			return id, nil
		}
	}
	return 0, fmt.Errorf("unknown HID format %q", name)
}

type HIDProxID struct {
	Format   byte   `json:"format"`
	Facility uint32 `json:"facility"`
	Number   uint64 `json:"number"`
	Issue    byte   `json:"issue"`
	OEM      uint16 `json:"oem"`
}

func (v HIDProxID) Encode() (HexBytes, error) {
	spec, ok := hidFormats[v.Format]
	if !ok || v.Facility > spec.MaxFacility || v.Number > spec.MaxNumber || v.Issue > spec.MaxIssue || v.OEM > spec.MaxOEM {
		return nil, fmt.Errorf("HID fields exceed the selected format")
	}
	b := make(HexBytes, 13)
	b[0] = v.Format
	binary.BigEndian.PutUint32(b[1:5], v.Facility)
	b[5] = byte(v.Number >> 32)
	binary.BigEndian.PutUint32(b[6:10], uint32(v.Number))
	b[10] = v.Issue
	binary.BigEndian.PutUint16(b[11:], v.OEM)
	return b, nil
}
func DecodeHIDProx(b []byte) (HIDProxID, error) {
	if len(b) != 13 {
		return HIDProxID{}, fmt.Errorf("HID structure requires 13 bytes")
	}
	v := HIDProxID{b[0], binary.BigEndian.Uint32(b[1:5]), uint64(b[5])<<32 | uint64(binary.BigEndian.Uint32(b[6:10])), b[10], binary.BigEndian.Uint16(b[11:])}
	_, e := v.Encode()
	return v, e
}

type IDTECKInfo struct {
	Number           uint32 `json:"number"`
	PreambleValid    bool   `json:"preamble_valid"`
	ChecksumValid    bool   `json:"checksum_valid"`
	Checksum         byte   `json:"checksum"`
	ExpectedChecksum byte   `json:"expected_checksum"`
}

func ComposeIDTECK(number uint32) (HexBytes, error) {
	if number > 0xffffff {
		return nil, fmt.Errorf("IDTECK number must fit 24 bits")
	}
	a, b, c := byte(number), byte(number>>8), byte(number>>16)
	return HexBytes{0x49, 0x44, 0x54, 0x4b, a + b + c, a, b, c}, nil
}
func DecodeIDTECK(b []byte) (IDTECKInfo, error) {
	if len(b) != 8 {
		return IDTECKInfo{}, fmt.Errorf("IDTECK frame requires eight bytes")
	}
	sum := b[5] + b[6] + b[7]
	return IDTECKInfo{uint32(b[5]) | uint32(b[6])<<8 | uint32(b[7])<<16, bytes.Equal(b[:4], []byte("IDTK")), b[4] == sum, b[4], sum}, nil
}

type EM4x05Info struct {
	Config   uint32 `json:"config"`
	UID      uint32 `json:"uid"`
	UIDHigh  uint32 `json:"uid_high"`
	IsEM4x69 bool   `json:"is_em4x69"`
	UIDBlock byte   `json:"uid_block"`
}
type LFRecord struct {
	PACRaw          HexBytes     `json:"pac_raw,omitempty"`
	Pyramid         *PyramidInfo `json:"pyramid,omitempty"`
	JablotronNumber *uint64      `json:"jablotron_number,omitempty"`
	Protocol        string       `json:"protocol"`
	TagType         uint16       `json:"tag_type,omitempty"`
	ID              HexBytes     `json:"id"`
	HID             *HIDProxID   `json:"hid,omitempty"`
	IOProx          *IOProxID    `json:"ioprox,omitempty"`
	IDTECK          *IDTECKInfo  `json:"idteck,omitempty"`
	PAC             string       `json:"pac,omitempty"`
	EM4x05          *EM4x05Info  `json:"em4x05,omitempty"`
}

func DecodeLFRecord(protocol string, tag uint16, b []byte) (out LFRecord, err error) {
	out = LFRecord{Protocol: protocol, TagType: tag, ID: append(HexBytes(nil), b...)}
	switch protocol {
	case "hidprox":
		v, e := DecodeHIDProx(b)
		if e != nil {
			return out, e
		}
		out.HID = &v
	case "ioprox":
		v, e := parseIOProx(b)
		if e != nil {
			return out, e
		}
		out.IOProx = &v
	case "idteck":
		v, e := DecodeIDTECK(b)
		if e != nil {
			return out, e
		}
		out.IDTECK = &v
	case "pac":
		if len(b) != 8 {
			return out, fmt.Errorf("PAC ID must have eight ASCII bytes")
		}
		for _, v := range b {
			if v < 32 || v > 126 {
				return out, fmt.Errorf("PAC ID must be printable ASCII")
			}
		}
		out.PAC = string(b)
		out.PACRaw, err = EncodePACRaw(b)
		if err != nil {
			return out, err
		}
	case "em410x", "electra":
		n := 5
		if tag == 104 || protocol == "electra" {
			n = 13
		}
		if len(b) != n {
			return out, fmt.Errorf("EM410x/Electra ID geometry mismatch")
		}
	case "viking":
		if len(b) != 4 {
			return out, fmt.Errorf("Viking ID must have four bytes")
		}
	case "jablotron":
		number, e := DecodeJablotronNumber(b)
		if e != nil {
			return out, e
		}
		out.JablotronNumber = &number
	case "pyramid":
		info, e := DecodePyramid(b)
		if e != nil {
			return out, e
		}
		out.Pyramid = &info
	case "em4x05":
		if len(b) != 14 || b[12] > 1 {
			return out, fmt.Errorf("invalid EM4x05 response")
		}
		out.EM4x05 = &EM4x05Info{binary.BigEndian.Uint32(b), binary.BigEndian.Uint32(b[4:]), binary.BigEndian.Uint32(b[8:]), b[12] != 0, b[13]}
	default:
		return out, fmt.Errorf("unknown LF protocol")
	}
	return out, nil
}
func (c *Client) ReadLFRecord(ctx context.Context, protocol string, format byte, password uint32) (out LFRecord, err error) {
	protocol = strings.ToLower(protocol)
	if protocol == "hid" {
		protocol = "hidprox"
	}
	if protocol == "hidprox" && format != 0 {
		if _, ok := hidFormats[format]; !ok {
			return out, fmt.Errorf("unknown HID format")
		}
	}
	if protocol != "em4x05" {
		v, e := c.ScanLF(ctx, protocol, format)
		if e != nil {
			return out, e
		}
		return DecodeLFRecord(protocol, v.TagType, v.ID)
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		p := make([]byte, 4)
		binary.BigEndian.PutUint32(p, password)
		b, e := s.expect(ctx, CmdEm4x05Scan, p, 0x40)
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) && status.Status == 0x41 {
				return ErrNoCard
			}
			return e
		}
		out, e = DecodeLFRecord(protocol, 0, b)
		return e
	})
	return
}

type LFIdentityInput struct {
	TagType      uint16     `json:"tag_type,omitempty"`
	Protocol     string     `json:"protocol"`
	ID           HexBytes   `json:"id,omitempty"`
	HID          *HIDProxID `json:"hid,omitempty"`
	IOProx       *IOProxID  `json:"ioprox,omitempty"`
	PAC          string     `json:"pac,omitempty"`
	IDTECKNumber *uint32    `json:"idteck_number,omitempty"`
}

func (c *Client) EncodeLFIdentity(ctx context.Context, v LFIdentityInput) (LFRecord, error) {
	protocol := strings.ToLower(v.Protocol)
	if protocol == "hid" {
		protocol = "hidprox"
	}
	if protocol == "em410x" && v.TagType == 104 {
		protocol = "electra"
	}
	b := append(HexBytes(nil), v.ID...)
	tag := uint16(0)
	switch protocol {
	case "hidprox":
		if v.HID != nil {
			var e error
			b, e = v.HID.Encode()
			if e != nil {
				return LFRecord{}, e
			}
		}
	case "ioprox":
		if v.IOProx != nil {
			var result IOProxID
			var e error
			if len(v.IOProx.Raw) > 0 {
				result, e = c.DecodeIOProx(ctx, v.IOProx.Raw)
				if e == nil && (v.IOProx.Version != 0 || v.IOProx.Facility != 0 || v.IOProx.Number != 0) && (result.Version != v.IOProx.Version || result.Facility != v.IOProx.Facility || result.Number != v.IOProx.Number) {
					return LFRecord{}, fmt.Errorf("ioProx raw and structured fields conflict")
				}
			} else {
				result, e = c.ComposeIOProx(ctx, v.IOProx.Version, v.IOProx.Facility, v.IOProx.Number)
			}
			if e != nil {
				return LFRecord{}, e
			}
			b = result.Encoded
		}
	case "pac":
		if v.PAC != "" {
			b = HexBytes(v.PAC)
		}
	case "idteck":
		if v.IDTECKNumber != nil {
			var e error
			b, e = ComposeIDTECK(*v.IDTECKNumber)
			if e != nil {
				return LFRecord{}, e
			}
		} else if len(b) == 4 {
			b = append(HexBytes("IDTK"), b...)
		}
	case "em410x":
		tag = 100
	case "electra":
		tag = 104
	}
	expectedID := v.ID
	if protocol == "idteck" && len(expectedID) == 4 {
		expectedID = append(HexBytes("IDTK"), expectedID...)
	}
	if len(expectedID) > 0 && !bytes.Equal(expectedID, b) {
		return LFRecord{}, fmt.Errorf("raw ID and structured LF fields conflict")
	}
	record, e := DecodeLFRecord(protocol, tag, b)
	if e != nil {
		return record, e
	}
	if protocol == "idteck" && (!record.IDTECK.PreambleValid || !record.IDTECK.ChecksumValid) {
		return record, fmt.Errorf("IDTECK preamble/checksum invalid")
	}
	return record, nil
}
func (c *Client) SlotLFIdentity(ctx context.Context, slot int, protocol string, input *LFIdentityInput, persist bool) (out LFRecord, err error) {
	if protocol == "hid" {
		protocol = "hidprox"
	}
	getProtocol := protocol
	if protocol == "electra" {
		getProtocol = "em410x"
	}
	get, set, e := lfIDCommands(getProtocol)
	if e != nil {
		return out, e
	}
	var encoded LFRecord
	if input != nil {
		copy := *input
		copy.Protocol = protocol
		encoded, e = c.EncodeLFIdentity(ctx, copy)
		if e != nil {
			return out, e
		}
	}
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		a, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
		if e != nil {
			return e
		}
		types, e := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
		if e != nil {
			return e
		}
		if len(a) != 1 || a[0] > 7 || len(types) != 32 {
			return fmt.Errorf("invalid slot LF metadata")
		}
		tag := binary.BigEndian.Uint16(types[int(a[0])*4+2:])
		expected := map[string]uint16{"em410x": 100, "electra": 104, "hidprox": 200, "ioprox": 201, "viking": 170, "pac": 150, "jablotron": 180, "idteck": 310}[protocol]
		if tag != expected && !(protocol == "em410x" && tag >= 100 && tag <= 104) {
			return fmt.Errorf("slot LF type %d does not match %s; no ID written", tag, protocol)
		}
		if input != nil {
			if get == 5001 && ((tag == 104 && len(encoded.ID) != 13) || (tag != 104 && len(encoded.ID) != 5)) {
				return fmt.Errorf("EM410x ID length does not match slot type")
			}
			if _, e = s.expect(ctx, set, encoded.ID, 0x68); e != nil {
				return e
			}
		}
		b, e := s.expect(ctx, get, nil, 0x68)
		if e != nil {
			return e
		}
		if get == CmdEm410xGetEmuId {
			b, e = normalizeEM410xID(tag, b)
			if e != nil {
				return e
			}
		}
		out, e = DecodeLFRecord(protocol, tag, b)
		if e != nil {
			return e
		}
		if input != nil && !bytes.Equal(out.ID, encoded.ID) {
			return fmt.Errorf("LF ID readback mismatch")
		}
		return nil
	})
	return
}

// CloneLF requires explicit old/new T55xx passwords; it never guesses a list.
func (c *Client) CloneLF(ctx context.Context, v LFIdentityInput, newPassword []byte, oldPasswords [][]byte) (LFRecord, error) {
	return c.CloneLFObserved(ctx, v, newPassword, oldPasswords, nil)
}

// CloneLFObserved reports the mutating command boundary after codec, model and
// reader-mode checks. beforeWrite runs once immediately before attempting the
// T55xx write command. It must not call back into the same Client. A callback
// invocation does not prove delivery; subsequent failure leaves the outcome
// unknown. No write command is replayed.
func (c *Client) CloneLFObserved(ctx context.Context, v LFIdentityInput, newPassword []byte, oldPasswords [][]byte, beforeWrite func()) (LFRecord, error) {
	if len(newPassword) != 4 || len(oldPasswords) < 1 || len(oldPasswords) > 64 {
		return LFRecord{}, fmt.Errorf("explicit new password and 1..64 old passwords required")
	}
	for _, p := range oldPasswords {
		if len(p) != 4 {
			return LFRecord{}, fmt.Errorf("old password must have four bytes")
		}
	}
	record, e := c.EncodeLFIdentity(ctx, v)
	if e != nil {
		return record, e
	}
	model, _, e := c.Model(ctx)
	if e != nil {
		return record, e
	}
	if model != 0 {
		return record, fmt.Errorf("LF writes require Chameleon Ultra")
	}
	e = c.writeT55Observed(ctx, record.Protocol, record.ID, newPassword, oldPasswords, beforeWrite)
	return record, e
}

// EncodeLFIdentityOffline encodes host-supported identities without a transport.
// IOProx composition/decoding requires firmware and must use Client.EncodeLFIdentity.
func EncodeLFIdentityOffline(v LFIdentityInput) (LFRecord, error) {
	if strings.EqualFold(v.Protocol, "ioprox") {
		return LFRecord{}, fmt.Errorf("IOProx encoding requires a connected device")
	}
	return (*Client)(nil).EncodeLFIdentity(context.Background(), v)
}
