package chameleon

import (
	"context"
	"fmt"
	"strings"
)

// ClassicRead selects an expected UID and a key pair for each contiguous sector.
// Completeness is derived; callers do not have to construct a key-map flag.
type ClassicRead struct {
	UID     HexBytes     `json:"uid"`
	Sectors []SectorKeys `json:"sectors"`
}

func (r ClassicRead) KeyMap() (ClassicKeyMap, error) {
	k := ClassicKeyMap{Schema: 1, UID: r.UID, Sectors: r.Sectors}
	k.updateComplete()
	if _, err := classicReadGeometry(k); err != nil {
		return k, err
	}
	return k, nil
}

func classicReadGeometry(k ClassicKeyMap) (uint16, error) {
	if err := k.Validate(); err != nil {
		return 0, err
	}
	tag, ok := map[int]uint16{5: 1000, 16: 1001, 32: 1002, 40: 1003}[len(k.Sectors)]
	if !ok {
		return 0, fmt.Errorf("card geometry requires 5, 16, 32 or 40 sectors")
	}
	for _, s := range k.Sectors {
		if len(s.A) == 0 && len(s.B) == 0 {
			return 0, fmt.Errorf("sector %d needs at least one key", s.Sector)
		}
	}
	return tag, nil
}

func (r ClassicRead) Requirements() ([]Command, error) {
	k, err := r.KeyMap()
	if err != nil {
		return nil, err
	}
	commands := []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdMf1ReadOneBlock}
	// Supplying both keys may require independent authentication of the unused
	// key. Card ACLs are not known at preview time; one-key reads need no auth command.
	for _, s := range k.Sectors {
		if len(s.A) != 0 && len(s.B) != 0 {
			commands = append(commands, CmdMf1AuthOneKeyBlock)
			break
		}
	}
	return commands, nil
}

type Type2Read struct {
	UID   HexBytes `json:"uid"`
	Start int      `json:"start"`
	Count int      `json:"count"`
}

func (r Type2Read) Requirements() ([]Command, error) {
	if len(r.UID) != 4 && len(r.UID) != 7 && len(r.UID) != 10 {
		return nil, fmt.Errorf("expected UID must be 4, 7 or 10 bytes")
	}
	if r.Start < 0 || r.Start > 255 || r.Count < 1 || r.Count > 256-r.Start {
		return nil, fmt.Errorf("page range must fit 0..255")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdHf14aRaw}, nil
}

// ReadType2Range checks the expected UID before and after each four-page READ.
// Only confirmed chunks are returned on failure. It does not infer capacity,
// authenticate passwords or turn the requested range into a complete card image.
func (c *Client) ReadType2Range(ctx context.Context, r Type2Read) (out HexBytes, err error) {
	if _, err = r.Requirements(); err != nil {
		return nil, err
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for page := r.Start; page < r.Start+r.Count; {
			if e := ensureUID(ctx, s, r.UID); e != nil {
				return e
			}
			base := page / 4 * 4
			b, e := s.Raw14A(ctx, []byte{0x30, byte(base)}, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, CheckResponseCRC: true, TimeoutMS: 500})
			if e != nil {
				return e
			}
			if len(b) != 16 {
				return fmt.Errorf("READ returned %d bytes instead of 16", len(b))
			}
			if e = ensureUID(ctx, s, r.UID); e != nil {
				return e
			}
			n := min(4-page%4, r.Start+r.Count-page)
			out = append(out, b[page%4*4:(page%4+n)*4]...)
			page += n
		}
		return nil
	})
	return
}

type LFRead struct {
	Protocol string  `json:"protocol"`
	Format   byte    `json:"format,omitempty"`
	Password *uint32 `json:"password,omitempty"`
}

func (r LFRead) Requirements() ([]Command, error) {
	protocol := strings.ToLower(r.Protocol)
	if protocol == "hid" {
		protocol = "hidprox"
	}
	cmd, ok := lfScanCommands[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported LF read protocol")
	}
	if r.Format != 0 {
		if protocol != "hidprox" {
			return nil, fmt.Errorf("format is only used for HIDProx")
		}
		if _, ok := hidFormats[r.Format]; !ok {
			return nil, fmt.Errorf("unknown HID format")
		}
	}
	if r.Password != nil && protocol != "em4x05" {
		return nil, fmt.Errorf("password is only used for EM4x05")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, cmd}, nil
}

func (c *Client) ReadLF(ctx context.Context, r LFRead) (LFRecord, error) {
	if _, err := r.Requirements(); err != nil {
		return LFRecord{}, err
	}
	password := uint32(0)
	if r.Password != nil {
		password = *r.Password
	}
	return c.ReadLFRecord(ctx, r.Protocol, r.Format, password)
}
