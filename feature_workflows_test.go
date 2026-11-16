package chameleon

import (
	"slices"
	"testing"
)

func TestModeledType2FeatureRequiresCleanupAndUltra(t *testing.T) {
	model := byte(0)
	device := &FeatureDevice{ModelID: &model, Capabilities: []uint16{1001, 1002, 2000, 2010, 2101}, Transport: TransportInfo{Connection: "tcp", Medium: "usb-serial"}}
	input := Type2WorkflowRead{UID: HexBytes{4, 1, 2, 3, 4, 5, 6}, Full: true}
	check := func() FeatureAvailability {
		t.Helper()
		v, err := EvaluateFeature("reader.type2-workflow", "web", FeatureContext{Device: device}, &FeatureRequest{Type2Workflow: &input})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := check(); v.State != "available" || !slices.Contains(v.RequiredCommands, CmdHf14aSetFieldOff) {
		t.Fatal(v)
	}
	device.Capabilities = device.Capabilities[:4]
	v := check()
	if v.State != "unavailable" {
		t.Fatal(v)
	}
	missing := false
	for _, r := range v.Reasons {
		if r.Code == "missing_commands" && slices.Contains(r.Commands, CmdHf14aSetFieldOff) {
			missing = true
		}
	}
	if !missing {
		t.Fatal(v)
	}
	device.Capabilities = append(device.Capabilities, 2101)
	model = 1
	if v = check(); v.State != "unavailable" {
		t.Fatal(v)
	}
}

func TestIOProxFeatureSelectsDecodeOrComposeWithoutUnion(t *testing.T) {
	model := byte(1)
	device := &FeatureDevice{ModelID: &model, Capabilities: []uint16{3012}, Transport: TransportInfo{Connection: "tcp", Medium: "ble"}}
	input := LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: make(HexBytes, 16)}
	check := func() FeatureAvailability {
		t.Helper()
		v, err := EvaluateFeature("library.edit-lf-device", "web", FeatureContext{Device: device}, &FeatureRequest{LFIdentity: &input})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := check(); v.State != "available" || len(v.RequiredCommands) != 1 || v.RequiredCommands[0] != 3012 {
		t.Fatal(v)
	}
	input.ID = nil
	input.IOProx = &IOProxID{Version: 1, Facility: 2, Number: 3}
	if v := check(); v.State != "unavailable" || len(v.RequiredCommands) != 1 || v.RequiredCommands[0] != 3013 {
		t.Fatal(v)
	}
	device.Capabilities = []uint16{3013}
	if v := check(); v.State != "available" {
		t.Fatal(v)
	}
}
