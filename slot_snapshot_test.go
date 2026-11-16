package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func slotSnapshotRequest(tag uint16) SlotReadRequest {
	sense := SenseHF
	if tag < 1000 {
		sense = SenseLF
	}
	return SlotReadRequest{Slot: 3, Sense: sense, TagType: tag}
}
func slotSnapshotCapabilities(r SlotReadRequest) []byte {
	commands, _ := r.Requirements()
	var data []byte
	for _, cmd := range commands {
		data = append(data, byte(cmd>>8), byte(cmd))
	}
	return data
}
func slotSnapshotObservation(r SlotReadRequest, uid []byte) []readerStep {
	types, flags := make([]byte, 32), make([]byte, 16)
	offset, flag := 8, 4
	if r.Sense == SenseLF {
		offset += 2
		flag++
	}
	binary.BigEndian.PutUint16(types[offset:], r.TagType)
	flags[flag] = 1
	out := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{2}}, {command: 1019, status: 0x68, data: types}, {command: 1023, status: 0x68, data: flags}, {command: 1008, status: 0x68, request: []byte{2, byte(r.Sense)}, data: []byte("测试 slot")}}
	if r.Sense == SenseHF {
		out = append(out, readerStep{command: 4018, status: 0x68, data: append(append([]byte{byte(len(uid))}, uid...), 4, 0, 8, 0)})
	}
	return out
}
func TestReadCurrentSlotClassicAndType2PreserveIdentityAndMemory(t *testing.T) {
	for _, tag := range []uint16{1000, 1001, 1002, 1003, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 1107, 1108} {
		t.Run(fmt.Sprint(tag), func(t *testing.T) {
			r := slotSnapshotRequest(tag)
			n, width, _ := r.geometry()
			uid := []byte{1, 2, 3, 4}
			data := bytes.Repeat([]byte{0xa5}, n*width)
			steps := []readerStep{{command: 1035, status: 0x68, data: slotSnapshotCapabilities(r)}}
			steps = append(steps, slotSnapshotObservation(r, uid)...)
			command := uint16(4008)
			if width == 4 {
				command = 4021
				steps = append(steps, readerStep{command: 4030, status: 0x68, data: []byte{byte(n)}})
			}
			for pos := 0; pos < n; {
				count := min(32, n-pos)
				steps = append(steps, readerStep{command: command, status: 0x68, request: []byte{byte(pos), byte(count)}, data: data[pos*width : (pos+count)*width]})
				steps = append(steps, slotSnapshotObservation(r, uid)...)
				pos += count
			}
			client := readerSimulator(t, steps)
			out, e := client.ReadCurrentSlot(context.Background(), r)
			if e != nil || !out.Complete || out.Kind != "slot" || out.Read != n || out.Slot.Validate() != nil || !bytes.Equal(out.Slot.Data, data) {
				t.Fatalf("%+v %v", out, e)
			}
			if !bytes.Equal(out.Slot.Identity.ATQA, []byte{0, 4}) || out.Slot.Identity.ATS == nil || len(*out.Slot.Identity.ATS) != 0 || out.Slot.Nickname == nil || *out.Slot.Nickname != "测试 slot" {
				t.Fatal("identity/nickname lost", out.Slot)
			}
			card, e := NewGUICardFromRead("snapshot", out, "")
			if e != nil {
				t.Fatal(e)
			}
			draft, e := card.ReadDraft()
			if e != nil || len(draft.Rows) != n || card.Tag != 0 || len(card.Data) != 0 {
				t.Fatal(card, e)
			}
			if card.RequireCardImage() == nil || card.ClassicPromotionError() == nil {
				t.Fatal("slot snapshot became physical evidence/loadable card")
			}
		})
	}
}
func TestSlotSnapshotPartialUnknownAndEditRoundTrip(t *testing.T) {
	r := slotSnapshotRequest(1001)
	uid := []byte{1, 2, 3, 4}
	steps := []readerStep{{command: 1035, status: 0x68, data: slotSnapshotCapabilities(r)}}
	steps = append(steps, slotSnapshotObservation(r, uid)...)
	steps = append(steps, readerStep{command: 4008, status: 0x68, request: []byte{0, 32}, data: bytes.Repeat([]byte{0x42}, 512)})
	steps = append(steps, slotSnapshotObservation(r, uid)...)
	steps = append(steps, readerStep{command: 4008, status: 103, request: []byte{32, 32}})
	c := readerSimulator(t, steps)
	out, e := c.ReadCurrentSlot(context.Background(), r)
	if e == nil || out.Complete || out.Read != 32 || out.Slot.SourceChanged {
		t.Fatal(out, e)
	}
	card, e := NewGUICardFromRead("partial", out, e.Error())
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(card)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := ParseGUICard(raw)
	if e != nil {
		t.Fatal(e)
	}
	draft, e := copy.ReadDraft()
	if e != nil || draft.Rows[32] != strings.Repeat("??", 16) || draft.Rows[0] != strings.Repeat("42", 16) {
		t.Fatal(draft, e)
	}
	original := draft.Result.Slot.SHA256
	rows := append([]string(nil), draft.Rows...)
	rows[0] = strings.Repeat("11", 16)
	edited, e := copy.EditData("edited", rows)
	if e != nil {
		t.Fatal(e)
	}
	editedDraft, _ := edited.ReadDraft()
	if editedDraft.Result.Slot.SHA256 != original || !bytes.Equal(editedDraft.Result.Slot.Data, out.Slot.Data) {
		t.Fatal("original snapshot overwritten")
	}
	rows[32] = strings.Repeat("00", 16)
	if _, e := copy.EditData("unsafe", rows); e == nil {
		t.Fatal("unknown memory replaced with zero")
	}
	out.Slot.Data[0] ^= 1
	if _, e := NewGUICardFromRead("corrupt", out, ""); e == nil {
		t.Fatal("checksum corruption accepted")
	}
}
func TestSlotSnapshotRejectsChangedSourceAndUnconfirmedChunk(t *testing.T) {
	r := slotSnapshotRequest(1000)
	uid := []byte{1, 2, 3, 4}
	for _, change := range []string{"identity", "slot", "type", "short"} {
		t.Run(change, func(t *testing.T) {
			steps := []readerStep{{command: 1035, status: 0x68, data: slotSnapshotCapabilities(r)}}
			steps = append(steps, slotSnapshotObservation(r, uid)...)
			data := make([]byte, 320)
			if change == "short" {
				data = data[:319]
			}
			steps = append(steps, readerStep{command: 4008, status: 0x68, request: []byte{0, 20}, data: data})
			if change != "short" {
				observation := slotSnapshotObservation(r, uid)
				switch change {
				case "identity":
					observation[5].data[1] = 9
				case "slot":
					observation[1].data = []byte{3}
					observation = observation[:2]
				case "type":
					observation[2].data[9] = 0xe9
					observation = observation[:4]
				}
				steps = append(steps, observation...)
			}
			c := readerSimulator(t, steps)
			out, e := c.ReadCurrentSlot(context.Background(), r)
			if e == nil || out.Read != 0 || out.Complete {
				t.Fatal(out, e)
			}
			if change != "short" && !out.Slot.SourceChanged {
				t.Fatal("source change not recorded")
			}
			if _, e := NewGUICardFromRead("changed", out, ""); e == nil {
				t.Fatal("unconfirmed bytes imported")
			}
		})
	}
}
func TestSlotSnapshotModeChangeBlocksImportOfConfirmedPrefix(t *testing.T) {
	r := slotSnapshotRequest(1001)
	uid := []byte{1, 2, 3, 4}
	steps := []readerStep{{command: 1035, status: 0x68, data: slotSnapshotCapabilities(r)}}
	steps = append(steps, slotSnapshotObservation(r, uid)...)
	steps = append(steps, readerStep{command: 4008, status: 0x68, request: []byte{0, 32}, data: make([]byte, 512)})
	steps = append(steps, slotSnapshotObservation(r, uid)...)
	steps = append(steps,
		readerStep{command: 4008, status: 0x68, request: []byte{32, 32}, data: make([]byte, 512)},
		readerStep{command: 1002, status: 0x68, data: []byte{1}},
	)
	out, err := readerSimulator(t, steps).ReadCurrentSlot(context.Background(), r)
	if err == nil || out.Read != 32 || !out.Slot.SourceChanged || out.Complete || out.Slot.Validate() != nil {
		t.Fatal(out, err)
	}
	if _, err := NewGUICardFromRead("changed source", out, err.Error()); err == nil {
		t.Fatal("snapshot with a detected mode change was imported")
	}
}

func TestSlotSnapshotLFIdentityAndNoModeOrSlotMutation(t *testing.T) {
	for _, tag := range []uint16{100, 101, 102, 103, 104, 150, 170, 180, 200, 201, 310} {
		t.Run(fmt.Sprint(tag), func(t *testing.T) {
			r := slotSnapshotRequest(tag)
			_, width, _ := r.geometry()
			id := bytes.Repeat([]byte{1}, width)
			switch tag {
			case 150:
				id = []byte("12345678")
			case 180:
				id = []byte{0x12, 0x34, 0x56, 0x78, 0x90}
			case 200:
				encoded, e := (HIDProxID{Format: 1, Facility: 12, Number: 345}).Encode()
				if e != nil {
					t.Fatal(e)
				}
				id = encoded
			case 201:
				id = []byte{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
			case 310:
				encoded, e := ComposeIDTECK(0x123456)
				if e != nil {
					t.Fatal(e)
				}
				id = encoded
			}
			get, _, _ := lfBackupCommands(tag)
			payload := id
			if get == CmdEm410xGetEmuId {
				payload = append([]byte{byte(tag >> 8), byte(tag)}, id...)
			}
			steps := []readerStep{{command: 1035, status: 0x68, data: slotSnapshotCapabilities(r)}}
			steps = append(steps, slotSnapshotObservation(r, nil)...)
			steps = append(steps, readerStep{command: uint16(get), status: 0x68, data: payload})
			steps = append(steps, slotSnapshotObservation(r, nil)...)
			steps = append(steps, readerStep{command: uint16(get), status: 0x68, data: payload})
			c := readerSimulator(t, steps)
			out, e := c.ReadCurrentSlot(context.Background(), r)
			if e != nil || !out.Complete || out.Slot.Validate() != nil || !bytes.Equal(out.Slot.Record.ID, id) {
				t.Fatal(out, e)
			}
			card, e := NewGUICardFromRead("LF slot", out, "")
			if e != nil {
				t.Fatal(e)
			}
			db, _ := ParseGUIDatabase([]byte(`{"cards":[],"folders":[],"dictionaries":[],"dictionaryFolders":[]}`))
			db, e = db.AddCard(card)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := db.Records(false, "", "lf")
			if e != nil || len(rows) != 1 || rows[0].Frequency != "lf" || rows[0].UID != HexBytes(id).String() {
				t.Fatal(rows, e)
			}
		})
	}
}
func TestSlotReadRequirementsAndReadOnlyPreconditions(t *testing.T) {
	for _, r := range []SlotReadRequest{{}, {Slot: 9, Sense: SenseHF, TagType: 1001}, {Slot: 1, Sense: SenseHF, TagType: 3000}, {Slot: 1, Sense: SenseLF, TagType: 202}} {
		if _, e := r.Requirements(); e == nil {
			t.Fatal(r)
		}
	}
	r := slotSnapshotRequest(1000)
	for _, reason := range []string{"capabilities", "mode", "native"} {
		t.Run(reason, func(t *testing.T) {
			steps := []readerStep{}
			if reason != "native" {
				caps := slotSnapshotCapabilities(r)
				if reason == "capabilities" {
					caps = nil
				}
				steps = append(steps, readerStep{command: 1035, status: 0x68, data: caps})
			}
			if reason == "mode" {
				steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}})
			}
			c := readerSimulator(t, steps)
			if reason == "native" {
				c.transportInfo.NativeUSBMaintenance = true
			}
			out, e := c.ReadCurrentSlot(context.Background(), r)
			if e == nil || out.Read != 0 {
				t.Fatal(out, e)
			}
		})
	}
}
