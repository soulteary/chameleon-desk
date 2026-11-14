package chameleon

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

func TestLoadGUICardOnlyRequiresSaveWhenRequested(t *testing.T) {
	for _, persist := range []bool{false, true} {
		t.Run(map[bool]string{false: "RAM", true: "flash"}[persist], func(t *testing.T) {
			card := guiTestCard(t, 100, "0102030405", nil, nil)
			plan, err := PlanGUICardLoad(card, 2)
			if err != nil {
				t.Fatal(err)
			}
			all := guiCaps(plan, CmdEm410xGetEmuId)
			var caps []byte
			for i := 0; i < len(all); i += 2 {
				if Command(binary.BigEndian.Uint16(all[i:])) != CmdSlotDataConfigSave {
					caps = append(caps, all[i:i+2]...)
				}
			}
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, request: []byte{0}, status: 0x68}, {command: 1035, status: 0x68, data: caps}}
			if !persist {
				steps = append(steps, readerStep{command: 1018, status: 0x68, data: []byte{0}})
				for _, step := range plan.Steps {
					steps = append(steps, readerStep{command: uint16(step.Command), request: step.Payload, status: 0x68})
				}
				steps = append(steps, readerStep{command: uint16(CmdEm410xGetEmuId), status: 0x68, data: []byte{0, 100, 1, 2, 3, 4, 5}}, readerStep{command: 1008, request: []byte{1, 1}, status: 0x68, data: []byte(plan.Nickname)}, readerStep{command: 1003, request: []byte{0}, status: 0x68})
			}
			steps = append(steps, readerStep{command: 1001, request: []byte{1}, status: 0x68})
			client := readerSimulator(t, steps)
			out, err := client.LoadGUICard(context.Background(), 2, card, GUICardLoadOptions{Persist: persist})
			if persist {
				if err == nil || !strings.Contains(err.Error(), "SLOT_DATA_CONFIG_SAVE") || out.Verified || out.Persisted {
					t.Fatalf("unsupported persistence did not fail before writes: %+v %v", out, err)
				}
			} else if err != nil || !out.Verified || out.Persisted || out.Activated {
				t.Fatalf("RAM-only load incorrectly requires flash save: %+v %v", out, err)
			}
		})
	}
}

func TestVanityUIDRequiresHexadecimalBytes(t *testing.T) {
	for _, input := range []string{"GGGGGGGG", "0411223X", "你好你好你好你好", "0411223344556Z"} {
		if VanitySAKFromUIDHex(input) != nil {
			t.Fatalf("invalid hexadecimal UID accepted: %q", input)
		}
	}
	for _, input := range []string{" 04\t11\n22\r33 ", "04 11 22 33 44 55 66"} {
		if VanitySAKFromUIDHex(input) == nil {
			t.Fatalf("whitespace-separated UID rejected: %q", input)
		}
	}
}
