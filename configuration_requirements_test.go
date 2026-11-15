package chameleon

import (
	"reflect"
	"testing"
)

func TestSelectedConfigurationRequirements(t *testing.T) {
	animation := byte(2)
	req := &FeatureRequest{SettingsPatch: &SettingsPatch{Animation: &animation}, Persist: true}
	ctx := featureTestContext()
	ctx.Device.ModelID = featureModelID(1)
	ctx.Device.Capabilities = []uint16{1015, 1016, 1034, 1013}
	a, err := EvaluateFeature("device.configure", "web", ctx, req)
	if err != nil || a.State != "available" || !reflect.DeepEqual(a.RequiredCommands, []Command{1013, 1015, 1016, 1034}) {
		t.Fatal(a, err)
	}
	pin := "123456"
	req.SettingsPatch = &SettingsPatch{BLEKey: &pin}
	ctx = featureTestContext()
	ctx.Device.Transport = TransportInfo{Connection: "tcp", Medium: "serial", AllowsPairingChanges: true}
	a, _ = EvaluateFeature("device.configure", "web", ctx, req)
	if !hasFeatureReason(a, "transport_restricted") {
		t.Fatal(a)
	}
	name := "测试"
	edit := SlotEdit{Slot: 1, Sense: SenseHF, Action: "nickname", Name: &name}
	ctx = featureTestContext()
	ctx.Device.Capabilities = []uint16{1007, 1008}
	a, err = EvaluateFeature("slot.configure", "web", ctx, &FeatureRequest{SlotEdit: &edit})
	if err != nil || a.State != "available" {
		t.Fatal(a, err)
	}
	edit.Persist = true
	a, _ = EvaluateFeature("slot.configure", "web", ctx, &FeatureRequest{SlotEdit: &edit})
	if !hasFeatureReason(a, "missing_commands") {
		t.Fatal(a)
	}
}
func TestSlotEditValidation(t *testing.T) {
	tag := uint16(1001)
	enabled := true
	for _, edit := range []SlotEdit{{Slot: 0, Sense: SenseHF, Action: "enable", Enabled: &enabled}, {Slot: 1, Sense: SenseLF, Action: "type", Tag: &tag}, {Slot: 1, Sense: SenseHF, Action: "initialize", Tag: &tag}, {Slot: 1, Sense: SenseHF, Action: "nickname"}, {Slot: 1, Sense: SenseHF, Action: "enable", Enabled: &enabled, Tag: &tag}} {
		if _, err := edit.Requirements(); err == nil {
			t.Fatal(edit)
		}
	}
	if _, err := SettingsRequirements(SettingsPatch{}, false); err == nil {
		t.Fatal("empty patch accepted")
	}
}
