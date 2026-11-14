package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type backupDevice struct {
	readerConfig []byte
	active, mode byte
	slots        [8]BackupSlot
	settings     []byte
	threshold    []byte
	writes       atomic.Int32
	badRead      bool
}

func newBackupDevice() *backupDevice {
	d := &backupDevice{readerConfig: []byte{0, 0, 0, 0}, mode: 1, settings: append([]byte{6, 1, 0, 1, 2, 3, 1}, []byte("123456")...), threshold: []byte{1, 244}}
	for i := range d.slots {
		d.slots[i].Number = i + 1
	}
	image := EmulatorImage{Schema: 1, TagType: 1001, UnitBytes: 16, SourceSlot: 1, Data: make([]byte, 1024)}
	image.Data[9] = 42
	image.SHA256 = imageDigest(image.Data)
	s := &d.slots[0]
	s.HFType = 1001
	s.LFType = 100
	s.HFEnabled = true
	s.LFEnabled = true
	s.Image = &image
	s.LFID = HexBytes{1, 2, 3, 4, 5}
	s.Fields = []BackupField{{Get: 4018, Value: HexBytes{4, 1, 2, 3, 4, 4, 0, 8, 0}}}
	for _, cmd := range []Command{4007, 4010, 4012, 4014, 4016, 4039, 4040} {
		s.Fields = append(s.Fields, BackupField{Get: cmd, Value: HexBytes{0}})
	}
	return d
}
func (d *backupDevice) handle(cmd Command, p []byte) (uint16, []byte) {
	s := &d.slots[d.active]
	ok := uint16(0x68)
	switch cmd {
	case 2200:
		return ok, d.readerConfig
	case 1033:
		return ok, []byte{0}
	case 1017:
		return ok, []byte("test")
	case 1002:
		return ok, []byte{d.mode}
	case 1018:
		return ok, []byte{d.active}
	case 1034:
		return ok, append([]byte(nil), d.settings...)
	case 1041:
		return ok, d.threshold
	case 1035:
		var b []byte
		for n := uint16(1000); n <= 5013; n++ {
			if m, found := LookupCommand(Command(n)); found && m.Registered {
				b = append(b, byte(n>>8), byte(n))
			}
		}
		return ok, b
	case 1019:
		b := make([]byte, 32)
		for n, v := range d.slots {
			binary.BigEndian.PutUint16(b[n*4:], v.HFType)
			binary.BigEndian.PutUint16(b[n*4+2:], v.LFType)
		}
		return ok, b
	case 1023:
		b := make([]byte, 16)
		for n, v := range d.slots {
			b[n*2] = boolean(v.HFEnabled)
			b[n*2+1] = boolean(v.LFEnabled)
		}
		return ok, b
	case 1008:
		nick := d.slots[p[0]].LFNickname
		if p[1] == 2 {
			nick = d.slots[p[0]].HFNickname
		}
		if nick == nil {
			return 0x71, nil
		}
		return ok, []byte(*nick)
	case 4030:
		return ok, []byte{byte(len(s.Image.Data) / 4)}
	case 4021:
		return ok, s.Image.Data[int(p[0])*4 : (int(p[0])+int(p[1]))*4]
	case 4027:
		return 114, nil
	case 4008:
		out := append([]byte(nil), s.Image.Data[int(p[0])*16:(int(p[0])+int(p[1]))*16]...)
		if d.badRead {
			out[0] ^= 1
		}
		return ok, out
	case 5001:
		return ok, s.LFID
	}
	for _, f := range s.Fields {
		if f.Get == cmd && bytes.Equal(f.Request, p) {
			return ok, f.Value
		}
	}
	d.writes.Add(1)
	switch cmd {
	case 2201:
		d.readerConfig = append([]byte(nil), p...)
	case 1001:
		d.mode = p[0]
	case 1003:
		d.active = p[0]
	case 1004:
		v := &d.slots[p[0]]
		tag := binary.BigEndian.Uint16(p[1:])
		if tag >= 1000 {
			v.HFType = tag
		} else {
			v.LFType = tag
		}
	case 1005: // Factory defaults: memory is deliberately cleared.
		v := &d.slots[p[0]]
		if binary.BigEndian.Uint16(p[1:]) >= 1000 {
			width, size := 16, 1024
			if v.HFType >= 1100 {
				width, size = 4, 64
			}
			v.Image = &EmulatorImage{Schema: 1, TagType: v.HFType, UnitBytes: width, SourceSlot: int(p[0]) + 1, Data: make([]byte, size)}
		}
	case 1024:
		v := &d.slots[p[0]]
		if p[1] == 1 {
			v.LFType = 0
			v.LFID = nil
		} else {
			v.HFType = 0
			v.Image = nil
			v.Fields = nil
		}
	case 1007:
		v := &d.slots[p[0]]
		name := string(p[2:])
		if p[1] == 1 {
			v.LFNickname = &name
		} else {
			v.HFNickname = &name
		}
	case 1021:
		v := &d.slots[p[0]]
		if p[1] == 1 {
			v.LFNickname = nil
		} else {
			v.HFNickname = nil
		}
	case 1006:
		v := &d.slots[p[0]]
		if p[1] == 1 {
			v.LFEnabled = p[2] != 0
		} else {
			v.HFEnabled = p[2] != 0
		}
	case 4022:
		copy(s.Image.Data[int(p[0])*4:], p[2:])
	case 4000:
		copy(s.Image.Data[int(p[0])*16:], p[1:])
	case 5000:
		s.LFID = append(HexBytes(nil), p...)
	case 1015:
		d.settings[1] = p[0]
	case 1027:
		i := 2
		if p[0] == 'B' {
			i = 3
		}
		d.settings[i] = p[1]
	case 1029:
		i := 4
		if p[0] == 'B' {
			i = 5
		}
		d.settings[i] = p[1]
	case 1030:
		copy(d.settings[7:], p)
	case 1037:
		d.settings[6] = p[0]
	case 1042:
		d.threshold = append([]byte(nil), p...)
	case 1009, 1013:
	default:
		for get, spec := range backupProperties {
			if spec.Set == cmd {
				found := false
				for n := range s.Fields {
					if s.Fields[n].Get == get {
						s.Fields[n].Value = append(HexBytes(nil), p...)
						found = true
					}
				}
				if !found {
					s.Fields = append(s.Fields, BackupField{Get: get, Value: append(HexBytes(nil), p...)})
				}
				return ok, nil
			}
		}
		return 0x67, nil
	}
	return ok, nil
}
func backupSimulator(t *testing.T, d *backupDevice) *Client {
	a, b := net.Pipe()
	c := NewClient(a, time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer b.Close()
		for {
			h := make([]byte, 9)
			if _, e := io.ReadFull(b, h); e != nil {
				return
			}
			p := make([]byte, int(binary.BigEndian.Uint16(h[6:]))+1)
			if _, e := io.ReadFull(b, p); e != nil {
				return
			}
			cmd := binary.BigEndian.Uint16(h[2:])
			status, data := d.handle(Command(cmd), p[:len(p)-1])
			if _, e := b.Write(response(cmd, status, data)); e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = c.Close(); <-done })
	return c
}
func TestBackupEightSlotsAndRestoreRoundTrip(t *testing.T) {
	d := newBackupDevice()
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Slots) != 8 || b.Mode != 1 || b.Slots[0].Image.Data[9] != 42 {
		t.Fatal("bad capture")
	}
	v, e := c.RestoreDevice(context.Background(), b, RestoreOptions{Persist: true})
	if e != nil || !v.Verified || !v.Persisted || v.CompletedSlots != 8 {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestBackupValidationBeforeDeviceIO(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.RestoreDevice(context.Background(), DeviceBackup{}, RestoreOptions{}); e == nil {
		t.Fatal("invalid backup accepted")
	}
}
func TestBackupChecksumAndPropertyValidation(t *testing.T) {
	c := backupSimulator(t, newBackupDevice())
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	b.Slots[0].Fields = append(b.Slots[0].Fields, BackupField{Get: 1020, Value: []byte{0}})
	b.SHA256 = b.digest()
	if e = b.Validate(); e == nil {
		t.Fatal("arbitrary command accepted")
	}
	raw, _ := json.Marshal(b)
	if len(raw) == 0 {
		t.Fatal("empty serialization")
	}
}

func TestBackupType2AndReadbackFailure(t *testing.T) {
	d := newBackupDevice()
	v := &d.slots[1]
	v.HFType = 1103
	v.HFEnabled = true
	image := EmulatorImage{1, 1103, 4, 2, make(HexBytes, 64), ""}
	image.SHA256 = imageDigest(image.Data)
	v.Image = &image
	v.Fields = []BackupField{{Get: 4018, Value: HexBytes{4, 1, 2, 3, 4, 4, 0, 0, 0}}, {Get: 4019, Value: HexBytes{0}}, {Get: 4031, Value: HexBytes{0}}, {Get: 4036, Value: HexBytes{0}}, {Get: 4023, Value: make(HexBytes, 8)}, {Get: 4025, Value: make(HexBytes, 32)}}
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if result, e := c.RestoreDevice(context.Background(), b, RestoreOptions{}); e != nil || !result.Verified {
		t.Fatal(result, e)
	}
}
func TestEM410xResponseNormalization(t *testing.T) {
	for _, b := range [][]byte{{1, 2, 3, 4, 5}, {0, 100, 1, 2, 3, 4, 5}} {
		id, e := normalizeEM410xID(100, b)
		if e != nil || len(id) != 5 || id[0] != 1 {
			t.Fatal(id, e)
		}
	}
	if _, e := normalizeEM410xID(100, []byte{0, 101, 1, 2, 3, 4, 5}); e == nil {
		t.Fatal("mismatched LF type accepted")
	}
}
func TestRestoreReadbackMismatchIsNotSuccess(t *testing.T) {
	d := newBackupDevice()
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	d.badRead = true
	v, e := c.RestoreDevice(context.Background(), b, RestoreOptions{Persist: true})
	if e == nil || v.Verified || v.Persisted {
		t.Fatal(v, e)
	}
}
func TestRestoreWrongModelBeforeWrites(t *testing.T) {
	d := newBackupDevice()
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	before := d.writes.Load()
	b.Model = 1
	b.SHA256 = b.digest()
	if _, e = c.RestoreDevice(context.Background(), b, RestoreOptions{}); e == nil {
		t.Fatal("model mismatch accepted")
	}
	if d.writes.Load() != before {
		t.Fatal("write before model validation")
	}
}

func TestLegacyBackupRetainsCurrentReaderConfiguration(t *testing.T) {
	d := newBackupDevice()
	d.readerConfig = []byte{1, 0, 2, 0}
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	b.ReaderConfiguration = nil
	b.SHA256 = b.digest()
	v, e := c.RestoreDevice(context.Background(), b, RestoreOptions{})
	if e != nil || !v.Verified {
		t.Fatal(v, e)
	}
	if !bytes.Equal(d.readerConfig, []byte{1, 0, 2, 0}) {
		t.Fatal("legacy restore overwrote RF configuration")
	}
}
