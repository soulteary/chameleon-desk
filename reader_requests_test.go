package chameleon

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func classicReadInput() ClassicRead {
	r := ClassicRead{UID: HexBytes{1, 2, 3, 4}}
	for i := 0; i < 5; i++ {
		r.Sectors = append(r.Sectors, SectorKeys{Sector: i, A: HexBytes{1, 2, 3, 4, 5, 6}})
	}
	return r
}

func TestReaderRequestRequirementsAndPolicy(t *testing.T) {
	classic := classicReadInput()
	for _, tc := range []struct {
		id       string
		request  FeatureRequest
		commands []Command
	}{
		{"reader.classic-keyed", FeatureRequest{ClassicRead: &classic}, []Command{1001, 1002, 2000, 2008}},
		{"reader.type2-range", FeatureRequest{Type2Read: &Type2Read{UID: HexBytes{1, 2, 3, 4}, Start: 255, Count: 1}}, []Command{1001, 1002, 2000, 2010}},
		{"reader.lf", FeatureRequest{LFRead: &LFRead{Protocol: "pac"}}, []Command{1001, 1002, 3014}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			ctx := featureTestContext()
			ctx.WritesAllowed = false
			ctx.Device.Capabilities = []uint16{}
			for _, cmd := range tc.commands {
				ctx.Device.Capabilities = append(ctx.Device.Capabilities, uint16(cmd))
			}
			state, err := EvaluateFeature(tc.id, "web", ctx, &tc.request)
			if err != nil || state.State != "available" || !reflect.DeepEqual(state.RequiredCommands, tc.commands) {
				t.Fatal(state, err)
			}
			ctx.Device.ModelID = featureModelID(1)
			state, _ = EvaluateFeature(tc.id, "web", ctx, &tc.request)
			if !hasFeatureReason(state, "unsupported_model") {
				t.Fatal(state)
			}
			ctx.Device.ModelID = featureModelID(0)
			ctx.Device.Capabilities = ctx.Device.Capabilities[:2]
			state, _ = EvaluateFeature(tc.id, "web", ctx, &tc.request)
			if !hasFeatureReason(state, "missing_commands") {
				t.Fatal(state)
			}
		})
	}
	classic.Sectors[0].B = HexBytes{6, 5, 4, 3, 2, 1}
	commands, err := classic.Requirements()
	if err != nil || !reflect.DeepEqual(commands, []Command{1001, 1002, 2000, 2008, 2007}) {
		t.Fatal(commands, err)
	}
}

func TestReaderInputsRejectBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	for _, r := range []Type2Read{{UID: HexBytes{1}, Count: 1}, {UID: HexBytes{1, 2, 3, 4}, Start: 255, Count: 2}, {UID: HexBytes{1, 2, 3, 4}, Start: -1, Count: 1}} {
		if _, err := c.ReadType2Range(context.Background(), r); err == nil {
			t.Fatal(r)
		}
	}
	pwd := uint32(0)
	for _, r := range []LFRead{{Protocol: "idteck"}, {Protocol: "pac", Format: 1}, {Protocol: "pac", Password: &pwd}, {Protocol: "hidprox", Format: 255}} {
		if _, err := c.ReadLF(context.Background(), r); err == nil {
			t.Fatal(r)
		}
	}
	classic := classicReadInput()
	classic.Sectors[1].A = nil
	if _, err := classic.KeyMap(); err == nil {
		t.Fatal("sector without a key accepted")
	}
}

func TestType2RangeConfirmedPartialAndUIDChange(t *testing.T) {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{true: "changed", false: "success"}[changed], func(t *testing.T) {
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}}
			for _, base := range []byte{0, 4} {
				steps = append(steps, readerStep{command: 2000, data: scan}, readerStep{command: 2010, request: []byte{0xf4, 1, 244, 0, 16, 0x30, base}, data: bytes.Repeat([]byte{base + 1}, 16)})
				uid := append([]byte(nil), scan...)
				if changed && base == 4 {
					uid[1] = 9
				}
				steps = append(steps, readerStep{command: 2000, data: uid})
			}
			steps = append(steps, readerStep{command: 1001, request: []byte{0}, status: 0x68})
			c := readerSimulator(t, steps)
			out, err := c.ReadType2Range(context.Background(), Type2Read{UID: HexBytes{1, 2, 3, 4}, Start: 3, Count: 3})
			if changed {
				if err == nil || len(out) != 4 {
					t.Fatal(out, err)
				}
			} else if err != nil || !bytes.Equal(out, append(bytes.Repeat([]byte{1}, 4), bytes.Repeat([]byte{5}, 8)...)) {
				t.Fatal(out, err)
			}
		})
	}
}

func TestType2RangeCancellationDoesNotReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}, before: cancel}})
	_, err := c.ReadType2Range(ctx, Type2Read{UID: HexBytes{1, 2, 3, 4}, Count: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.Stats().Requests > 2 {
		t.Fatal("read replayed after cancellation")
	}
}
