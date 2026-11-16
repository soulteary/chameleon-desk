package chameleon

import (
	"context"
	"encoding/binary"
	"testing"
)

func currentEmulatorCaps(r CurrentEmulatorRequest) readerStep {
	cmds, _ := r.Requirements()
	raw := make([]byte, len(cmds)*2)
	for i, cmd := range cmds {
		binary.BigEndian.PutUint16(raw[i*2:], uint16(cmd))
	}
	return readerStep{command: 1035, status: 0x68, data: raw}
}
func currentSourceSteps() []readerStep {
	return []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1001, 0, 1)}}
}

func TestCurrentEmulatorCompareReadbackAndNoImplicitModeOrSlotChanges(t *testing.T) {
	r := CurrentEmulatorRequest{Slot: 1, TagType: 1001, Action: "configuration"}
	steps := []readerStep{currentEmulatorCaps(r)}
	steps = append(steps, currentSourceSteps()...)
	steps = append(steps, classicConfigSteps(0)...)
	steps = append(steps, currentSourceSteps()...)
	cfg, err := readerSimulator(t, steps).ReadCurrentEmulator(context.Background(), r)
	if err != nil || cfg.Slot != 1 || cfg.Classic.PRNG != 1 {
		t.Fatal(cfg, err)
	}
	yes := true
	r.Action = "configure"
	r.Expected = &cfg
	r.Patch = EmulatorPatch{Family: "classic", Gen2: &yes}
	for _, scenario := range []string{"success", "stale", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			request := r
			copy := cfg
			copy.Classic = &ClassicEmulatorSettings{EmulatorConfig: cfg.Classic.EmulatorConfig, PRNG: 1}
			if scenario == "stale" {
				copy.Classic.Gen2 = true
			}
			request.Expected = &copy
			steps := []readerStep{currentEmulatorCaps(request)}
			steps = append(steps, currentSourceSteps()...)
			steps = append(steps, classicConfigSteps(0)...)
			if scenario != "stale" {
				status := uint16(0x68)
				if scenario == "rejected" {
					status = 0x67
				}
				steps = append(steps, readerStep{command: 4013, status: status, request: []byte{1}})
				if scenario == "success" {
					steps = append(steps, readerStep{command: 4012, status: 0x68, data: []byte{1}})
					steps = append(steps, classicConfigSteps(1)...)
					steps = append(steps, currentSourceSteps()...)
					steps = append(steps, currentSourceSteps()...)
				}
			}
			calls := 0
			out, err := readerSimulator(t, steps).ConfigureCurrentEmulator(context.Background(), request, func() { calls++ })
			if scenario == "success" {
				if err != nil || !out.Verified || !out.After.Classic.Gen2 || out.Persisted || calls != 1 {
					t.Fatal(out, err)
				}
			} else if err == nil || out.Verified {
				t.Fatal(out, err)
			}
			if scenario == "stale" && (calls != 0 || out.WriteAttempted) {
				t.Fatal("stale config was written")
			}
			if scenario == "rejected" && (calls != 1 || out.Outcome != "configuration_may_be_partial") {
				t.Fatal(out, err)
			}
		})
	}
}

func TestReaderConfigAndManualModeCompareBeforeWrite(t *testing.T) {
	r := ReaderConfigUpdate{Expected: ReaderConfig{}, Patch: ReaderConfig{BCC: 1, CL2: -1, CL3: 2, RATS: -1}}
	steps := []readerStep{{command: uint16(CmdHf14aGetConfig), status: 0x68, data: []byte{0, 0, 0, 0}}, {command: uint16(CmdHf14aSetConfig), status: 0x68, request: []byte{1, 255, 2, 255}}, {command: uint16(CmdHf14aGetConfig), status: 0x68, data: []byte{1, 0, 2, 0}}}
	calls := 0
	out, err := readerSimulator(t, steps).ConfigureReaderObserved(context.Background(), r, func() { calls++ })
	if err != nil || calls != 1 || out.BCC != 1 || out.CL2 != 0 || out.CL3 != 2 {
		t.Fatal(out, err)
	}
	calls = 0
	steps = []readerStep{{command: uint16(CmdHf14aGetConfig), status: 0x68, data: []byte{2, 0, 0, 0}}}
	if _, err = readerSimulator(t, steps).ConfigureReaderObserved(context.Background(), r, func() { calls++ }); err == nil || calls != 0 {
		t.Fatal(err)
	}
	mode, err := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, status: 0x68, request: []byte{0}}, {command: 1002, status: 0x68, data: []byte{0}}}).ChangeDeviceModeObserved(context.Background(), DeviceModeChange{Expected: 1, Mode: 0}, nil)
	if err != nil || mode != 0 {
		t.Fatal(mode, err)
	}
}

func TestNewType2UserPageLimitsExcludeDynamicLocksAndCredentials(t *testing.T) {
	for _, test := range []struct {
		version HexBytes
		page    int
	}{{HexBytes{0, 4, 4, 1, 1, 0, 0x0b, 3}, 16}, {HexBytes{0, 4, 4, 1, 1, 0, 0x0e, 3}, 36}} {
		input := type2WriteInput()
		input.Page = test.page
		steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: type2TestScan(input.UID)}, type2RawStep([]byte{0x60}, 0xf4, test.version), {command: 2000, data: type2TestScan(input.UID)}, {command: 2101, status: 0x68}}
		out, err := readerSimulator(t, steps).WriteType2UserPage(context.Background(), input)
		if err == nil || out.WriteAttempted {
			t.Fatal(out, err)
		}
	}
}
