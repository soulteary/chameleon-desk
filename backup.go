package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

type BackupField struct {
	Get     Command  `json:"get"`
	Request HexBytes `json:"request"`
	Value   HexBytes `json:"value"`
}
type BackupSlot struct {
	Number     int            `json:"number"`
	HFType     uint16         `json:"hf_type"`
	LFType     uint16         `json:"lf_type"`
	HFEnabled  bool           `json:"hf_enabled"`
	LFEnabled  bool           `json:"lf_enabled"`
	HFNickname *string        `json:"hf_nickname"`
	LFNickname *string        `json:"lf_nickname"`
	Image      *EmulatorImage `json:"image,omitempty"`
	Fields     []BackupField  `json:"fields"`
	LFID       HexBytes       `json:"lf_id,omitempty"`
}
type DeviceBackup struct {
	ReaderConfiguration *ReaderConfig `json:"reader_configuration,omitempty"`
	Schema              int           `json:"schema"`
	Model               byte          `json:"model"`
	Firmware            string        `json:"firmware"`
	Mode                byte          `json:"mode"`
	ActiveSlot          int           `json:"active_slot"`
	Settings            HexBytes      `json:"settings"`
	Threshold           HexBytes      `json:"long_press_threshold"`
	Slots               []BackupSlot  `json:"slots"`
	SHA256              string        `json:"sha256"`
}

func (b DeviceBackup) digest() string { b.SHA256 = ""; v, _ := json.Marshal(b); return imageDigest(v) }

var backupProperties = map[Command]struct {
	Set  Command
	Size int
}{4018: {4001, -1}, 4007: {4004, 1}, 4010: {4011, 1}, 4012: {4013, 1}, 4014: {4015, 1}, 4016: {4017, 1}, 4039: {4038, 1}, 4040: {4041, 1}, 4019: {4020, 1}, 4031: {4032, 1}, 4036: {4033, 1}, 4023: {4024, 8}, 4025: {4026, 32}, 4027: {4028, 4}}

func lfBackupCommands(tag uint16) (Command, Command, error) {
	names := map[uint16]string{100: "em410x", 101: "em410x", 102: "em410x", 103: "em410x", 104: "em410x", 150: "pac", 170: "viking", 180: "jablotron", 200: "hidprox", 201: "ioprox", 310: "idteck"}
	return lfIDCommands(names[tag])
}
func (b DeviceBackup) Validate() error {
	if b.Schema != 1 || b.Model > 1 || b.Mode > 1 || b.ActiveSlot < 1 || b.ActiveSlot > 8 || len(b.Slots) != 8 || b.SHA256 != b.digest() {
		return fmt.Errorf("invalid backup schema/model/mode/slots/checksum")
	}
	if b.ReaderConfiguration != nil {
		p, e := b.ReaderConfiguration.payload()
		if e != nil {
			return e
		}
		for _, v := range p {
			if v > 2 {
				return fmt.Errorf("snapshot reader overrides must have concrete values 0..2")
			}
		}
	}
	s := b.Settings
	if (len(s) != 13 && len(s) != 14) || (s[0] != 6 && s[0] != 7) || s[1] > 3 || s[6] > 1 || len(b.Threshold) != 2 {
		return fmt.Errorf("invalid settings snapshot")
	}
	for _, v := range s[2:6] {
		if v > 5 {
			return fmt.Errorf("invalid button action")
		}
	}
	for _, v := range s[7:13] {
		if v < '0' || v > '9' {
			return fmt.Errorf("invalid BLE PIN")
		}
	}
	for n, v := range b.Slots {
		if v.Number != n+1 {
			return fmt.Errorf("slots must be ordered 1..8")
		}
		for _, name := range []*string{v.HFNickname, v.LFNickname} {
			if name != nil && (!utf8.ValidString(*name) || len(*name) > 32) {
				return fmt.Errorf("invalid nickname")
			}
		}
		if v.HFType != 0 {
			if v.Image == nil || v.Image.TagType != v.HFType || v.Image.SourceSlot != v.Number {
				return fmt.Errorf("missing/mismatched HF image")
			}
			if e := v.Image.Validate(); e != nil {
				return e
			}
		} else if v.Image != nil || len(v.Fields) != 0 {
			return fmt.Errorf("data on empty HF slot")
		}
		seen := map[string]bool{}
		for _, f := range v.Fields {
			spec, ok := backupProperties[f.Get]
			if !ok || (spec.Size >= 0 && len(f.Value) != spec.Size) {
				return fmt.Errorf("invalid backup property")
			}
			id := fmt.Sprintf("%d/%x", f.Get, []byte(f.Request))
			if seen[id] {
				return fmt.Errorf("duplicate property")
			}
			seen[id] = true
			if f.Get == 4027 {
				if len(f.Request) != 1 || f.Request[0] > 2 || f.Value[3] != 0xbd {
					return fmt.Errorf("counter tearing state cannot be restored exactly")
				}
			} else if len(f.Request) != 0 {
				return fmt.Errorf("unexpected property parameters")
			}
			if f.Get == 4018 {
				cards, e := parseCards(f.Value)
				if e != nil || len(cards) != 1 {
					return fmt.Errorf("invalid identity")
				}
			} else if spec.Size == 1 {
				max := byte(1)
				if f.Get == 4016 || f.Get == 4031 {
					max = 4
				}
				if f.Get == 4040 {
					max = 2
				}
				if f.Value[0] > max {
					return fmt.Errorf("invalid emulator property value")
				}
			}
			if v.HFType >= 1100 && f.Get >= 4007 && f.Get <= 4016 {
				return fmt.Errorf("Classic property on Type2")
			}
			if v.HFType < 1100 && (f.Get == 4027 || f.Get == 4023 || f.Get == 4025 || f.Get == 4019 || f.Get == 4031 || f.Get == 4036) {
				return fmt.Errorf("Type2 property on Classic")
			}
		}
		if v.HFType != 0 {
			required := []Command{4018}
			if v.HFType < 1100 {
				required = append(required, 4007, 4010, 4012, 4014, 4016, 4039, 4040)
			} else {
				required = append(required, 4019, 4031, 4036)
				if v.HFType != 1103 && v.HFType != 1104 {
					required = append(required, 4023, 4025)
				}
			}
			for _, get := range required {
				if !seen[fmt.Sprintf("%d/", get)] {
					return fmt.Errorf("backup missing required HF property %s", get)
				}
			}
		}
		if v.LFType != 0 {
			lengths := map[uint16]int{100: 5, 101: 13, 102: 13, 103: 13, 104: 13, 150: 8, 170: 4, 180: 5, 200: 13, 201: 16, 310: 8}
			if len(v.LFID) != lengths[v.LFType] && !(v.LFType >= 100 && v.LFType <= 104 && (len(v.LFID) == 5 || len(v.LFID) == 13)) {
				return fmt.Errorf("slot %d LF type %d: incompatible ID length %d", v.Number, v.LFType, len(v.LFID))
			}
			if _, _, e := lfBackupCommands(v.LFType); e != nil || len(v.LFID) == 0 || len(v.LFID) > 16 {
				return fmt.Errorf("unsupported/missing LF image")
			}
		} else if len(v.LFID) != 0 {
			return fmt.Errorf("LF data on empty slot")
		}
	}
	return nil
}
func (c *Client) backupLocked(ctx context.Context) (out DeviceBackup, err error) {
	out.Schema = 1
	q := func(cmd Command, p ...byte) ([]byte, error) { return c.exchange(ctx, uint16(cmd), p, 0x68) }
	model, e := q(1033)
	if e != nil || len(model) != 1 {
		return out, errors.Join(e, fmt.Errorf("invalid model"))
	}
	out.Model = model[0]
	fw, e := q(1017)
	if e != nil {
		return out, e
	}
	out.Firmware = string(fw)
	mode, e := q(1002)
	if e != nil {
		return out, e
	}
	if len(mode) != 1 || mode[0] > 1 {
		return out, fmt.Errorf("invalid device mode")
	}
	active, e := q(1018)
	if e != nil || len(active) != 1 || active[0] > 7 {
		return out, fmt.Errorf("read active slot: %w", e)
	}
	out.Mode = mode[0]
	out.ActiveSlot = int(active[0]) + 1
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*c.timeout)
		defer cancel()
		_, e1 := c.exchange(cleanup, 1003, active, 0x68)
		_, e2 := c.exchange(cleanup, 1001, mode, 0x68)
		err = errors.Join(err, e1, e2)
	}()
	types, e := q(1019)
	if e != nil || len(types) != 32 {
		return out, fmt.Errorf("invalid slot types: %w", e)
	}
	enabled, e := q(1023)
	if e != nil || len(enabled) != 16 {
		return out, fmt.Errorf("invalid enabled flags: %w", e)
	}
	out.Settings, e = q(1034)
	if e != nil {
		return out, e
	}
	out.Threshold, e = q(1041)
	if e != nil {
		return out, e
	}
	cfg, ce := q(2200)
	if ce == nil {
		if len(cfg) != 4 {
			return out, fmt.Errorf("invalid reader configuration backup")
		}
		v := ReaderConfig{int8(cfg[0]), int8(cfg[1]), int8(cfg[2]), int8(cfg[3])}
		out.ReaderConfiguration = &v
	} else {
		var status *StatusError
		if !errors.As(ce, &status) || (status.Status != 103 && status.Status != 105) {
			return out, ce
		}
	}
	if _, e = q(1001, 0); e != nil {
		return out, e
	}
	for n := 0; n < 8; n++ {
		v := BackupSlot{Number: n + 1, HFType: binary.BigEndian.Uint16(types[n*4:]), LFType: binary.BigEndian.Uint16(types[n*4+2:]), HFEnabled: enabled[n*2] == 1, LFEnabled: enabled[n*2+1] == 1}
		for sense := byte(1); sense <= 2; sense++ {
			f, e := c.exchangeFrame(ctx, 1008, []byte{byte(n), sense})
			if e != nil {
				return out, e
			}
			var nick *string
			if f.status == 0x68 {
				s := string(f.data)
				nick = &s
			} else if f.status != 0x71 {
				return out, &StatusError{1008, f.status}
			}
			if sense == 1 {
				v.LFNickname = nick
			} else {
				v.HFNickname = nick
			}
		}
		if _, e = q(1003, byte(n)); e != nil {
			return out, e
		}
		if v.HFType != 0 {
			slot, tag, units, width, e := c.activeGeometry(ctx)
			if e != nil {
				return out, fmt.Errorf("slot %d cannot be fully exported: %w", n+1, e)
			}
			data, e := c.readImageUnits(ctx, 0, units, width)
			if e != nil {
				return out, e
			}
			image := EmulatorImage{1, tag, width, slot, data, imageDigest(data)}
			v.Image = &image
			gets := []Command{4018}
			if width == 16 {
				gets = append(gets, 4007, 4010, 4012, 4014, 4016, 4039, 4040)
			} else {
				gets = append(gets, 4019, 4031, 4036)
				if v.HFType != 1103 && v.HFType != 1104 {
					gets = append(gets, 4023, 4025)
				}
			}
			for _, get := range gets {
				b, e := q(get)
				if e != nil {
					return out, e
				}
				v.Fields = append(v.Fields, BackupField{get, nil, b})
			}
			if width == 4 {
				for index := byte(0); index < 3; index++ {
					b, e := q(4027, index)
					if isInvalidSlotType(e) {
						continue
					}
					if e != nil {
						return out, e
					}
					v.Fields = append(v.Fields, BackupField{4027, HexBytes{index}, b})
				}
			}
		}
		if v.LFType != 0 {
			get, _, e := lfBackupCommands(v.LFType)
			if e != nil {
				return out, e
			}
			v.LFID, e = q(get)
			if e == nil && get == 5001 {
				v.LFID, e = normalizeEM410xID(v.LFType, v.LFID)
			}
			if e != nil {
				return out, e
			}
		}
		out.Slots = append(out.Slots, v)
	}
	out.SHA256 = out.digest()
	return out, out.Validate()
}

// BackupDevice serializes all eight slots, temporarily switching active slot
// and mode; restores both even on failure. It never initializes or saves slots.
func (c *Client) BackupDevice(ctx context.Context) (DeviceBackup, error) {
	if e := c.acquire(ctx); e != nil {
		return DeviceBackup{}, e
	}
	defer c.release()
	return c.backupLocked(ctx)
}

type RestoreOptions struct {
	Persist         bool
	AllowOtherModel bool
}
type RestoreResult struct {
	Verified       bool `json:"verified"`
	Persisted      bool `json:"persisted"`
	CompletedSlots int  `json:"completed_slots"`
}

// RestoreDevice verifies a validated snapshot by exporting all slots afterwards.
// A disconnect can leave partial RAM/flash changes; retain a pre-restore backup.
// Slot switches and initialization can persist data in upstream firmware.
// Persist controls the final explicit slot-config/settings commit only.
func (c *Client) RestoreDevice(ctx context.Context, b DeviceBackup, o RestoreOptions) (out RestoreResult, err error) {
	if e := b.Validate(); e != nil {
		return out, e
	}
	if e := c.acquire(ctx); e != nil {
		return out, e
	}
	defer c.release()
	q := func(cmd Command, p ...byte) error { _, e := c.exchange(ctx, uint16(cmd), p, 0x68); return e }
	model, e := c.exchange(ctx, 1033, nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(model) != 1 || (!o.AllowOtherModel && model[0] != b.Model) {
		return out, fmt.Errorf("backup model mismatch")
	}
	caps, ce := c.exchange(ctx, 1035, nil, 0x68)
	if ce != nil {
		return out, ce
	}
	if len(caps)%2 != 0 {
		return out, fmt.Errorf("invalid capabilities")
	}
	available := map[Command]bool{}
	for p := 0; p < len(caps); p += 2 {
		available[Command(binary.BigEndian.Uint16(caps[p:]))] = true
	}
	required := []Command{1001, 1003, 1004, 1005, 1006, 1007, 1008, 1009, 1013, 1015, 1018, 1019, 1021, 1023, 1024, 1027, 1029, 1030, 1034, 1037, 1041, 1042}
	if len(b.Settings) == 14 {
		required = append(required, 1040)
	}
	if b.ReaderConfiguration != nil {
		required = append(required, 2200, 2201)
	}
	for _, slot := range b.Slots {
		if slot.Image != nil {
			if slot.Image.UnitBytes == 16 {
				required = append(required, 4000, 4008)
			} else {
				required = append(required, 4022, 4021, 4030)
			}
		}
		for _, f := range slot.Fields {
			required = append(required, f.Get, backupProperties[f.Get].Set)
		}
		if slot.LFType != 0 {
			get, set, _ := lfBackupCommands(slot.LFType)
			required = append(required, get, set)
		}
	}
	for _, cmd := range required {
		if !available[cmd] {
			return out, fmt.Errorf("restore requires unsupported command %s; no restore writes sent", cmd)
		}
	}
	settings, se := c.exchange(ctx, 1034, nil, 0x68)
	if se != nil {
		return out, se
	}
	if len(settings) != len(b.Settings) || settings[0] != b.Settings[0] {
		return out, fmt.Errorf("settings schema mismatch; no restore writes sent")
	}
	mode, e := c.exchange(ctx, 1002, nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(mode) != 1 || mode[0] > 1 {
		return out, fmt.Errorf("invalid device mode; no restore writes sent")
	}
	active, e := c.exchange(ctx, 1018, nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(active) != 1 || active[0] > 7 {
		return out, fmt.Errorf("invalid active slot; no restore writes sent")
	}
	success := false
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*c.timeout)
			defer cancel()
			_, e1 := c.exchange(cleanup, 1003, active, 0x68)
			_, e2 := c.exchange(cleanup, 1001, mode, 0x68)
			err = errors.Join(err, e1, e2)
		}
	}()
	if e = q(1001, 0); e != nil {
		return out, e
	}
	for _, v := range b.Slots {
		n := byte(v.Number - 1)
		for _, pair := range []struct {
			s byte
			t uint16
		}{{2, v.HFType}, {1, v.LFType}} {
			if pair.t == 0 {
				e = q(1024, n, pair.s)
			} else {
				e = q(1004, n, byte(pair.t>>8), byte(pair.t))
				if e == nil {
					e = q(1005, n, byte(pair.t>>8), byte(pair.t))
				}
			}
			if e != nil {
				return out, e
			}
		}
		if e = q(1003, n); e != nil {
			return out, e
		}
		if v.Image != nil {
			image := v.Image
			for pos := 0; pos < len(image.Data)/image.UnitBytes; {
				count := min(32, len(image.Data)/image.UnitBytes-pos)
				cmd := Command(4000)
				p := []byte{byte(pos)}
				if image.UnitBytes == 4 {
					cmd = 4022
					p = append(p, byte(count))
				}
				p = append(p, image.Data[pos*image.UnitBytes:(pos+count)*image.UnitBytes]...)
				if e = q(cmd, p...); e != nil {
					return out, e
				}
				pos += count
			}
			for _, f := range v.Fields {
				payload := append([]byte(nil), f.Value...)
				if f.Get == 4027 {
					payload = append([]byte{f.Request[0] | 0x80}, f.Value[:3]...)
				}
				if e = q(backupProperties[f.Get].Set, payload...); e != nil {
					return out, e
				}
			}
		}
		if v.LFType != 0 {
			_, set, _ := lfBackupCommands(v.LFType)
			if e = q(set, v.LFID...); e != nil {
				return out, e
			}
		}
		for _, pair := range []struct {
			s       byte
			nick    *string
			enabled bool
		}{{1, v.LFNickname, v.LFEnabled}, {2, v.HFNickname, v.HFEnabled}} {
			if pair.nick == nil {
				e = q(1021, n, pair.s)
			} else {
				e = q(1007, append([]byte{n, pair.s}, []byte(*pair.nick)...)...)
			}
			if e != nil {
				return out, e
			}
			if e = q(1006, n, pair.s, boolean(pair.enabled)); e != nil {
				return out, e
			}
		}
		out.CompletedSlots++
	}
	s := b.Settings
	for _, write := range []struct {
		c Command
		p []byte
	}{{1015, s[1:2]}, {1027, []byte{'A', s[2]}}, {1027, []byte{'B', s[3]}}, {1029, []byte{'A', s[4]}}, {1029, []byte{'B', s[5]}}, {1030, s[7:13]}, {1037, s[6:7]}, {1042, b.Threshold}} {
		if e = q(write.c, write.p...); e != nil {
			return out, e
		}
	}
	if b.ReaderConfiguration != nil {
		p, _ := b.ReaderConfiguration.payload()
		if e = q(2201, p...); e != nil {
			return out, e
		}
	}
	if len(s) == 14 {
		if e = q(1040, s[13]); e != nil {
			return out, e
		}
	}
	if e = q(1003, byte(b.ActiveSlot-1)); e != nil {
		return out, e
	}
	if e = q(1001, b.Mode); e != nil {
		return out, e
	}
	actual, e := c.backupLocked(ctx)
	if e != nil {
		return out, e
	}
	if b.ReaderConfiguration == nil {
		actual.ReaderConfiguration = nil
	}
	actual.Firmware = b.Firmware
	actual.Model = b.Model
	actual.SHA256 = ""
	expected := b
	expected.SHA256 = ""
	ab, _ := json.Marshal(canonicalBackup(actual))
	bb, _ := json.Marshal(canonicalBackup(expected))
	if !bytes.Equal(ab, bb) {
		return out, fmt.Errorf("restore readback differs; settings/slots not committed")
	}
	out.Verified = true
	if o.Persist {
		if e = q(1009); e != nil {
			return out, e
		}
		if e = q(CmdSaveSettings); e != nil {
			return out, e
		}
		out.Persisted = true
	}
	success = true
	return out, nil
}

func isInvalidSlotType(e error) bool {
	var status *StatusError
	return errors.As(e, &status) && status.Status == 114
}

type RestoreSlotPlan struct {
	Number     int    `json:"number"`
	HFType     uint16 `json:"hf_type"`
	LFType     uint16 `json:"lf_type"`
	HFBytes    int    `json:"hf_bytes"`
	LFBytes    int    `json:"lf_bytes"`
	Properties int    `json:"properties"`
}
type RestorePlan struct {
	SlotDetails []RestoreSlotPlan `json:"slot_details"`
	Slots       int               `json:"slots"`
	ActiveSlot  int               `json:"active_slot"`
	Model       byte              `json:"model"`
	Firmware    string            `json:"firmware"`
	Warning     string            `json:"warning"`
}

func (b DeviceBackup) RestorePlan() (RestorePlan, error) {
	if e := b.Validate(); e != nil {
		return RestorePlan{}, e
	}
	details := make([]RestoreSlotPlan, 0, 8)
	for _, s := range b.Slots {
		n := 0
		if s.Image != nil {
			n = len(s.Image.Data)
		}
		details = append(details, RestoreSlotPlan{s.Number, s.HFType, s.LFType, n, len(s.LFID), len(s.Fields)})
	}
	return RestorePlan{Slots: 8, ActiveSlot: b.ActiveSlot, Model: b.Model, Firmware: b.Firmware, SlotDetails: details, Warning: "Restore overwrites eight slots and settings. Upstream slot switching/initialization may write flash before final commit; this is not atomic. Retain a separate pre-restore backup."}, nil
}

// Property order and absent/empty collections do not change restored state.
func canonicalBackup(b DeviceBackup) DeviceBackup {
	b.Slots = append([]BackupSlot(nil), b.Slots...)
	for i := range b.Slots {
		fields := append([]BackupField(nil), b.Slots[i].Fields...)
		sort.Slice(fields, func(i, j int) bool {
			if fields[i].Get != fields[j].Get {
				return fields[i].Get < fields[j].Get
			}
			return bytes.Compare(fields[i].Request, fields[j].Request) < 0
		})
		for j := range fields {
			if len(fields[j].Request) == 0 {
				fields[j].Request = nil
			}
		}
		b.Slots[i].Fields = fields
	}
	return b
}
