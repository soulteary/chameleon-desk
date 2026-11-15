package chameleon

import "testing"

func featureModelID(id byte) *byte { return &id }
func featureTestContext() FeatureContext {
	caps := []uint16{}
	for _, c := range Commands() {
		caps = append(caps, uint16(c.ID))
	}
	return FeatureContext{Device: &FeatureDevice{ModelID: featureModelID(0), Capabilities: caps, Transport: serialTransport()}, WritesAllowed: true}
}
func hasFeatureReason(a FeatureAvailability, code string) bool {
	for _, r := range a.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
func TestFeatureAvailabilityKnowledgeAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, id, surface, state, code string
		modify                         func(*FeatureContext)
	}{
		{"ultra", "reader.hf-scan", "web", "available", "", func(*FeatureContext) {}},
		{"lite readers", "reader.lf-scan", "web", "unavailable", "unsupported_model", func(c *FeatureContext) { c.Device.ModelID = featureModelID(1) }},
		{"unknown model", "reader.hf-scan", "web", "unavailable", "unsupported_model", func(c *FeatureContext) { c.Device.ModelID = featureModelID(99) }},
		{"missing model", "reader.hf-scan", "web", "unknown", "model_unknown", func(c *FeatureContext) { c.Device.ModelID = nil }},
		{"unknown device", "reader.hf-scan", "web", "unknown", "device_unknown", func(c *FeatureContext) { c.Device = nil }},
		{"unknown capabilities", "reader.hf-scan", "web", "unknown", "capabilities_unknown", func(c *FeatureContext) { c.Device.Capabilities = nil }},
		{"empty capabilities", "reader.hf-scan", "web", "unavailable", "missing_commands", func(c *FeatureContext) { c.Device.Capabilities = []uint16{} }},
		{"read only", "slot.activate", "web", "unavailable", "writes_disabled", func(c *FeatureContext) { c.WritesAllowed = false }},
		{"missing Web entry", "classic.read", "web", "unavailable", "no_entry", func(*FeatureContext) {}},
		{"offline", "files.acl-decode", "web", "available", "", func(c *FeatureContext) { c.Device = nil; c.WritesAllowed = false }},
		{"unresolved patch", "device.configure", "sdk", "unknown", "parameters_required", func(*FeatureContext) {}},
		{"BLE pairing", "device.pairing", "sdk", "unavailable", "transport_restricted", func(c *FeatureContext) { c.Device.Transport = bleTransport("tcp", true) }},
		{"unknown TCP restore", "backup.restore", "sdk", "unavailable", "transport_restricted", func(c *FeatureContext) {
			c.Device.Transport = TransportInfo{Connection: "tcp", Medium: "unknown", AllowsDeviceRestore: true}
		}},
		{"trusted custom not certified wired", "backup.restore", "sdk", "unavailable", "transport_restricted", func(c *FeatureContext) {
			c.Device.Transport = TransportInfo{Connection: "custom", Medium: "caller-managed", AllowsDeviceRestore: true}
		}},
		{"DFU external context", "firmware.flash", "sdk", "unknown", "parameters_required", func(*FeatureContext) {}},
		{"removed native BLE", "transport.native-ble", "sdk", "unavailable", "no_entry", func(*FeatureContext) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := featureTestContext()
			tc.modify(&ctx)
			a, e := EvaluateFeature(tc.id, tc.surface, ctx, nil)
			if e != nil || a.State != tc.state || (tc.code != "" && !hasFeatureReason(a, tc.code)) {
				t.Fatalf("%+v %v", a, e)
			}
		})
	}
}
func TestCardFeatureRequirementsFollowPlanAndOptions(t *testing.T) {
	// LF emulation is supported by both models; no HF memory or unrelated LF
	// commands may be required for this exact card.
	card, err := ParseGUICard([]byte(`{"id":"x","name":"LF","tag":100,"uid":"1122334455","sak":0,"atqa":[0,0],"ats":[],"data":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := featureTestContext()
	ctx.Device.ModelID = featureModelID(1)
	req := FeatureRequest{Card: &card, Slot: 1}
	exact, e := GUICardLoadRequirements(card, 1, GUICardLoadOptions{})
	if e != nil {
		t.Fatal(e)
	}
	ctx.Device.Capabilities = nil
	for _, c := range exact {
		ctx.Device.Capabilities = append(ctx.Device.Capabilities, uint16(c))
	}
	a, e := EvaluateFeature("slot.load-card", "web", ctx, &req)
	if e != nil || a.State != "available" {
		t.Fatalf("%+v %v", a, e)
	}
	for _, c := range a.RequiredCommands {
		if c == CmdMf1WriteEmuBlockData || c == CmdMf0NtagWriteEmuPageData || c == CmdSlotDataConfigSave {
			t.Fatalf("irrelevant command %s", c)
		}
	}
	req.Persist = true
	a, e = EvaluateFeature("slot.load-card", "web", ctx, &req)
	if e != nil || a.State != "unavailable" || !hasFeatureReason(a, "missing_commands") {
		t.Fatalf("%+v %v", a, e)
	}
	req.Slot = 0
	a, _ = EvaluateFeature("slot.load-card", "web", ctx, &req)
	if !hasFeatureReason(a, "invalid_parameters") {
		t.Fatal(a)
	}
}
func TestRawFeatureUsesExistingTransportGuard(t *testing.T) {
	ctx := featureTestContext()
	ctx.Device.Transport = bleTransport("tcp", true)
	cmd := CmdResetSettings
	a, e := EvaluateFeature("protocol.raw", "sdk", ctx, &FeatureRequest{Command: &cmd})
	if e != nil || !hasFeatureReason(a, "transport_restricted") {
		t.Fatalf("%+v %v", a, e)
	}
	ctx.Device.Transport = TransportInfo{Connection: "tcp", Medium: "unknown", AllowsPairingChanges: true}
	a, e = EvaluateFeature("protocol.raw", "sdk", ctx, &FeatureRequest{Command: &cmd})
	if e != nil || !hasFeatureReason(a, "transport_restricted") {
		t.Fatalf("TCP claim elevated permissions: %+v %v", a, e)
	}
	ctx.Device.Transport = serialTransport()
	ctx.WritesAllowed = false
	a, e = EvaluateFeature("protocol.raw", "sdk", ctx, &FeatureRequest{Command: &cmd})
	if e != nil || !hasFeatureReason(a, "writes_disabled") {
		t.Fatalf("%+v %v", a, e)
	}
	cmd = CmdGetDeviceMode
	a, e = EvaluateFeature("protocol.raw", "sdk", ctx, &FeatureRequest{Command: &cmd})
	if e != nil || a.State != "available" {
		t.Fatalf("%+v %v", a, e)
	}
	if _, e = EvaluateFeature("not-a-feature", "sdk", ctx, nil); e == nil {
		t.Fatal("unknown feature accepted")
	}
	if _, e = EvaluateFeatures("browser", ctx); e == nil {
		t.Fatal("unknown surface accepted")
	}
}
func TestFeatureCatalogCopiesAndExplicitFirmwareCheck(t *testing.T) {
	a := Features()
	a[0].Commands[0] = 999
	a[0].SDK.Entries[0] = "bad"
	if Features()[0].Commands[0] == 999 || Features()[0].SDK.Entries[0] == "bad" {
		t.Fatal("catalog aliases caller slices")
	}
	v, e := EvaluateFeature("firmware.check", "sdk", FeatureContext{}, &FeatureRequest{Model: "ultra", Version: "v2.0"})
	if e != nil || v.State != "available" || len(v.RequiredCommands) != 0 {
		t.Fatalf("%+v %v", v, e)
	}
}
