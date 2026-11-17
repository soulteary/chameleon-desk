package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func rfInput(format string, data []byte) RFFileRequest {
	r := RFFileRequest{Format: format, Name: "source", Data: data}
	if strings.HasPrefix(format, "hf-") {
		r.Layout = "data"
	}
	return r
}
func TestOfflineLFRateSelectionErrorsAndExports(t *testing.T) {
	data := []byte{}
	for _, v := range []byte{0, 1, 1, 0, 0, 1, 1, 0} {
		data = append(data, bytes.Repeat([]byte{v * 200}, 4)...)
	}
	r := rfInput("lf-u8", data)
	r.Decode = true
	r.Clock = 8
	if _, err := AnalyzeRFFile(context.Background(), r); err == nil {
		t.Fatal("unknown rate decoded")
	}
	r.RateHz = 125000
	r.Export = "csv"
	out, err := AnalyzeRFFile(context.Background(), r)
	if err != nil || out.LF.Manchester.Bits != "0101" || out.LF.RateSource != "operator_assigned" || !strings.Contains(out.Export.Text, "1,8.000000,0") {
		t.Fatal(out, err)
	}
	encoded, _ := json.Marshal(out)
	var saved RFFileAnalysis
	if err := json.Unmarshal(encoded, &saved); err != nil || saved.Parameters.Clock != 8 || !saved.Parameters.Decode || saved.Parameters.RateHz != 125000 || saved.Parameters.Export != "csv" {
		t.Fatal("report lost analysis options", saved.Parameters, err)
	}
	original := append([]byte(nil), data...)
	r.Start = 4
	r.Count = 8
	r.Decode = false
	r.Export = "svg"
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.LF.Start != 4 || len(out.LF.Data) != 8 || out.Source.SHA256 != imageDigest(original) || out.Export.MediaType != "image/svg+xml" || !bytes.Equal(data, original) {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(LFSamples{RateHz: 1000, Data: data, RequestedMS: 12})
	r = rfInput("lf-json", raw)
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.LF.RFHeuristicAvailable || out.LF.Analysis.EstimatedRFDivider != 0 || out.LF.RateSource != "file_declared" || out.LF.DeclaredRequestedMS != 12 {
		t.Fatal(out, err)
	}
	r.RateHz = 125000
	if _, err = AnalyzeRFFile(context.Background(), r); err == nil {
		t.Fatal("rate silently overridden")
	}
	for _, bad := range []string{`{}`, `{"rate_hz":125000}`, `{"data":null}`, `{"rate_hz":125000,"data":"00","data":"FF"}`, `{"data":"00","DATA":"FF"}`, `{"data":"000"}`, `{"data":"00","path":"file"}`, `{"data":"00"} trailing`} {
		if _, err = AnalyzeRFFile(context.Background(), rfInput("lf-json", []byte(bad))); err == nil {
			t.Fatal("malformed source accepted", bad)
		}
	}
}
func rfPacked(raw []byte, bits uint16, card bool) []byte {
	if card {
		bits |= 0x8000
	}
	return append([]byte{byte(bits >> 8), byte(bits)}, raw...)
}
func rfParityData(data []byte, wrong bool) []byte {
	out := make([]byte, (len(data)*9+7)/8)
	for i, b := range data {
		p := byte(1)
		for j := 0; j < 8; j++ {
			bit := (b >> uint(j)) & 1
			p ^= bit
			index := i*9 + j
			out[index/8] |= bit << uint(index%8)
		}
		if wrong && i == 0 {
			p ^= 1
		}
		index := i*9 + 8
		out[index/8] |= p << uint(index%8)
	}
	return out
}
func TestOfflineHFLayoutsPartialPrefixAndCRCRemainCandidates(t *testing.T) {
	raw := append(rfPacked([]byte{0x26}, 7, false), rfPacked(rfParityData([]byte{0, 255}, true), 18, true)...)
	raw = append(raw, 1)
	r := rfInput("hf-packed", raw)
	r.Layout = "parity9"
	out, err := AnalyzeRFFile(context.Background(), r)
	if err != nil || out.Complete || out.HF.Parsed != 2 || out.Issues[0].Code != "truncated_header" || out.HF.Frames[1].Data.String() != "00FF" || out.HF.Frames[1].ParityErrors != 1 || out.HF.Timing != "not_captured" {
		t.Fatal(out, err)
	}
	r.Layout = "data"
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.HF.Frames[1].Bits != 18 || out.HF.Frames[1].ParityCheck != "not_captured" {
		t.Fatal(out, err)
	}
	r = rfInput("hf-packed", rfPacked([]byte{0x50, 0, 0x57, 0xcd}, 32, false))
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.HF.Frames[0].CRCACandidate != "matches_suffix" {
		t.Fatal(out, err)
	}
	r.Data[len(r.Data)-1] ^= 1
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.HF.Frames[0].CRCACandidate != "does_not_match_suffix" {
		t.Fatal(out, err)
	}
	for _, source := range []string{`[{"bits":8,"data":"60"}]`, `[{"from_card":false,"bits":9,"data":"60"}]`, `[{"from_card":false,"bits":8,"data":"60","DATA":"FF"}]`} {
		out, err = AnalyzeRFFile(context.Background(), rfInput("hf-json", []byte(source)))
		if err != nil || out.Complete || out.HF.Parsed != 0 {
			t.Fatal("invalid frame granted authority", out, err)
		}
	}
}
func TestOfflinePM3TimeAndGUIShortMarkersAreExplicit(t *testing.T) {
	record := make([]byte, 10)
	binary.LittleEndian.PutUint32(record, 1234)
	binary.LittleEndian.PutUint16(record[4:], 64)
	binary.LittleEndian.PutUint16(record[6:], 0x8001)
	record[8] = 0x26
	record[9] = 7
	r := rfInput("pm3-trace", record)
	out, err := AnalyzeRFFile(context.Background(), r)
	if err != nil || out.HF.Frames[0].Bits != 8 || out.HF.Frames[0].RawBits != 0 || *out.HF.Frames[0].Timestamp != 1234 || !out.HF.Frames[0].FromCard || out.HF.Timing != "file_declared_ticks_unverified" {
		t.Fatal(out, err)
	}
	r.ShortMarkers = true
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.HF.Frames[0].Bits != 7 || out.HF.Frames[0].ParityCheck != "not_captured" {
		t.Fatal(out, err)
	}
	r.Data = append(record, 0)
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.Complete || out.HF.Parsed != 1 {
		t.Fatal(out, err)
	}
}
func TestOfflineAnalysisBoundsCancellationAndLargeEnvelope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeRFFile(ctx, rfInput("lf-u8", []byte{1})); err != context.Canceled {
		t.Fatal(err)
	}
	for _, r := range []RFFileRequest{rfInput("lf-u8", make([]byte, MaxRFSamples+1)), rfInput("unknown", []byte{1}), rfInput("hf-packed", make([]byte, MaxRFFileBytes+1))} {
		if _, err := AnalyzeRFFile(context.Background(), r); err == nil {
			t.Fatal("unbounded input accepted")
		}
	}
	r := rfInput("lf-u8", make([]byte, MaxRFSamples))
	out, err := AnalyzeRFFile(context.Background(), r)
	if err != nil || len(out.LF.Buckets) != 1024 {
		t.Fatal(err)
	}
	r.Export = "csv"
	r.RateHz = 125000
	if _, err := AnalyzeRFFile(context.Background(), r); err == nil {
		t.Fatal("oversized CSV accepted")
	}
	r = rfInput("hf-packed", bytes.Repeat([]byte{0, 8, 0x26}, MaxRFFrames+1))
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || out.Complete || out.HF.Parsed != MaxRFFrames || out.Issues[0].Code != "frame_limit" {
		t.Fatal(out, err)
	}
}

func TestOfflineAuthenticationCandidatesNeverCrossSelectionBoundaries(t *testing.T) {
	command := []byte{0x60, 4}
	crc := uint16(0x6363)
	for _, b := range command {
		crc ^= uint16(b)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	command = append(command, byte(crc), byte(crc>>8))
	source := append(rfPacked(command, 32, false), rfPacked([]byte{1, 2, 3, 4}, 32, true)...)
	source = append(source, rfPacked(make([]byte, 8), 64, false)...)
	source = append(source, rfPacked(make([]byte, 4), 32, true)...)
	r := rfInput("hf-packed", source)
	out, err := AnalyzeRFFile(context.Background(), r)
	if err != nil || len(out.HF.AuthCandidates) != 1 {
		t.Fatal(out, err)
	}
	c := out.HF.AuthCandidates[0]
	if c.Request != 0 || c.Nonce != 1 || *c.ReaderAnswer != 2 || *c.CardAnswer != 3 || c.Block != 4 {
		t.Fatal(c)
	}
	r.Start = 1
	r.Count = 3
	out, err = AnalyzeRFFile(context.Background(), r)
	if err != nil || len(out.HF.AuthCandidates) != 0 {
		t.Fatal("stitched evidence outside selection", out, err)
	}
}

func FuzzOfflineRFParsers(f *testing.F) {
	f.Add(uint8(0), []byte{0, 7, 0x26})
	f.Add(uint8(1), []byte{0, 8, 0x60})
	f.Add(uint8(2), make([]byte, 8))
	f.Add(uint8(3), []byte(`[{"from_card":false,"bits":7,"data":"26"}]`))
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		if len(data) > 8192 {
			return
		}
		formats := []string{"hf-packed", "hf-packed", "pm3-trace", "hf-json"}
		r := rfInput(formats[int(kind)%4], data)
		if kind%4 == 1 {
			r.Layout = "parity9"
		}
		out, err := AnalyzeRFFile(context.Background(), r)
		if err == nil && (len(out.HF.Frames) > MaxRFFrames || out.Source.SHA256 != imageDigest(data)) {
			t.Fatal("invalid bounded parse", out)
		}
	})
}
