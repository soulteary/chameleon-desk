package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func slotTypes(hf, lf uint16, slot int) []byte {
	b := make([]byte, 32)
	b[(slot-1)*4] = byte(hf >> 8)
	b[(slot-1)*4+1] = byte(hf)
	b[(slot-1)*4+2] = byte(lf >> 8)
	b[(slot-1)*4+3] = byte(lf)
	return b
}
func TestSelectedMemoryLastUnitRestoresBeforeSave(t *testing.T) {
	data := bytes.Repeat([]byte{0xa5}, 16)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, request: []byte{0}, status: 0x68}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1003, request: []byte{1}, status: 0x68}, {command: 1018, status: 0x68, data: []byte{1}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 2)}, {command: 4000, request: append([]byte{63}, data...), status: 0x68}, {command: 4008, request: []byte{63, 1}, status: 0x68, data: data}, {command: 1003, request: []byte{0}, status: 0x68}, {command: 1009, status: 0x68}, {command: 1001, request: []byte{1}, status: 0x68}}
	c := readerSimulator(t, steps)
	v, e := c.LoadSlotMemoryFile(context.Background(), 2, 63, data, "bin", "classic", true)
	if e != nil || !v.Verified || !v.Saved || v.Units != 1 {
		t.Fatal(v, e)
	}
}
func TestMemoryTypeMismatchNeverWrites(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 1)}})
	if _, e := c.LoadSlotMemoryFile(context.Background(), 0, 0, make([]byte, 16), "bin", "type2", false); e == nil {
		t.Fatal("type2 operation wrote Classic memory")
	}
}
func TestMemoryFormatsAndFinalBlock(t *testing.T) {
	data := make(HexBytes, 1024)
	data[1023] = 0xab
	i := EmulatorImage{1, 1001, 16, 1, data, imageDigest(data)}
	for _, kind := range []string{"bin", "eml", "json"} {
		b, e := EncodeMemoryFile(i, kind)
		if e != nil {
			t.Fatal(e)
		}
		got, e := DecodeMemoryFile(b, kind)
		if e != nil || !bytes.Equal(got, data) {
			t.Fatal(kind, e)
		}
	}
	if _, e := DecodeMemoryFile([]byte("0G"), "eml"); e == nil {
		t.Fatal("invalid hex accepted")
	}
}
func classicConfigSteps(gen2 byte) []readerStep {
	return []readerStep{{command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 1)}, {command: 4018, status: 0x68, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}, {command: 4009, status: 0x68, data: []byte{0, 0, gen2, 0, 0}}, {command: 4039, status: 0x68, data: []byte{0}}, {command: 4040, status: 0x68, data: []byte{1}}}
}
func TestEmulatorPatchPreservesUnspecifiedFields(t *testing.T) {
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}}
	steps = append(steps, classicConfigSteps(0)...)
	steps = append(steps, readerStep{command: 4013, request: []byte{1}, status: 0x68}, readerStep{command: 4012, status: 0x68, data: []byte{1}})
	steps = append(steps, classicConfigSteps(1)...)
	c := readerSimulator(t, steps)
	yes := true
	v, e := c.ConfigureSlotEmulator(context.Background(), 0, EmulatorPatch{Family: "classic", Gen2: &yes}, false)
	if e != nil || !v.Classic.Gen2 || v.Classic.Gen1A || v.Classic.PRNG != 1 {
		t.Fatal(v, e)
	}
}
func TestEmulatorWrongFamilyFailsBeforeWrite(t *testing.T) {
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}}
	steps = append(steps, classicConfigSteps(0)...)
	c := readerSimulator(t, steps)
	yes := true
	if _, e := c.ConfigureSlotEmulator(context.Background(), 0, EmulatorPatch{Family: "type2", UIDMagic: &yes}, false); e == nil {
		t.Fatal("wrong family accepted")
	}
}
func TestNicknameAbsenceAndDeleteIdempotence(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1008, request: []byte{2, 2}, status: 0x71}})
	if e := c.UpdateSlotNickname(context.Background(), 3, SenseHF, nil); e != nil {
		t.Fatal(e)
	}
}
func TestNicknameUnicodeReadback(t *testing.T) {
	name := "测试槽"
	c := readerSimulator(t, []readerStep{{command: 1007, request: append([]byte{1, 1}, []byte(name)...), status: 0x68}, {command: 1008, request: []byte{1, 1}, status: 0x68, data: []byte(name)}})
	if e := c.UpdateSlotNickname(context.Background(), 2, SenseLF, &name); e != nil {
		t.Fatal(e)
	}
}
func TestType2PasswordLogNoProgress(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1103, 0, 1)}, {command: 4030, status: 0x68, data: []byte{16}}, {command: 4034, status: 0x68, data: []byte{0, 0, 0, 1}}, {command: 4035, request: []byte{0, 0, 0, 0}, status: 0x68}})
	if _, e := c.Type2DetectionLogs(context.Background(), 0, 0, 10); e == nil {
		t.Fatal("zero-progress log accepted")
	}
}
func TestSettingsViewRedactsPinAndIncludesThreshold(t *testing.T) {
	raw := append([]byte{6, 1, 0, 1, 2, 3, 1}, []byte("123456")...)
	c := readerSimulator(t, []readerStep{{command: 1034, status: 0x68, data: raw}, {command: 1039, status: 0x68, data: []byte{10}}, {command: 1041, status: 0x68, data: []byte{1, 244}}})
	v, e := c.SettingsView(context.Background(), false)
	if e != nil || v.LongPressMS == nil || *v.LongPressMS != 500 || v.SleepSeconds == nil || *v.SleepSeconds != 10 {
		t.Fatal(v, e)
	}
	b, _ := json.Marshal(v)
	if bytes.Contains(b, []byte("123456")) || v.Raw != nil {
		t.Fatal("PIN leaked")
	}
}
func TestSettingsPatchPreflightAndReadback(t *testing.T) {
	one := byte(1)
	raw := append([]byte{6, 1, 0, 1, 2, 3, 1}, []byte("123456")...)
	c := readerSimulator(t, []readerStep{{command: 1016, status: 0x68, data: []byte{0}}, {command: 1015, request: []byte{1}, status: 0x68}, {command: 1016, status: 0x68, data: []byte{1}}, {command: 1013, status: 0x68}, {command: 1034, status: 0x68, data: raw}, {command: 1039, status: 0x68, data: []byte{10}}, {command: 1041, status: 0x68, data: []byte{1, 244}}})
	v, e := c.ConfigureSettings(context.Background(), SettingsPatch{Animation: &one}, true)
	if e != nil || v.Animation != 1 {
		t.Fatal(v, e)
	}
}
func TestUnsupportedSettingPreflightNeverWrites(t *testing.T) {
	value := byte(3)
	c := readerSimulator(t, []readerStep{{command: 1039, status: 105}})
	if _, e := c.ConfigureSettings(context.Background(), SettingsPatch{SleepSeconds: &value}, false); e == nil {
		t.Fatal("unsupported sleep setter attempted")
	}
}
func TestOpenAllInitializationPlanAndReadback(t *testing.T) {
	d := newBackupDevice()
	c := backupSimulator(t, d)
	result, e := c.InitializeAllSlots(context.Background(), 1001, 100)
	if e != nil || result.CompletedSlots != 8 || !result.Verified || !result.Saved {
		t.Fatal(result, e)
	}
	slots, e := c.Slots(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range slots {
		if s.HFType != 1001 || s.LFType != 100 || !s.HFEnabled || !s.LFEnabled {
			t.Fatal(s)
		}
	}
	mode, e := c.Mode(context.Background())
	if e != nil || mode != "reader" {
		t.Fatal(mode, e)
	}
}
func TestInvalidOpenAllFailsBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.InitializeAllSlots(context.Background(), 3000, 100); e == nil {
		t.Fatal("unexportable type accepted")
	}
}
func TestType2LogLimitAndSnapshotIndex(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1103, 0, 1)}, {command: 4030, status: 0x68, data: []byte{16}}, {command: 4034, status: 0x68, data: []byte{0, 0, 0, 3}}, {command: 4035, request: []byte{0, 0, 0, 1}, status: 0x68, data: []byte{1, 2, 3, 4, 5, 6, 7, 8}}})
	v, e := c.Type2DetectionLogs(context.Background(), 0, 1, 1)
	if e != nil || len(v) != 1 || v[0].String() != "01020304" {
		t.Fatal(v, e)
	}
}
func TestMemoryReadbackFailureRestoresWithoutFinalSave(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 16)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1003, request: []byte{1}, status: 0x68}, {command: 1018, status: 0x68, data: []byte{1}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 2)}, {command: 4000, request: append([]byte{0}, data...), status: 0x68}, {command: 4008, request: []byte{0, 1}, status: 0x68, data: make([]byte, 16)}, {command: 1003, request: []byte{0}, status: 0x68}})
	v, e := c.LoadSlotMemory(context.Background(), 2, 0, data, true)
	if e == nil || v.Verified || v.Saved {
		t.Fatal(v, e)
	}
}
func TestSlotPRNGReadback(t *testing.T) {
	value := byte(2)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 1)}, {command: 4041, request: []byte{2}, status: 0x68}, {command: 4040, status: 0x68, data: []byte{2}}})
	if got, e := c.SlotPRNG(context.Background(), 0, &value, false); e != nil || got != 2 {
		t.Fatal(got, e)
	}
}
func TestSlotDetailsSeparatesNicknameAndType(t *testing.T) {
	steps := []readerStep{{command: 1019, status: 0x68, data: slotTypes(1001, 100, 1)}, {command: 1023, status: 0x68, data: make([]byte, 16)}, {command: 1018, status: 0x68, data: []byte{0}}}
	for n := byte(0); n < 8; n++ {
		for _, sense := range []byte{2, 1} {
			s := readerStep{command: 1008, request: []byte{n, sense}, status: 0x71}
			if n == 0 && sense == 2 {
				s.status = 0x68
				s.data = []byte("测试卡")
			}
			steps = append(steps, s)
		}
	}
	c := readerSimulator(t, steps)
	v, e := c.SlotDetails(context.Background())
	if e != nil || len(v) != 8 || v[0].HFNickname == nil || *v[0].HFNickname != "测试卡" || v[0].HFName != "MIFARE Classic 1K" || v[0].LFNickname != nil {
		t.Fatal(v, e)
	}
}
func TestNativeSlotInitializationAndDeletionEncoding(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1005, request: []byte{7, 3, 233}, status: 0x68}, {command: 1024, request: []byte{7, 1}, status: 0x68}})
	if e := c.InitializeSlot(context.Background(), 8, 1001); e != nil {
		t.Fatal(e)
	}
	if e := c.DeleteSlot(context.Background(), 8, SenseLF); e != nil {
		t.Fatal(e)
	}
}
func TestClearSlotRemovesLeftoverNickname(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1024, request: []byte{7, 2}, status: 0x68}, {command: 1008, request: []byte{7, 2}, status: 0x68, data: []byte("old")}, {command: 1021, request: []byte{7, 2}, status: 0x68}})
	if e := c.ClearSlot(context.Background(), 8, SenseHF); e != nil {
		t.Fatal(e)
	}
	c = readerSimulator(t, []readerStep{{command: 1024, request: []byte{7, 1}, status: 0x68}, {command: 1008, request: []byte{7, 1}, status: 0x71}})
	if e := c.ClearSlot(context.Background(), 8, SenseLF); e != nil {
		t.Fatal(e)
	}
	c = readerSimulator(t, []readerStep{{command: 1024, request: []byte{7, 1}, status: 0x68}, {command: 1008, request: []byte{7, 1}, status: 0x70}})
	if e := c.ClearSlot(context.Background(), 8, SenseLF); e == nil {
		t.Fatal("unexpected nickname read failure ignored")
	}
}
func TestSettingMismatchDoesNotSave(t *testing.T) {
	value := byte(2)
	c := readerSimulator(t, []readerStep{{command: 1016, status: 0x68, data: []byte{0}}, {command: 1015, request: []byte{2}, status: 0x68}, {command: 1016, status: 0x68, data: []byte{0}}})
	if _, e := c.ConfigureSettings(context.Background(), SettingsPatch{Animation: &value}, true); e == nil {
		t.Fatal("mismatch accepted")
	}
}
func type2ConfigSteps(counterValue byte) []readerStep {
	return []readerStep{{command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1105, 0, 1)}, {command: 4030, status: 0x68, data: []byte{20}}, {command: 4018, status: 0x68, data: []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}}, {command: 4037, status: 0x68, data: []byte{0, 0, 0}}, {command: 4023, status: 0x68, data: make([]byte, 8)}, {command: 4025, status: 0x68, data: make([]byte, 32)}, {command: 4027, request: []byte{0}, status: 0x68, data: []byte{counterValue, 0, 0, 0xbd}}, {command: 4027, request: []byte{1}, status: 0x68, data: []byte{0, 0, 0, 0xbd}}, {command: 4027, request: []byte{2}, status: 0x68, data: []byte{0, 0, 0, 0xbd}}}
}
func TestType2CompositePatchCounterAndAuthReset(t *testing.T) {
	value := uint32(7)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}}
	steps = append(steps, type2ConfigSteps(0)...)
	steps = append(steps, readerStep{command: 4028, request: []byte{0, 7, 0, 0}, status: 0x68}, readerStep{command: 4027, request: []byte{0}, status: 0x68, data: []byte{7, 0, 0, 0xbd}}, readerStep{command: 4029, status: 0x68, data: []byte{3}})
	steps = append(steps, type2ConfigSteps(7)...)
	c := readerSimulator(t, steps)
	v, e := c.ConfigureSlotEmulator(context.Background(), 0, EmulatorPatch{Family: "type2", Counters: map[byte]EmulatorCounterPatch{0: {Value: &value}}, ResetAuthAttempts: true}, false)
	if e != nil || v.Type2.Counters[0].Value != 7 {
		t.Fatal(v, e)
	}
}
func TestCounterPatchRequiresExplicitValue(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.ConfigureSlotEmulator(context.Background(), 0, EmulatorPatch{Counters: map[byte]EmulatorCounterPatch{0: {ResetTearing: true}}}, false); e == nil {
		t.Fatal("implicit zero counter overwrite accepted")
	}
}
