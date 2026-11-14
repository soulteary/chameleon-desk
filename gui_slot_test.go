package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func guiTestCard(t *testing.T, tag int, uid string, rows [][]int, more map[string]any) GUICard {
	t.Helper()
	m := map[string]any{"id": "card-1", "name": "门禁", "uid": uid, "tag": tag, "sak": 8, "atqa": []int{0, 4}, "ats": []int{}, "data": rows}
	for k, v := range more {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	c, e := ParseGUICard(b)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func guiRows(n, width int, fill byte) [][]int {
	rows := make([][]int, n)
	for i := range rows {
		rows[i] = make([]int, width)
		for j := range rows[i] {
			rows[i][j] = int(fill) + i
		}
	}
	return rows
}

func TestPlanGUIClassicOrderIdentityAndSparseRows(t *testing.T) {
	rows := guiRows(64, 16, 0)
	rows[5] = []int{}
	p, e := PlanGUICardLoad(guiTestCard(t, 1001, "01 02 03 04", rows, nil), 3)
	if e != nil {
		t.Fatal(e)
	}
	want := []Command{CmdSetSlotEnable, CmdSetActiveSlot, CmdSetSlotTagType, CmdSetSlotDataDefault, CmdHf14aSetAntiCollData}
	for i, cmd := range want {
		if p.Steps[i].Command != cmd {
			t.Fatalf("step %d is %s", i, p.Steps[i].Name)
		}
	}
	// ATQA is stored numerically (00 04) and sent in wire order (04 00).
	if !bytes.Equal(p.Steps[4].Payload, []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}) {
		t.Fatalf("identity %x", p.Steps[4].Payload)
	}
	if !bytes.Equal(p.Steps[0].Payload, []byte{2, 2, 1}) || !bytes.Equal(p.Steps[2].Payload, []byte{2, 0x03, 0xe9}) {
		t.Fatal("slot payloads", p.Steps[0].Payload, p.Steps[2].Payload)
	}
	if p.MemoryUnits != 63 || p.SkippedRows != 1 || p.Sense != "hf" {
		t.Fatal(p.MemoryUnits, p.SkippedRows)
	}
	starts := []byte{}
	for _, s := range p.Steps {
		if s.Command == CmdMf1WriteEmuBlockData {
			if len(s.Payload) > 1+guiChunkBytes {
				t.Fatal("chunk too large")
			}
			starts = append(starts, s.Payload[0])
		}
	}
	if !bytes.Equal(starts, []byte{0, 6, 14, 22, 30, 38, 46, 54, 62}) {
		t.Fatal("run starts", starts)
	}
	last := p.Steps[len(p.Steps)-1]
	if last.Command != CmdSetSlotTagNick || string(last.Payload[2:]) != "门禁" {
		t.Fatal(last)
	}
}

func TestPlanGUIClassicEV1AndRejections(t *testing.T) {
	p, e := PlanGUICardLoad(guiTestCard(t, 1001, "01020304", guiRows(72, 16, 1), nil), 1)
	if e != nil || !p.ClassicEV1 || p.SlotTagType != 1002 || p.MemoryUnits != 72 {
		t.Fatal(p.ClassicEV1, p.SlotTagType, p.MemoryUnits, e)
	}
	bad := guiRows(4, 16, 0)
	bad[2] = []int{1, 2, 3}
	if _, e = PlanGUICardLoad(guiTestCard(t, 1001, "01020304", bad, nil), 1); e == nil {
		t.Fatal("short row accepted")
	}
	if _, e = PlanGUICardLoad(guiTestCard(t, 1001, "01020304", nil, nil), 9); e == nil {
		t.Fatal("slot 9 accepted")
	}
	if _, e = PlanGUICardLoad(guiTestCard(t, 202, "0102030405", nil, nil), 1); e == nil {
		t.Fatal("unsupported LF type accepted")
	}
	if _, e = PlanGUICardLoad(guiTestCard(t, 100, "01020304", nil, nil), 1); e == nil {
		t.Fatal("four-byte EM410x ID accepted")
	}
	long := guiTestCard(t, 100, "0102030405", nil, map[string]any{"name": strings.Repeat("卡", 20)})
	p, e = PlanGUICardLoad(long, 1)
	if e != nil || len(p.Nickname) != 30 {
		t.Fatal(len(p.Nickname), e)
	}
}

func TestPlanGUIType2Extras(t *testing.T) {
	extra := map[string]any{"ultralightVersion": []int{0, 4, 4, 2, 1, 0, 17, 3}, "ultralightSignature": make([]int, 32), "ultralightCounters": []int{0x030201}}
	c := guiTestCard(t, 1101, "04112233445566", guiRows(135, 4, 0), map[string]any{"sak": 0, "atqa": []int{0, 0x44}, "extra": extra})
	p, e := PlanGUICardLoad(c, 8)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[Command]HexBytes{}
	pages := 0
	for _, s := range p.Steps {
		seen[s.Command] = s.Payload
		if s.Command == CmdMf0NtagWriteEmuPageData {
			pages += int(s.Payload[1])
			if len(s.Payload) != 2+4*int(s.Payload[1]) {
				t.Fatal("page payload shape")
			}
		}
	}
	if pages != 135 || len(seen[CmdMf0NtagSetVersionData]) != 8 || len(seen[CmdMf0NtagSetSignatureData]) != 32 {
		t.Fatal(pages)
	}
	if !bytes.Equal(seen[CmdMf0NtagSetCounterData], []byte{0x80, 1, 2, 3}) || seen[CmdMf0NtagResetAuthCnt] == nil {
		t.Fatal("counter/auth reset", seen[CmdMf0NtagSetCounterData])
	}
	extra["ultralightVersion"] = []int{1, 2}
	c = guiTestCard(t, 1101, "04112233445566", nil, map[string]any{"extra": extra})
	if _, e = PlanGUICardLoad(c, 1); e == nil {
		t.Fatal("short version accepted")
	}
}

func guiCaps(p GUICardLoadPlan, extra ...Command) []byte {
	list := []uint16{uint16(CmdGetActiveSlot), uint16(CmdGetSlotTagNick), uint16(CmdSlotDataConfigSave)}
	for _, s := range p.Steps {
		list = append(list, uint16(s.Command))
	}
	for _, c := range extra {
		list = append(list, uint16(c))
	}
	return capabilities(list...)
}

func TestLoadGUICardVerifiesRestoresAndSaves(t *testing.T) {
	card := guiTestCard(t, 1001, "01020304", guiRows(2, 16, 0x10), nil)
	p, _ := PlanGUICardLoad(card, 2)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, request: []byte{0}, status: 0x68},
		{command: 1035, status: 0x68, data: guiCaps(p, CmdHf14aGetAntiCollData, CmdMf1ReadEmuBlockData)},
		{command: 1018, status: 0x68, data: []byte{0}}}
	for _, s := range p.Steps {
		steps = append(steps, readerStep{command: uint16(s.Command), request: s.Payload, status: 0x68})
	}
	steps = append(steps,
		readerStep{command: 4018, status: 0x68, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}},
		readerStep{command: 4008, request: []byte{0, 2}, status: 0x68, data: p.runs[0].data},
		readerStep{command: 1008, request: []byte{1, 2}, status: 0x68, data: []byte("门禁")},
		readerStep{command: 1003, request: []byte{0}, status: 0x68},
		readerStep{command: 1009, status: 0x68},
		readerStep{command: 1001, request: []byte{1}, status: 0x68})
	c := readerSimulator(t, steps)
	v, e := c.LoadGUICard(context.Background(), 2, card, GUICardLoadOptions{Persist: true})
	if e != nil || !v.Verified || !v.Persisted || v.Activated {
		t.Fatal(v.Verified, v.Persisted, v.Activated, e)
	}
}

func TestLoadGUICardMismatchNeverSaves(t *testing.T) {
	card := guiTestCard(t, 100, "0102030405", nil, nil)
	p, _ := PlanGUICardLoad(card, 1)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}},
		{command: 1035, status: 0x68, data: guiCaps(p, CmdEm410xGetEmuId)},
		{command: 1018, status: 0x68, data: []byte{0}}}
	for _, s := range p.Steps {
		steps = append(steps, readerStep{command: uint16(s.Command), request: s.Payload, status: 0x68})
	}
	steps = append(steps, readerStep{command: uint16(CmdEm410xGetEmuId), status: 0x68, data: []byte{0, 100, 9, 9, 9, 9, 9}})
	c := readerSimulator(t, steps)
	v, e := c.LoadGUICard(context.Background(), 1, card, GUICardLoadOptions{Persist: true, Activate: true})
	if e == nil || v.Verified || v.Persisted || v.Activated {
		t.Fatal("mismatch accepted", v, e)
	}
}

func TestLoadGUICardMissingCapabilityWritesNothing(t *testing.T) {
	card := guiTestCard(t, 100, "0102030405", nil, nil)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1035, status: 0x68, data: capabilities(1018, 1003)}})
	if _, e := c.LoadGUICard(context.Background(), 1, card, GUICardLoadOptions{}); e == nil || !strings.Contains(e.Error(), "nothing written") {
		t.Fatal(e)
	}
}

func TestGUILibraryCRUD(t *testing.T) {
	d := NewGUIDatabase()
	card := guiTestCard(t, 1001, "01020304", guiRows(1, 16, 0), nil)
	d2, e := d.AddCard(card)
	if e != nil {
		t.Fatal(e)
	}
	if n, _ := d.Records(false, "", ""); len(n) != 0 {
		t.Fatal("source database mutated")
	}
	if _, e = d2.AddCard(card); e == nil {
		t.Fatal("duplicate ID accepted")
	}
	lf := guiTestCard(t, 100, "0102030405", nil, map[string]any{"id": "card-2", "name": "Garage"})
	if d2, e = d2.AddCard(lf); e != nil {
		t.Fatal(e)
	}
	if v, _ := d2.Records(false, "garage", "lf"); len(v) != 1 || v[0].Frequency != "lf" || v[0].TagName != "EM410X" {
		t.Fatal(v)
	}
	if v, _ := d2.Records(false, "", "hf"); len(v) != 1 || v[0].ID != "card-1" {
		t.Fatal(v)
	}
	if _, e = d2.Card("card-"); e == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	name, color, bad := "新名字", "#12ABef", "red"
	got, _ := d2.Card("Garage")
	if _, e = got.Edit(GUICardPatch{Color: &bad}); e == nil {
		t.Fatal("bad color accepted")
	}
	edited, e := got.Edit(GUICardPatch{Name: &name, Color: &color})
	if e != nil || edited.Color() != "#12abef" {
		t.Fatal(edited.Color(), e)
	}
	if d2, e = d2.ReplaceCard(edited); e != nil {
		t.Fatal(e)
	}
	missing := "nope"
	if _, e = edited.Edit(GUICardPatch{FolderID: &missing}); e != nil {
		t.Fatal(e) // folder existence is a database-level check
	}
	moved, _ := edited.Edit(GUICardPatch{FolderID: &missing})
	if _, e = d2.ReplaceCard(moved); e == nil {
		t.Fatal("missing folder accepted")
	}
	d3, id, e := d2.DeleteRecord(false, "card-1")
	if e != nil || id != "card-1" {
		t.Fatal(id, e)
	}
	if v, _ := d3.Records(false, "", "all"); len(v) != 1 || v[0].Name != name {
		t.Fatal(v)
	}
	var dict GUIDictionary
	if e = json.Unmarshal([]byte(`{"id":"d1","name":"keys","keys":[[255,255,255,255,255,255]]}`), &dict); e != nil {
		t.Fatal(e)
	}
	if d3, e = d3.AddDictionary(dict); e != nil {
		t.Fatal(e)
	}
	if v, _ := d3.Records(true, "", ""); len(v) != 1 || v[0].Keys != 1 || v[0].Kind != "dictionary" {
		t.Fatal(v)
	}
}

func TestGUICardDumpRoundTrip(t *testing.T) {
	dump := make([]byte, 1024)
	copy(dump, []byte{0xde, 0xad, 0xbe, 0xef})
	c, e := NewGUICardFromDump("dump", 1001, dump, GUICardPatch{})
	if e != nil || c.UID != "DEADBEEF" || len(c.Data) != 64 || len(c.ID) != 36 {
		t.Fatal(c.UID, len(c.Data), e)
	}
	b, _ := json.Marshal(c)
	back, e := ParseGUICard(b)
	if e != nil {
		t.Fatal(e)
	}
	out, e := back.Dump()
	if e != nil || !bytes.Equal(out, dump) {
		t.Fatal(e)
	}
	if _, e = NewGUICardFromDump("x", 1001, make([]byte, 1025), GUICardPatch{}); e == nil {
		t.Fatal("misaligned dump accepted")
	}
	if _, e = NewGUICardFromDump("x", 100, make([]byte, 5), GUICardPatch{}); e == nil {
		t.Fatal("LF dump accepted")
	}
}
