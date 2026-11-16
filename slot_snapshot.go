package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// SlotReadRequest binds a read to the observed active slot and selected channel.
// It does not select slots, change mode, initialize memory or save to flash.
type SlotReadRequest struct {
	Slot    int    `json:"slot"`
	Sense   Sense  `json:"sense"`
	TagType uint16 `json:"tag_type"`
}

func (r SlotReadRequest) geometry() (units, width int, err error) {
	if r.Slot < 1 || r.Slot > 8 {
		return 0, 0, fmt.Errorf("observed slot must be 1..8")
	}
	if r.Sense == SenseHF {
		if n, ok := guiClassicBlocks[r.TagType]; ok {
			return n, 16, nil
		}
		if n, ok := guiType2Pages[r.TagType]; ok {
			return n, 4, nil
		}
	} else if r.Sense == SenseLF {
		if protocol, ok := guiLFProtocols[r.TagType]; ok {
			n := guiLFLengths[protocol]
			if r.TagType == 104 {
				n = 13
			}
			return 1, n, nil
		}
	}
	return 0, 0, fmt.Errorf("selected channel/type has no supported slot snapshot layout")
}
func (r SlotReadRequest) Requirements() ([]Command, error) {
	_, width, e := r.geometry()
	if e != nil {
		return nil, e
	}
	commands := []Command{CmdGetDeviceCapabilities, CmdGetDeviceMode, CmdGetActiveSlot, CmdGetSlotInfo, CmdGetEnabledSlots, CmdGetSlotTagNick}
	if r.Sense == SenseHF {
		commands = append(commands, CmdHf14aGetAntiCollData)
		if width == 16 {
			commands = append(commands, CmdMf1ReadEmuBlockData)
		} else {
			commands = append(commands, CmdMf0NtagGetPageCount, CmdMf0NtagReadEmuPageData)
		}
	} else {
		get, _, e := lfBackupCommands(r.TagType)
		if e != nil {
			return nil, e
		}
		commands = append(commands, get)
	}
	return uniqueCommands(commands), nil
}

type SlotUnknownRange struct {
	Start  int    `json:"start"`
	Count  int    `json:"count"`
	Reason string `json:"reason"`
}

// SlotSnapshot is an observation of emulator RAM, not a physical-card read or
// a full configuration/flash backup. Unknown suffixes never become zero bytes.
type SlotSnapshot struct {
	MetadataRead  bool               `json:"metadata_read"`
	Schema        int                `json:"schema"`
	Source        string             `json:"source"`
	Slot          int                `json:"slot"`
	Sense         Sense              `json:"sense"`
	TagType       uint16             `json:"tag_type"`
	Enabled       bool               `json:"enabled"`
	Nickname      *string            `json:"nickname"`
	Identity      *ClassicIdentity   `json:"identity,omitempty"`
	Record        *LFRecord          `json:"record,omitempty"`
	UnitBytes     int                `json:"unit_bytes"`
	Units         int                `json:"units"`
	Read          int                `json:"units_read"`
	Data          HexBytes           `json:"data"`
	Unknown       []SlotUnknownRange `json:"unknown_ranges"`
	Complete      bool               `json:"complete"`
	SourceChanged bool               `json:"source_changed"`
	Uncaptured    []string           `json:"uncaptured"`
	SHA256        string             `json:"sha256"`
}

func (s SlotSnapshot) digest() string { s.SHA256 = ""; b, _ := json.Marshal(s); return imageDigest(b) }
func slotUncaptured() []string {
	return []string{"emulator_configuration", "version_signature_counters", "flash_persistence", "physical_source_provenance"}
}
func slotUnknown(read, units int) []SlotUnknownRange {
	if read < units {
		return []SlotUnknownRange{{read, units - read, "not_read_or_unconfirmed"}}
	}
	return []SlotUnknownRange{}
}
func (s SlotSnapshot) Validate() error {
	n, width, e := (SlotReadRequest{Slot: s.Slot, Sense: s.Sense, TagType: s.TagType}).geometry()
	if e != nil {
		return e
	}
	if s.Schema != 1 || s.Source != "emulator_slot" || s.Units != n || s.UnitBytes != width || s.Read < 0 || s.Read > n || len(s.Data) != s.Read*width || s.SHA256 != s.digest() {
		return fmt.Errorf("invalid slot snapshot geometry, counters or checksum")
	}
	if !reflect.DeepEqual(s.Unknown, slotUnknown(s.Read, s.Units)) || !reflect.DeepEqual(s.Uncaptured, slotUncaptured()) {
		return fmt.Errorf("slot snapshot coverage/unknown markers are invalid")
	}
	if s.Complete && (s.Read != s.Units || s.SourceChanged) {
		return fmt.Errorf("incomplete slot snapshot cannot claim completion")
	}
	if s.Nickname != nil && (!utf8.ValidString(*s.Nickname) || len(*s.Nickname) > 32) {
		return fmt.Errorf("invalid slot nickname")
	}
	if s.Sense == SenseHF {
		if s.Record != nil {
			return fmt.Errorf("LF record is not valid on an HF slot")
		}
		if s.Identity != nil {
			if e := s.Identity.Validate(); e != nil {
				return e
			}
		}
		if s.Read > 0 && !s.MetadataRead {
			return fmt.Errorf("confirmed slot data requires captured metadata")
		}
		if s.Read > 0 && s.Identity == nil {
			return fmt.Errorf("confirmed HF slot data requires captured identity")
		}
	} else {
		if s.Identity != nil {
			return fmt.Errorf("HF identity is not valid on an LF slot")
		}
		if s.Read > 0 {
			if !s.MetadataRead {
				return fmt.Errorf("confirmed slot data requires captured metadata")
			}
			if s.Record == nil || s.Record.Protocol != guiLFProtocols[s.TagType] || s.Record.TagType != s.TagType || !bytes.Equal(s.Record.ID, s.Data) {
				return fmt.Errorf("LF snapshot identity differs from data")
			}
			record, e := DecodeLFRecord(s.Record.Protocol, s.TagType, s.Data)
			if e != nil || !reflect.DeepEqual(record, *s.Record) {
				return fmt.Errorf("invalid LF snapshot record")
			}
		} else if s.Record != nil {
			return fmt.Errorf("unconfirmed LF data cannot carry a confirmed record")
		}
	}
	return nil
}
func (s SlotSnapshot) DraftRows() ([]string, error) {
	if e := s.Validate(); e != nil {
		return nil, e
	}
	if s.Read == 0 || s.SourceChanged {
		return nil, fmt.Errorf("slot import requires confirmed data without a detected source change")
	}
	rows := make([]string, s.Units)
	for i := range rows {
		rows[i] = strings.Repeat("??", s.UnitBytes)
		if i < s.Read {
			rows[i] = strings.ToUpper(hex.EncodeToString(s.Data[i*s.UnitBytes : (i+1)*s.UnitBytes]))
		}
	}
	return rows, nil
}

var errSlotSourceChanged = errors.New("slot source changed")

type slotObservation struct {
	slot     int
	tag      uint16
	enabled  bool
	nickname *string
	identity *ClassicIdentity
}

func (c *Client) observeCurrentSlot(ctx context.Context, r SlotReadRequest) (out slotObservation, err error) {
	q := func(cmd Command, p ...byte) ([]byte, error) { return c.exchange(ctx, uint16(cmd), p, 0x68) }
	mode, e := q(CmdGetDeviceMode)
	if e != nil {
		return out, e
	}
	if len(mode) != 1 {
		return out, fmt.Errorf("invalid device mode response")
	}
	if mode[0] != 0 {
		return out, fmt.Errorf("%w: current-slot reads require emulator mode; mode was not changed", errSlotSourceChanged)
	}
	active, e := q(CmdGetActiveSlot)
	if e != nil {
		return out, e
	}
	if len(active) != 1 || active[0] > 7 {
		return out, fmt.Errorf("invalid active slot")
	}
	if int(active[0])+1 != r.Slot {
		return out, fmt.Errorf("%w: active slot changed; refresh slot information", errSlotSourceChanged)
	}
	types, e := q(CmdGetSlotInfo)
	if e != nil {
		return out, e
	}
	if len(types) != 32 {
		return out, fmt.Errorf("invalid slot types")
	}
	flags, e := q(CmdGetEnabledSlots)
	if e != nil {
		return out, e
	}
	if len(flags) != 16 {
		return out, fmt.Errorf("invalid slot flags")
	}
	offset := int(active[0]) * 4
	flag := int(active[0]) * 2
	if r.Sense == SenseLF {
		offset += 2
		flag++
	}
	out.slot = r.Slot
	out.tag = binary.BigEndian.Uint16(types[offset:])
	if flags[flag] > 1 {
		return out, fmt.Errorf("invalid channel enable flag")
	}
	out.enabled = flags[flag] == 1
	if out.tag != r.TagType {
		return out, fmt.Errorf("%w: slot type changed; refresh slot information", errSlotSourceChanged)
	}
	session := &ReaderSession{c: c}
	defer func() { session.closed = true }()
	out.nickname, e = slotNickname(ctx, session, r.Slot, r.Sense)
	if e != nil {
		return out, e
	}
	if r.Sense == SenseHF {
		raw, e := q(CmdHf14aGetAntiCollData)
		if e != nil {
			return out, e
		}
		cards, e := parseCards(raw)
		if e != nil || len(cards) != 1 {
			return out, fmt.Errorf("invalid slot anticollision identity")
		}
		card := cards[0]
		atqa := make(HexBytes, 2)
		binary.BigEndian.PutUint16(atqa, card.ATQA)
		sak, ats := card.SAK, append(HexBytes{}, card.ATS...)
		out.identity = &ClassicIdentity{Schema: 1, UID: card.UID, ATQA: atqa, SAK: &sak, ATS: &ats}
		if e := out.identity.Validate(); e != nil {
			return out, e
		}
	}
	return out, nil
}

// ReadCurrentSlot reads only the current slot in emulator mode. Each accepted
// chunk has matching source metadata before/after it. RF may still mutate RAM;
// this is not an atomic snapshot, a flash backup or physical-card evidence.
func (c *Client) ReadCurrentSlot(ctx context.Context, r SlotReadRequest) (out ReadResult, err error) {
	units, width, e := r.geometry()
	if e != nil {
		return out, e
	}
	snapshot := &SlotSnapshot{Schema: 1, Source: "emulator_slot", Slot: r.Slot, Sense: r.Sense, TagType: r.TagType, Units: units, UnitBytes: width, Uncaptured: slotUncaptured(), Data: HexBytes{}}
	out = ReadResult{Schema: 1, Kind: "slot", Requested: units, Slot: snapshot}
	defer func() {
		snapshot.Read = len(snapshot.Data) / width
		snapshot.Complete = err == nil && snapshot.Read == units
		out.Read = snapshot.Read
		out.Complete = snapshot.Complete
		snapshot.Unknown = slotUnknown(snapshot.Read, units)
		snapshot.SHA256 = snapshot.digest()
	}()
	if c.transportInfo.NativeUSBMaintenance {
		return out, ErrUSBBridgeRequired
	}
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	e = c.acquire(queued)
	cancel()
	if e != nil {
		return out, e
	}
	defer c.release()
	required, _ := r.Requirements()
	caps, e := c.exchange(ctx, uint16(CmdGetDeviceCapabilities), nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(caps)%2 != 0 {
		return out, fmt.Errorf("invalid capabilities")
	}
	available := map[Command]bool{}
	for i := 0; i < len(caps); i += 2 {
		available[Command(binary.BigEndian.Uint16(caps[i:]))] = true
	}
	for _, cmd := range required {
		if !available[cmd] {
			return out, fmt.Errorf("slot snapshot requires firmware command %d", cmd)
		}
	}
	initial, e := c.observeCurrentSlot(ctx, r)
	if e != nil {
		return out, e
	}
	snapshot.MetadataRead = true
	snapshot.Enabled, snapshot.Nickname, snapshot.Identity = initial.enabled, initial.nickname, initial.identity
	if initial.identity != nil {
		out.UID = append(HexBytes(nil), initial.identity.UID...)
	}
	verify := func() error {
		observed, e := c.observeCurrentSlot(ctx, r)
		if e != nil {
			if errors.Is(e, errSlotSourceChanged) {
				snapshot.SourceChanged = true
			}
			return e
		}
		if !reflect.DeepEqual(initial, observed) {
			snapshot.SourceChanged = true
			return fmt.Errorf("slot identity or configuration changed during read")
		}
		return nil
	}
	if r.Sense == SenseHF {
		if width == 4 {
			count, e := c.exchange(ctx, uint16(CmdMf0NtagGetPageCount), nil, 0x68)
			if e != nil {
				return out, e
			}
			if len(count) != 1 || int(count[0]) != units {
				return out, fmt.Errorf("firmware page count differs from modeled slot geometry")
			}
		}
		command := CmdMf1ReadEmuBlockData
		if width == 4 {
			command = CmdMf0NtagReadEmuPageData
		}
		for pos := 0; pos < units; {
			count := min(32, units-pos)
			data, e := c.exchange(ctx, uint16(command), []byte{byte(pos), byte(count)}, 0x68)
			if e != nil {
				return out, e
			}
			if len(data) != count*width {
				return out, fmt.Errorf("slot memory response length mismatch at unit %d", pos)
			}
			if e = verify(); e != nil {
				return out, e
			}
			snapshot.Data = append(snapshot.Data, data...)
			pos += count
		}
	} else {
		command, _, _ := lfBackupCommands(r.TagType)
		readID := func() ([]byte, error) {
			b, e := c.exchange(ctx, uint16(command), nil, 0x68)
			if e == nil && command == CmdEm410xGetEmuId {
				b, e = normalizeEM410xID(r.TagType, b)
			}
			return b, e
		}
		data, e := readID()
		if e != nil {
			return out, e
		}
		record, e := DecodeLFRecord(guiLFProtocols[r.TagType], r.TagType, data)
		if e != nil {
			return out, e
		}
		if e = verify(); e != nil {
			return out, e
		}
		again, e := readID()
		if e != nil {
			return out, e
		}
		if !bytes.Equal(data, again) {
			snapshot.SourceChanged = true
			return out, fmt.Errorf("LF slot identity changed during read")
		}
		snapshot.Record = &record
		snapshot.Data = append(snapshot.Data, data...)
	}
	return out, nil
}
