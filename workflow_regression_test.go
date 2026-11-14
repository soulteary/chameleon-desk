package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestType2ImagesRequireExactGeometry(t *testing.T) {
	for tag, pages := range guiType2Pages {
		for _, units := range []int{1, pages - 1, pages, pages + 1} {
			i := EmulatorImage{Schema: 1, TagType: tag, UnitBytes: 4, Data: make(HexBytes, units*4)}
			i.SHA256 = imageDigest(i.Data)
			e := i.Validate()
			if (e == nil) != (units == pages) {
				t.Fatalf("tag %d: %d pages, error %v", tag, units, e)
			}
		}
	}
}
func type2BackupFixture(t *testing.T) (*Client, *backupDevice, DeviceBackup) {
	t.Helper()
	d := newBackupDevice()
	s := &d.slots[1]
	s.HFType = 1103
	s.HFEnabled = true
	i := EmulatorImage{1, 1103, 4, 2, make(HexBytes, 64), ""}
	i.SHA256 = imageDigest(i.Data)
	s.Image = &i
	s.Fields = []BackupField{{Get: 4018, Value: HexBytes{4, 1, 2, 3, 4, 4, 0, 0, 0}}, {Get: 4019, Value: HexBytes{0}}, {Get: 4031, Value: HexBytes{0}}, {Get: 4036, Value: HexBytes{0}}}
	c := backupSimulator(t, d)
	b, e := c.BackupDevice(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return c, d, b
}
func TestRestoreRejectsTruncatedType2WithoutIO(t *testing.T) {
	c, d, b := type2BackupFixture(t)
	b.Slots[1].Image.Data = b.Slots[1].Image.Data[:4]
	b.Slots[1].Image.SHA256 = imageDigest(b.Slots[1].Image.Data)
	b.SHA256 = b.digest()
	requests, writes := c.Stats().Requests, d.writes.Load()
	out, e := c.RestoreDevice(context.Background(), b, RestoreOptions{Persist: true})
	if e == nil || out.CompletedSlots != 0 || c.Stats().Requests != requests || d.writes.Load() != writes {
		t.Fatal(out, e, "invalid restore accessed transport")
	}
}
func TestRestoreComparesPropertiesSemantically(t *testing.T) {
	c, _, b := type2BackupFixture(t)
	for i := range b.Slots {
		f := b.Slots[i].Fields
		for a, z := 0, len(f)-1; a < z; a, z = a+1, z-1 {
			f[a], f[z] = f[z], f[a]
		}
		if len(f) == 0 {
			b.Slots[i].Fields = []BackupField{}
		}
		for j := range f {
			if len(f[j].Request) == 0 {
				f[j].Request = HexBytes{}
			}
		}
	}
	b.SHA256 = b.digest()
	before, _ := json.Marshal(b)
	out, e := c.RestoreDevice(context.Background(), b, RestoreOptions{Persist: true})
	if e != nil || !out.Verified || !out.Persisted || out.CompletedSlots != 8 {
		t.Fatal(out, e)
	}
	after, _ := json.Marshal(b)
	if !bytes.Equal(before, after) {
		t.Fatal("verification mutated caller's backup")
	}
}
func TestRestoreStillRejectsDifferentState(t *testing.T) {
	c, d, b := type2BackupFixture(t)
	d.badRead = true
	out, e := c.RestoreDevice(context.Background(), b, RestoreOptions{Persist: true})
	if e == nil || out.Verified || out.Persisted {
		t.Fatal("mismatched memory accepted", out, e)
	}
}
func TestDumpClassicRejectsStaleProvidedKeys(t *testing.T) {
	for _, name := range []string{"hidden-B", "readable-B", "wrong-A", "invalid-ACL"} {
		t.Run(name, func(t *testing.T) {
			a := HexBytes{1, 2, 3, 4, 5, 6}
			b := HexBytes{9, 9, 9, 9, 9, 9}
			actualB := HexBytes{8, 7, 6, 5, 4, 3}
			k := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Complete: true, Sectors: make([]SectorKeys, 5)}
			for i := range k.Sectors {
				k.Sectors[i] = SectorKeys{Sector: i, A: a, B: b, BReadable: name == "readable-B"}
			}
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}}}
			for block := 0; block < 4; block++ {
				data := make([]byte, 16)
				if block == 3 {
					copy(data[6:9], []byte{0x7f, 7, 0x88})
					if name == "readable-B" {
						copy(data[6:9], []byte{0xff, 7, 0x80})
						copy(data[10:], actualB)
					}
					if name == "invalid-ACL" {
						data[6] ^= 1
					}
				}
				kind, key := KeyA, a
				if name == "wrong-A" {
					steps = append(steps, readerStep{command: 2008, request: append([]byte{0x60, byte(block)}, a...), status: 6})
					kind, key = KeyB, b
				}
				steps = append(steps, readerStep{command: 2008, request: append([]byte{byte(kind), byte(block)}, key...), data: data})
			}
			if name == "hidden-B" || name == "wrong-A" {
				kind, key := KeyB, b
				if name == "wrong-A" {
					kind, key = KeyA, a
				}
				steps = append(steps, readerStep{command: 2007, request: append([]byte{byte(kind), 3}, key...), status: 6})
			}
			c := readerSimulator(t, steps)
			dump, e := c.DumpClassicWithKeys(context.Background(), k)
			if e == nil || dump.Validate() == nil {
				t.Fatal("stale or invalid dump accepted", dump, e)
			}
			if name == "readable-B" && !strings.Contains(e.Error(), "differs from readable trailer") {
				t.Fatal(e)
			}
		})
	}
}
