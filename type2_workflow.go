package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
)

// Type2UnknownBytes describes unknown bytes at an absolute page address.
// Returned zeroes from secret pages are never treated as observed credentials.
type Type2UnknownBytes struct {
	Page   int    `json:"page"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Reason string `json:"reason"`
}

type Type2ReadMetadata struct {
	Schema        int                 `json:"schema"`
	Identity      *ClassicIdentity    `json:"identity"`
	Model         string              `json:"model"`
	TagType       uint16              `json:"tag_type"`
	Version       HexBytes            `json:"version"`
	Pages         int                 `json:"pages"`
	UserFirst     int                 `json:"user_first"`
	UserLast      int                 `json:"user_last"`
	Counters      []int               `json:"counters,omitempty"`
	Authenticated bool                `json:"authenticated"`
	Unknown       []Type2UnknownBytes `json:"unknown"`
}

type type2Layout struct {
	name                                    string
	tag                                     uint16
	pages, userLast, passwordPage, packPage int
}

func type2LayoutForVersion(version []byte) (type2Layout, error) {
	if len(version) != 8 || version[0] != 0 || version[1] != 4 || version[4] != 1 || version[5] != 0 || version[7] != 3 {
		return type2Layout{}, fmt.Errorf("unrecognized Type2 GET_VERSION response")
	}
	if version[2] == 4 && version[3] == 2 {
		switch version[6] {
		case 0x0f:
			return type2Layout{"NTAG213", 1100, 45, 39, 43, 44}, nil
		case 0x11:
			return type2Layout{"NTAG215", 1101, 135, 129, 133, 134}, nil
		case 0x13:
			return type2Layout{"NTAG216", 1102, 231, 225, 229, 230}, nil
		}
	}
	if version[2] == 3 && (version[3] == 1 || version[3] == 2) {
		switch version[6] {
		case 0x0b:
			return type2Layout{"Ultralight EV1 640", 1105, 20, 15, 18, 19}, nil
		case 0x0e:
			return type2Layout{"Ultralight EV1 1312", 1106, 41, 35, 39, 40}, nil
		}
	}
	return type2Layout{}, fmt.Errorf("unsupported Type2 model/version; no capacity is inferred from SAK or failed commands")
}

func validateType2Identity(id ClassicIdentity) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if len(id.UID) != 7 || len(*id.ATS) != 0 || *id.SAK != 0 || !bytes.Equal(id.ATQA, []byte{0, 0x44}) {
		return fmt.Errorf("recognized Type2 layouts require a seven-byte UID, SAK 00 and ATQA 0044")
	}
	return nil
}

func (m Type2ReadMetadata) Validate() error {
	layout, err := type2LayoutForVersion(m.Version)
	if err != nil {
		return err
	}
	if m.Schema != 1 || m.Model != layout.name || m.TagType != layout.tag || m.Pages != layout.pages || m.UserFirst != 4 || m.UserLast != layout.userLast || m.Identity == nil {
		return fmt.Errorf("invalid Type2 model metadata")
	}
	if err = validateType2Identity(*m.Identity); err != nil {
		return err
	}
	if len(m.Counters) > 3 {
		return fmt.Errorf("invalid Type2 counter count")
	}
	for _, v := range m.Counters {
		if v < 0 || v > 0xffffff {
			return fmt.Errorf("invalid Type2 counter value")
		}
	}
	seen := map[int]bool{}
	for _, u := range m.Unknown {
		if u.Page < 0 || u.Page >= m.Pages || u.Offset != 0 || u.Length != 4 || seen[u.Page] {
			return fmt.Errorf("invalid Type2 unknown-page mask")
		}
		seen[u.Page] = true
		switch u.Reason {
		case "hidden_credentials":
			if u.Page != layout.passwordPage && u.Page != layout.packPage {
				return fmt.Errorf("hidden-credential mask is outside credential pages")
			}
		case "not_requested", "read_failed_or_protected":
		default:
			return fmt.Errorf("invalid Type2 unknown-page reason")
		}
	}
	for _, page := range []int{layout.passwordPage, layout.packPage} {
		found := false
		for _, u := range m.Unknown {
			if u.Page == page && u.Reason == "hidden_credentials" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("Type2 hidden credential pages must remain unknown")
		}
	}
	return nil
}
func type2UnknownPages(layout type2Layout, start, count, read int) []Type2UnknownBytes {
	out := []Type2UnknownBytes{}
	for page := 0; page < layout.pages; page++ {
		reason := ""
		switch {
		case page == layout.passwordPage || page == layout.packPage:
			reason = "hidden_credentials"
		case page < start || page >= start+count:
			reason = "not_requested"
		case page >= start+read:
			reason = "read_failed_or_protected"
		}
		if reason != "" {
			out = append(out, Type2UnknownBytes{page, 0, 4, reason})
		}
	}
	return out
}

// ValidateType2ReadResult binds model/masks to the containing operation result.
func ValidateType2ReadResult(r ReadResult) error {
	m := r.Type2
	if m == nil {
		return fmt.Errorf("Type2 workflow metadata is missing")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if r.Kind != "type2" || r.Schema != 1 || r.Identity != nil || r.Dump != nil || r.Record != nil || r.Present != nil || !bytes.Equal(m.Identity.UID, r.UID) || r.Start < 0 || r.Requested < 1 || r.Start+r.Requested > m.Pages || r.Read < 0 || r.Read > r.Requested || len(r.Data) != r.Read*4 || (r.Complete && r.Read != r.Requested) {
		return fmt.Errorf("Type2 metadata differs from operation bounds or identity")
	}
	layout, _ := type2LayoutForVersion(m.Version)
	if r.Complete && r.Start == 0 && r.Read == m.Pages {
		if err := validateType2Counters(layout, m.Counters, r.Data); err != nil {
			return err
		}
	}
	expected := type2UnknownPages(layout, r.Start, r.Requested, r.Read)
	if len(expected) != len(m.Unknown) {
		return fmt.Errorf("Type2 masks do not describe all unobserved pages")
	}
	masks := map[int]Type2UnknownBytes{}
	for _, u := range m.Unknown {
		masks[u.Page] = u
	}
	for _, u := range expected {
		if masks[u.Page] != u {
			return fmt.Errorf("Type2 mask differs from operation coverage")
		}
		if u.Page >= r.Start && u.Page < r.Start+r.Read {
			for _, b := range r.Data[(u.Page-r.Start)*4 : (u.Page-r.Start+1)*4] {
				if b != 0 {
					return fmt.Errorf("Type2 hidden data must be redacted in the read result")
				}
			}
		}
	}
	return nil
}

// ApplyType2ReadMasks preserves absolute offsets in a draft, including hidden
// pages that returned zeroes. Metadata may describe pages outside the range.
func ApplyType2ReadMasks(m *Type2ReadMetadata, rows []string) ([]string, error) {
	if m == nil {
		return rows, nil
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	out := append([]string(nil), rows...)
	for _, u := range m.Unknown {
		if u.Page < len(out) {
			if len(out[u.Page]) != 8 {
				return nil, fmt.Errorf("Type2 row %d has incorrect width", u.Page)
			}
			out[u.Page] = strings.Repeat("??", 4)
		}
	}
	return out, nil
}

type Type2WorkflowRead struct {
	UID      HexBytes `json:"uid"`
	Start    int      `json:"start"`
	Count    int      `json:"count"`
	Full     bool     `json:"full"`
	Password HexBytes `json:"password,omitempty"`
}

func (r Type2WorkflowRead) Requirements() ([]Command, error) {
	if len(r.UID) != 7 {
		return nil, fmt.Errorf("modeled Type2 reads require the expected seven-byte UID")
	}
	if len(r.Password) != 0 && len(r.Password) != 4 {
		return nil, fmt.Errorf("Type2 password must be four bytes or absent")
	}
	if r.Full {
		if r.Start != 0 || r.Count != 0 {
			return nil, fmt.Errorf("full Type2 read must omit start/count")
		}
	} else if r.Start < 0 || r.Start > 255 || r.Count < 1 || r.Count > 256-r.Start {
		return nil, fmt.Errorf("Type2 range must fit 0..255")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdHf14aRaw, CmdHf14aSetFieldOff}, nil
}

func type2Observe(ctx context.Context, s *ReaderSession, uid []byte, identity *ClassicIdentity) error {
	b, err := s.expect(ctx, CmdHf14aScan, nil, 0)
	if err != nil {
		return err
	}
	cards, err := parseCards(b)
	if err != nil {
		return err
	}
	if len(cards) != 1 || !bytes.Equal(cards[0].UID, uid) {
		return fmt.Errorf("Type2 identity changed; exactly one expected UID is required")
	}
	card := cards[0]
	atqa := make(HexBytes, 2)
	binary.BigEndian.PutUint16(atqa, card.ATQA)
	ats := append(HexBytes{}, card.ATS...)
	sak := card.SAK
	current := ClassicIdentity{Schema: 1, UID: append(HexBytes(nil), card.UID...), ATQA: atqa, SAK: &sak, ATS: &ats}
	if err := current.Validate(); err != nil {
		return err
	}
	if identity.Schema == 0 {
		*identity = current
		return nil
	}
	if !bytes.Equal(identity.UID, current.UID) || !bytes.Equal(identity.ATQA, current.ATQA) || *identity.SAK != sak || !bytes.Equal(*identity.ATS, ats) {
		return fmt.Errorf("Type2 identity changed during operation")
	}
	return nil
}
func type2Version(ctx context.Context, s *ReaderSession) (HexBytes, error) {
	b, e := s.Raw14A(ctx, []byte{0x60}, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, CheckResponseCRC: true, TimeoutMS: 500})
	if e != nil {
		return nil, e
	}
	if len(b) != 8 {
		return nil, fmt.Errorf("GET_VERSION returned %d bytes; capacity remains unknown", len(b))
	}
	return b, nil
}
func type2FastRead(ctx context.Context, s *ReaderSession, password []byte, first, last int) (HexBytes, error) {
	// The RF frame includes two CRC bytes; the RC522 receive FIFO is 64 bytes.
	if first < 0 || last < first || last > 255 || (last-first+1)*4+2 > 64 {
		return nil, fmt.Errorf("FAST_READ range must fit the 64-byte RF receive FIFO including CRC")
	}
	if len(password) > 0 {
		if _, err := type2Password(ctx, s, password); err != nil {
			return nil, err
		}
	}
	options := RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 500}
	if len(password) == 0 {
		options.ActivateField = true
		options.AutoSelect = true
	}
	data, err := s.Raw14A(ctx, []byte{0x3a, byte(first), byte(last)}, options)
	if err != nil {
		return nil, err
	}
	if len(data) != (last-first+1)*4 {
		return nil, fmt.Errorf("FAST_READ returned %d bytes; expected %d", len(data), (last-first+1)*4)
	}
	return data, nil
}

// ReadType2Workflow discovers a recognized GET_VERSION layout and reads a
// bounded range/full memory without rollover. Failed authentication is never
// retried. Successful authentication resets known AUTHLIM failure counters.
func (c *Client) ReadType2Workflow(ctx context.Context, input Type2WorkflowRead) (out ReadResult, err error) {
	if _, err = input.Requirements(); err != nil {
		return out, err
	}
	out = ReadResult{Schema: 1, Kind: "type2", UID: append(HexBytes(nil), input.UID...), Start: input.Start, Requested: input.Count}
	var layout type2Layout
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		var identity ClassicIdentity
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		version, e := type2Version(ctx, s)
		if e != nil {
			return e
		}
		layout, e = type2LayoutForVersion(version)
		if e != nil {
			return e
		}
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		out.Type2 = &Type2ReadMetadata{Schema: 1, Identity: &identity, Model: layout.name, TagType: layout.tag, Version: append(HexBytes(nil), version...), Pages: layout.pages, UserFirst: 4, UserLast: layout.userLast, Authenticated: false}
		if input.Full {
			out.Start = 0
			out.Requested = layout.pages
		}
		out.Type2.Unknown = type2UnknownPages(layout, out.Start, out.Requested, 0)
		if e = out.Type2.Validate(); e != nil {
			return e
		}
		if out.Start+out.Requested > layout.pages {
			return fmt.Errorf("requested pages exceed identified %s capacity", layout.name)
		}
		for page := out.Start; page < out.Start+out.Requested; {
			if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
				return e
			}
			// FAST_READ carries two CRC bytes on RF. Sixteen pages would be
			// 66 bytes and exceed the reader's 64-byte receive FIFO.
			last := min(page+14, out.Start+out.Requested-1)
			data, e := type2FastRead(ctx, s, input.Password, page, last)
			if e != nil {
				return e
			}
			if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
				return e
			}
			for hidden := max(page, layout.passwordPage); hidden <= min(last, layout.packPage); hidden++ {
				for i := 0; i < 4; i++ {
					data[(hidden-page)*4+i] = 0
				}
			}
			out.Data = append(out.Data, data...)
			out.Read += last - page + 1
			if len(input.Password) > 0 {
				out.Type2.Authenticated = true
			}
			page = last + 1
			out.Type2.Unknown = type2UnknownPages(layout, out.Start, out.Requested, out.Read)
		}
		if out.Start == 0 && out.Read == layout.pages {
			for _, index := range type2CounterIndices(layout, out.Data) {
				if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
					return e
				}
				if len(input.Password) > 0 {
					if _, e = type2Password(ctx, s, input.Password); e != nil {
						return e
					}
				}
				opts := RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 500}
				if len(input.Password) == 0 {
					opts.ActivateField = true
					opts.AutoSelect = true
				}
				counter, e := s.Raw14A(ctx, []byte{0x39, index}, opts)
				if e != nil {
					return e
				}
				if len(counter) != 3 {
					return fmt.Errorf("READ_CNT returned %d bytes", len(counter))
				}
				if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
					return e
				}
				out.Type2.Counters = append(out.Type2.Counters, int(counter[0])|int(counter[1])<<8|int(counter[2])<<16)
			}
		}
		final, e := type2Version(ctx, s)
		if e != nil {
			return e
		}
		if !bytes.Equal(final, version) {
			return fmt.Errorf("Type2 model changed during read")
		}
		return type2Observe(ctx, s, input.UID, &identity)
	})
	out.Complete = err == nil
	return out, err
}

// Type2PageWrite changes one ordinary user page. Expected is a mandatory
// compare-before-write value from an explicit backup or current read.
type Type2PageWrite struct {
	UID      HexBytes `json:"uid"`
	Page     int      `json:"page"`
	Data     HexBytes `json:"data"`
	Expected HexBytes `json:"expected"`
	Password HexBytes `json:"password,omitempty"`
}

func (r Type2PageWrite) Requirements() ([]Command, error) {
	if len(r.UID) != 7 || r.Page < 4 || r.Page > 225 || len(r.Data) != 4 || len(r.Expected) != 4 || (len(r.Password) != 0 && len(r.Password) != 4) {
		return nil, fmt.Errorf("Type2 ordinary-page write requires expected seven-byte UID, page>=4, four-byte data/expected values and optional four-byte password")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdHf14aRaw, CmdHf14aSetFieldOff}, nil
}

type Type2PageWriteResult struct {
	Schema          int      `json:"schema"`
	Model           string   `json:"model,omitempty"`
	UID             HexBytes `json:"uid"`
	Page            int      `json:"page"`
	Before          HexBytes `json:"before,omitempty"`
	After           HexBytes `json:"after,omitempty"`
	WriteAttempted  bool     `json:"write_attempted"`
	Acknowledged    bool     `json:"acknowledged"`
	ReadbackMatches bool     `json:"readback_matches"`
	Verified        bool     `json:"verified"`
	Unchanged       bool     `json:"unchanged"`
}

func (c *Client) WriteType2UserPage(ctx context.Context, input Type2PageWrite) (Type2PageWriteResult, error) {
	return c.WriteType2UserPageObserved(ctx, input, nil)
}

// WriteType2UserPageObserved performs one WRITE at most. beforeWrite is called
// immediately before that command, after preflight/authentication, for task
// bookkeeping only; it must not call back into this Client. No write is retried.
func (c *Client) WriteType2UserPageObserved(ctx context.Context, input Type2PageWrite, beforeWrite func()) (Type2PageWriteResult, error) {
	return c.type2UserPage(ctx, input, beforeWrite, false)
}

// Type2PageWritePlan contains no password or credential response.
type Type2PageWritePlan struct {
	UID    HexBytes `json:"uid"`
	Model  string   `json:"model"`
	Page   int      `json:"page"`
	Before HexBytes `json:"before"`
	After  HexBytes `json:"after"`
}

// PlanType2UserPage probes model/configuration and checks the expected current
// page without issuing WRITE. Execution repeats these checks against live state.
func (c *Client) PlanType2UserPage(ctx context.Context, input Type2PageWrite) (Type2PageWritePlan, error) {
	out, err := c.type2UserPage(ctx, input, nil, true)
	return Type2PageWritePlan{UID: out.UID, Model: out.Model, Page: out.Page, Before: out.Before, After: append(HexBytes(nil), input.Data...)}, err
}

func (c *Client) type2UserPage(ctx context.Context, input Type2PageWrite, beforeWrite func(), planOnly bool) (out Type2PageWriteResult, err error) {
	if _, err = input.Requirements(); err != nil {
		return out, err
	}
	out = Type2PageWriteResult{Schema: 1, UID: append(HexBytes(nil), input.UID...), Page: input.Page}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		var identity ClassicIdentity
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		version, e := type2Version(ctx, s)
		if e != nil {
			return e
		}
		layout, e := type2LayoutForVersion(version)
		if e != nil {
			return e
		}
		out.Model = layout.name
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		ats := identity.ATS
		if len(identity.UID) != 7 || identity.SAK == nil || *identity.SAK != 0 || !bytes.Equal(identity.ATQA, []byte{0, 0x44}) || ats == nil || len(*ats) != 0 {
			return fmt.Errorf("Type2 write identity does not match the modeled layout")
		}
		if input.Page > layout.userLast {
			return fmt.Errorf("page %d is outside %s ordinary user pages 4..%d; manufacturer, OTP/lock, config and credential pages are excluded", input.Page, layout.name, layout.userLast)
		}
		readBounded := func(first, last int) (HexBytes, error) {
			if e := type2Observe(ctx, s, input.UID, &identity); e != nil {
				return nil, e
			}
			data, e := type2FastRead(ctx, s, input.Password, first, last)
			if e != nil {
				return nil, e
			}
			if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
				return nil, e
			}
			return data, nil
		}
		if layout.tag >= 1100 && layout.tag <= 1102 {
			cfg, e := readBounded(layout.passwordPage-2, layout.passwordPage-1)
			if e != nil {
				return e
			}
			if cfg[0]&0xc0 != 0 && cfg[2] > 3 {
				return fmt.Errorf("Type2 ASCII mirror is enabled; virtual readback cannot verify a physical-page write")
			}
		}
		data, e := readBounded(input.Page, input.Page)
		if e != nil {
			return e
		}
		out.Before = append(HexBytes(nil), data...)
		if !bytes.Equal(data, input.Expected) {
			return fmt.Errorf("Type2 page differs from expected backup bytes; write not attempted")
		}
		final, e := type2Version(ctx, s)
		if e != nil {
			return e
		}
		if !bytes.Equal(final, version) {
			return fmt.Errorf("Type2 model changed before write")
		}
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		if planOnly {
			return nil
		}
		if bytes.Equal(input.Data, input.Expected) {
			out.Unchanged = true
			out.After = append(HexBytes(nil), data...)
			out.ReadbackMatches = true
			return nil
		}
		if len(input.Password) > 0 {
			if _, e = type2Password(ctx, s, input.Password); e != nil {
				return e
			}
		}
		options := RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 500}
		if len(input.Password) == 0 {
			options.ActivateField = true
			options.AutoSelect = true
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		out.WriteAttempted = true
		if beforeWrite != nil {
			beforeWrite()
		}
		reply, e := s.Raw14A(ctx, append([]byte{0xa2, byte(input.Page)}, input.Data...), options)
		if e != nil {
			return e
		}
		if len(reply) != 1 || reply[0]&15 != 10 {
			return fmt.Errorf("Type2 WRITE was not acknowledged; do not automatically replay")
		}
		out.Acknowledged = true
		data, e = readBounded(input.Page, input.Page)
		if e != nil {
			return e
		}
		out.After = append(HexBytes(nil), data...)
		if !bytes.Equal(data, input.Data) {
			return fmt.Errorf("Type2 write readback differs from requested bytes")
		}
		final, e = type2Version(ctx, s)
		if e != nil {
			return e
		}
		if !bytes.Equal(final, version) {
			return fmt.Errorf("Type2 model changed after write")
		}
		if e = type2Observe(ctx, s, input.UID, &identity); e != nil {
			return e
		}
		out.ReadbackMatches = true
		return nil
	})
	out.Verified = err == nil && out.ReadbackMatches
	return out, err
}
