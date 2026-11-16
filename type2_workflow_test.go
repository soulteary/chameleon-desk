package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var type2TestUID = HexBytes{4, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}

func type2TestScan(uid []byte) []byte {
	return append(append([]byte{byte(len(uid))}, uid...), 0x44, 0, 0, 0)
}
func type2RawStep(data []byte, flags byte, response []byte) readerStep {
	return readerStep{command: 2010, request: append([]byte{flags, 1, 244, byte(len(data) * 8 >> 8), byte(len(data) * 8)}, data...), data: response}
}
func type2TestMemory(layout type2Layout) []byte {
	data := bytes.Repeat([]byte{0x22}, layout.pages*4)
	copy(data, type2TestUID[:3])
	data[3] = 0x88 ^ type2TestUID[0] ^ type2TestUID[1] ^ type2TestUID[2]
	copy(data[4:8], type2TestUID[3:])
	data[8] = type2TestUID[3] ^ type2TestUID[4] ^ type2TestUID[5] ^ type2TestUID[6]
	data[9], data[10], data[11] = 0x48, 0, 0
	copy(data[12:16], []byte{0xe1, 0x10, 0x12, 0})
	for page := layout.passwordPage - 2; page < layout.pages; page++ {
		clear(data[page*4 : (page+1)*4])
	}
	return data
}
func type2ReadFixture(version []byte, input Type2WorkflowRead, memory []byte) ([]readerStep, type2Layout) {
	layout, _ := type2LayoutForVersion(version)
	scan := type2TestScan(input.UID)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, type2RawStep([]byte{0x60}, 0xf4, version), {command: 2000, data: scan}}
	start, count := input.Start, input.Count
	if input.Full {
		start, count = 0, layout.pages
	}
	for first := start; first < start+count; {
		last := min(first+14, start+count-1)
		steps = append(steps, readerStep{command: 2000, data: scan})
		flags := byte(0xfc)
		if len(input.Password) > 0 {
			steps = append(steps, type2RawStep(append([]byte{0x1b}, input.Password...), 0xfc, []byte{0xbe, 0xef}))
			flags = 0x6c
		}
		steps = append(steps, type2RawStep([]byte{0x3a, byte(first), byte(last)}, flags, memory[first*4:(last+1)*4]), readerStep{command: 2000, data: scan})
		first = last + 1
	}
	if start == 0 && count == layout.pages {
		for _, index := range type2CounterIndices(layout, memory) {
			steps = append(steps, readerStep{command: 2000, data: scan})
			flags := byte(0xfc)
			if len(input.Password) > 0 {
				steps = append(steps, type2RawStep(append([]byte{0x1b}, input.Password...), 0xfc, []byte{0xbe, 0xef}))
				flags = 0x6c
			}
			steps = append(steps, type2RawStep([]byte{0x39, index}, flags, []byte{index, 1, 0}), readerStep{command: 2000, data: scan})
		}
	}
	steps = append(steps, type2RawStep([]byte{0x60}, 0xf4, version), readerStep{command: 2000, data: scan}, readerStep{command: 2101, status: 0x68})
	return steps, layout
}
func TestType2WorkflowRecognizesExactModelsMasksHiddenPagesAndRetainsAuthSelection(t *testing.T) {
	versions := [][]byte{{0, 4, 4, 2, 1, 0, 0x0f, 3}, {0, 4, 4, 2, 1, 0, 0x11, 3}, {0, 4, 4, 2, 1, 0, 0x13, 3}, {0, 4, 3, 1, 1, 0, 0x0b, 3}, {0, 4, 3, 2, 1, 0, 0x0e, 3}}
	for _, version := range versions {
		layout, _ := type2LayoutForVersion(version)
		t.Run(layout.name, func(t *testing.T) {
			input := Type2WorkflowRead{UID: type2TestUID, Full: true, Password: HexBytes{0xde, 0xad, 0xbe, 0xef}}
			memory := type2TestMemory(layout)
			copy(memory[layout.passwordPage*4:], []byte{0xde, 0xad, 0xbe, 0xef, 0xbe, 0xef, 0xfa, 0xce})
			steps, _ := type2ReadFixture(version, input, memory)
			c := readerSimulator(t, steps)
			out, err := c.ReadType2Workflow(context.Background(), input)
			if err != nil || !out.Complete || out.Read != layout.pages || out.Type2 == nil || out.Type2.TagType != layout.tag || !out.Type2.Authenticated {
				t.Fatal(out, err)
			}
			if err = ValidateType2ReadResult(out); err != nil {
				t.Fatal(err)
			}
			if len(out.Type2.Unknown) != 2 {
				t.Fatal(out.Type2.Unknown)
			}
			for _, page := range []int{layout.passwordPage, layout.packPage} {
				if !bytes.Equal(out.Data[page*4:(page+1)*4], make([]byte, 4)) {
					t.Fatal("credential response not redacted")
				}
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "DEADBEEF") || strings.Contains(string(raw), "BEEFFACE") {
				t.Fatal("password/PACK copied into result")
			}
			draft, err := NewGUICardFromRead("Type2", out, "")
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := draft.ReadDraft()
			if saved.Rows[layout.passwordPage] != "????????" || saved.Rows[layout.packPage] != "????????" {
				t.Fatal(saved.Rows)
			}
		})
	}
}
func TestType2WorkflowFailedAuthenticationStopsWithoutReplay(t *testing.T) {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	scan := type2TestScan(type2TestUID)
	input := Type2WorkflowRead{UID: type2TestUID, Start: 4, Count: 1, Password: HexBytes{1, 2, 3, 4}}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, type2RawStep([]byte{0x60}, 0xf4, version), {command: 2000, data: scan}, {command: 2000, data: scan}, type2RawStep([]byte{0x1b, 1, 2, 3, 4}, 0xfc, []byte{0}), {command: 2101, status: 0x68}}
	c := readerSimulator(t, steps)
	out, err := c.ReadType2Workflow(context.Background(), input)
	if err == nil || out.Read != 0 || out.Complete || out.Type2.Authenticated {
		t.Fatal(out, err)
	}
}
func TestType2WorkflowDiscardChangedIdentityAndReportCleanupFailures(t *testing.T) {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Start: 4, Count: 1}
	memory := type2TestMemory(layout)
	for _, which := range []string{"identity", "cleanup", "model"} {
		t.Run(which, func(t *testing.T) {
			steps, _ := type2ReadFixture(version, input, memory)
			switch which {
			case "identity":
				changed := type2TestScan(type2TestUID)
				changed[1] ^= 1
				steps[6].data = changed
				steps = append(steps[:7], readerStep{command: 2101, status: 0x68})
			case "cleanup":
				steps[len(steps)-1].status = 0x67
			case "model":
				steps[len(steps)-3].data = append([]byte(nil), version...)
				steps[len(steps)-3].data[6] = 0x11
				steps = append(steps[:len(steps)-2], readerStep{command: 2101, status: 0x68})
			}
			c := readerSimulator(t, steps)
			out, err := c.ReadType2Workflow(context.Background(), input)
			if err == nil || out.Complete {
				t.Fatal(out, err)
			}
			if which == "identity" && out.Read != 0 {
				t.Fatal("uncertain chunk retained")
			}
			if which == "cleanup" && out.Read != 1 {
				t.Fatal("confirmed read discarded")
			}
		})
	}
}
func TestType2WorkflowValidationAndUnknownMetadataFailClosed(t *testing.T) {
	c := readerSimulator(t, nil)
	for _, input := range []Type2WorkflowRead{{UID: HexBytes{1, 2, 3, 4}, Full: true}, {UID: type2TestUID, Full: true, Count: 1}, {UID: type2TestUID, Start: 255, Count: 2}, {UID: type2TestUID, Start: 4, Count: 1, Password: HexBytes{1}}} {
		if _, err := c.ReadType2Workflow(context.Background(), input); err == nil {
			t.Fatal(input)
		}
	}
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Full: true}
	steps, _ := type2ReadFixture(version, input, type2TestMemory(layout))
	c = readerSimulator(t, steps)
	out, err := c.ReadType2Workflow(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ReadResult){func(r *ReadResult) { r.Type2.Unknown = nil }, func(r *ReadResult) { r.Type2.Identity.UID = HexBytes{4, 1, 2, 3, 4, 5, 6} }, func(r *ReadResult) { r.Data[layout.passwordPage*4] = 1 }, func(r *ReadResult) { r.Type2.Version = HexBytes{0, 4, 4, 2, 1, 0, 0xff, 3} }} {
		raw, _ := json.Marshal(out)
		var r ReadResult
		json.Unmarshal(raw, &r)
		mutate(&r)
		if ValidateType2ReadResult(r) == nil {
			t.Fatal("malformed masks/model accepted")
		}
	}
	if _, err = type2LayoutForVersion([]byte{0, 4, 4, 2, 1, 0, 0x0b, 3}); err == nil {
		t.Fatal("unknown NTAG layout guessed")
	}
}
func TestType2WorkflowCancellationRetainsOnlyConfirmedPrefix(t *testing.T) {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Full: true}
	steps, _ := type2ReadFixture(version, input, type2TestMemory(layout))
	ctx, cancel := context.WithCancel(context.Background())
	steps[7].before = cancel
	steps = steps[:8]
	c := readerSimulator(t, steps)
	out, err := c.ReadType2Workflow(ctx, input)
	if !errors.Is(err, context.Canceled) || out.Read != 15 || out.Complete {
		t.Fatal(out.Read, out.Complete, err)
	}
}

func type2WriteFixture(input Type2PageWrite, planOnly bool) []readerStep {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	scan := type2TestScan(input.UID)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, type2RawStep([]byte{0x60}, 0xf4, version), {command: 2000, data: scan}}
	bounded := func(first, last int, data []byte) {
		steps = append(steps, readerStep{command: 2000, data: scan})
		flags := byte(0xfc)
		if len(input.Password) > 0 {
			steps = append(steps, type2RawStep(append([]byte{0x1b}, input.Password...), 0xfc, []byte{9, 8}))
			flags = 0x6c
		}
		steps = append(steps, type2RawStep([]byte{0x3a, byte(first), byte(last)}, flags, data), readerStep{command: 2000, data: scan})
	}
	bounded(41, 42, make([]byte, 8))
	bounded(input.Page, input.Page, input.Expected)
	steps = append(steps, type2RawStep([]byte{0x60}, 0xf4, version), readerStep{command: 2000, data: scan})
	if !planOnly && !bytes.Equal(input.Data, input.Expected) {
		flags := byte(0xf8)
		if len(input.Password) > 0 {
			steps = append(steps, type2RawStep(append([]byte{0x1b}, input.Password...), 0xfc, []byte{9, 8}))
			flags = 0x68
		}
		steps = append(steps, type2RawStep(append([]byte{0xa2, byte(input.Page)}, input.Data...), flags, []byte{0x0a}))
		bounded(input.Page, input.Page, input.Data)
		steps = append(steps, type2RawStep([]byte{0x60}, 0xf4, version), readerStep{command: 2000, data: scan})
	}
	return append(steps, readerStep{command: 2101, status: 0x68})
}
func type2WriteInput() Type2PageWrite {
	return Type2PageWrite{UID: type2TestUID, Page: 4, Expected: HexBytes{1, 2, 3, 4}, Data: HexBytes{4, 3, 2, 1}}
}
func TestType2UserPageWriteChecksPlanAndIssuesOneWriteWithReadback(t *testing.T) {
	for _, password := range []HexBytes{nil, {0xab, 0xcd, 0xef, 0x12}} {
		input := type2WriteInput()
		input.Password = password
		c := readerSimulator(t, type2WriteFixture(input, true))
		plan, err := c.PlanType2UserPage(context.Background(), input)
		if err != nil || plan.Model != "NTAG213" || !bytes.Equal(plan.Before, input.Expected) || !bytes.Equal(plan.After, input.Data) {
			t.Fatal(plan, err)
		}
		calls := 0
		c = readerSimulator(t, type2WriteFixture(input, false))
		out, err := c.WriteType2UserPageObserved(context.Background(), input, func() { calls++ })
		if err != nil || calls != 1 || !out.WriteAttempted || !out.Acknowledged || !out.ReadbackMatches || !out.Verified || !bytes.Equal(out.After, input.Data) {
			t.Fatal(out, calls, err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "ABCDEF12") {
			t.Fatal("password leaked")
		}
	}
}
func TestType2UserPageWriteRefusesProtectedPagesChangedPreconditionsAndMirrors(t *testing.T) {
	for _, page := range []int{0, 1, 2, 3, 40, 41, 42, 43, 44} {
		input := type2WriteInput()
		input.Page = page
		steps := []readerStep(nil)
		if page >= 4 {
			steps = append(type2WriteFixture(input, true)[:4], readerStep{command: 2101, status: 0x68})
		}
		c := readerSimulator(t, steps)
		out, err := c.WriteType2UserPage(context.Background(), input)
		if err == nil || out.WriteAttempted {
			t.Fatal(page, out, err)
		}
	}
	for _, which := range []string{"changed", "mirror", "model", "identity"} {
		t.Run(which, func(t *testing.T) {
			input := type2WriteInput()
			steps := type2WriteFixture(input, true)
			switch which {
			case "changed":
				steps[8].data = []byte{9, 9, 9, 9}
				steps = append(steps[:10], readerStep{command: 2101, status: 0x68})
			case "mirror":
				steps[5].data = []byte{0x40, 0, 4, 0, 0, 0, 0, 0}
				steps = append(steps[:7], readerStep{command: 2101, status: 0x68})
			case "model":
				steps[10].data = []byte{0, 4, 4, 2, 1, 0, 0x11, 3}
				steps = append(steps[:11], readerStep{command: 2101, status: 0x68})
			case "identity":
				steps[9].data = type2TestScan([]byte{4, 1, 2, 3, 4, 5, 6})
				steps = append(steps[:10], readerStep{command: 2101, status: 0x68})
			}
			out, err := readerSimulator(t, steps).WriteType2UserPage(context.Background(), input)
			if err == nil || out.WriteAttempted {
				t.Fatal(out, err)
			}
		})
	}
}
func TestType2UserPageWriteReportsAmbiguousOrUnverifiedWritesWithoutReplay(t *testing.T) {
	for _, which := range []string{"nak", "readback", "cleanup"} {
		t.Run(which, func(t *testing.T) {
			input := type2WriteInput()
			steps := type2WriteFixture(input, false)
			switch which {
			case "nak":
				steps[12].data = []byte{0}
				steps = append(steps[:13], readerStep{command: 2101, status: 0x68})
			case "readback":
				steps[14].data = []byte{9, 9, 9, 9}
				steps = append(steps[:16], readerStep{command: 2101, status: 0x68})
			case "cleanup":
				steps[len(steps)-1].status = 0x67
			}
			calls := 0
			out, err := readerSimulator(t, steps).WriteType2UserPageObserved(context.Background(), input, func() { calls++ })
			if err == nil || calls != 1 || !out.WriteAttempted || out.Verified {
				t.Fatal(out, calls, err)
			}
			if which == "cleanup" && (!out.Acknowledged || !out.ReadbackMatches) {
				t.Fatal(out)
			}
		})
	}
	input := type2WriteInput()
	input.Data = append(HexBytes(nil), input.Expected...)
	calls := 0
	out, err := readerSimulator(t, type2WriteFixture(input, false)).WriteType2UserPageObserved(context.Background(), input, func() { calls++ })
	if err != nil || calls != 0 || out.WriteAttempted || !out.Unchanged || !out.Verified {
		t.Fatal(out, calls, err)
	}
}

func TestType2UserPageWriteCancellationAtBoundaryDoesNotReplay(t *testing.T) {
	input := type2WriteInput()
	steps := type2WriteFixture(input, false)[:12]
	ctx, cancel := context.WithCancel(context.Background())
	out, err := readerSimulator(t, steps).WriteType2UserPageObserved(ctx, input, cancel)
	if !errors.Is(err, context.Canceled) || !out.WriteAttempted || out.Acknowledged || out.Verified {
		t.Fatal(out, err)
	}
}

func TestType2WorkflowEnabledCounterAndPartialMasks(t *testing.T) {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Full: true}
	memory := type2TestMemory(layout)
	memory[(layout.passwordPage-1)*4] = 0x10
	steps, _ := type2ReadFixture(version, input, memory)
	out, err := readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err != nil || len(out.Type2.Counters) != 1 || out.Type2.Counters[0] != 258 {
		t.Fatal(out, err)
	}
	if err = ValidateType2ReadResult(out); err != nil {
		t.Fatal(err)
	}
	out.Type2.Counters = nil
	if ValidateType2ReadResult(out) == nil {
		t.Fatal("enabled counter observation omitted")
	}
	input = Type2WorkflowRead{UID: type2TestUID, Start: 4, Count: 2}
	steps, _ = type2ReadFixture(version, input, memory)
	out, err = readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err != nil || len(out.Type2.Unknown) != layout.pages-2 {
		t.Fatal(out, err)
	}
	if err = ValidateType2ReadResult(out); err != nil {
		t.Fatal(err)
	}
	for _, u := range out.Type2.Unknown {
		if u.Page == 4 || u.Page == 5 {
			t.Fatal("confirmed public page marked unknown")
		}
		if u.Page != 43 && u.Page != 44 && u.Reason != "not_requested" {
			t.Fatal(u)
		}
	}
}

func TestType2WorkflowSplitsAtReceiveFIFOLimitWithoutFallback(t *testing.T) {
	// RF capacity includes two CRC bytes. A 16-page request cannot be one frame.
	if _, err := type2FastRead(context.Background(), nil, nil, 0, 15); err == nil {
		t.Fatal("66-byte RF response accepted")
	}
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Start: 0, Count: 16}
	memory := type2TestMemory(layout)
	steps, _ := type2ReadFixture(version, input, memory)
	frames := 0
	for _, step := range steps {
		if step.command == 2010 && len(step.request) == 8 && step.request[5] == 0x3a {
			frames++
			if size := 4*(int(step.request[7])-int(step.request[6])+1) + 2; size > 64 {
				t.Fatal("RF frame exceeds FIFO", size)
			}
			if frames == 1 && !bytes.Equal(step.request[5:], []byte{0x3a, 0, 14}) {
				t.Fatal(step.request)
			}
			if frames == 2 && !bytes.Equal(step.request[5:], []byte{0x3a, 15, 15}) {
				t.Fatal(step.request)
			}
		}
	}
	if frames != 2 {
		t.Fatal(frames)
	}
	out, err := readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err != nil || !out.Complete || out.Read != 16 || !bytes.Equal(out.Data, memory[:64]) {
		t.Fatal(out, err)
	}
	// A firmware error stops at the first chunk. There is no smaller-frame retry.
	steps, _ = type2ReadFixture(version, input, memory)
	steps[5].status = 2
	steps[5].data = nil
	steps = append(steps[:6], readerStep{command: 2101, status: 0x68})
	out, err = readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err == nil || out.Read != 0 || out.Complete {
		t.Fatal(out, err)
	}
}
