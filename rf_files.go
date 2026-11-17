package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"unicode/utf8"
)

const MaxRFFileBytes = 1 << 20
const MaxRFSamples = 1 << 18
const MaxRFFrames = 4096

type RFFileRequest struct {
	Format       string   `json:"format"`
	Name         string   `json:"name"`
	Data         HexBytes `json:"data"`
	Layout       string   `json:"layout,omitempty"`
	ShortMarkers bool     `json:"short_markers,omitempty"`
	RateHz       int      `json:"rate_hz,omitempty"`
	Start        int      `json:"start"`
	Count        int      `json:"count"`
	Decode       bool     `json:"decode"`
	Clock        int      `json:"clock"`
	Phase        int      `json:"phase"`
	Invert       bool     `json:"invert"`
	Export       string   `json:"export,omitempty"`
}

func (r RFFileRequest) Validate() error {
	if len(r.Data) == 0 || len(r.Data) > MaxRFFileBytes || len(r.Name) > 256 || !utf8.ValidString(r.Name) {
		return fmt.Errorf("source must contain 1..1048576 bytes; filename at most 256 UTF-8 bytes")
	}
	if r.Start < 0 || r.Count < 0 || r.RateHz < 0 || r.RateHz > 10000000 {
		return fmt.Errorf("invalid selection or sample rate")
	}
	switch r.Format {
	case "lf-json", "lf-u8":
		if r.Layout != "" || r.ShortMarkers {
			return fmt.Errorf("LF input does not accept HF layout options")
		}
	case "hf-json", "hf-packed":
		if r.Layout != "data" && r.Layout != "parity9" {
			return fmt.Errorf("choose data or parity9 bit layout explicitly")
		}
		if r.ShortMarkers {
			return fmt.Errorf("short-frame extension applies only to PM3/GUI trace")
		}
	case "pm3-trace":
		if r.Layout != "" {
			return fmt.Errorf("PM3 trace already has separate parity records")
		}
	default:
		return fmt.Errorf("choose lf-json, lf-u8, hf-json, hf-packed or pm3-trace; formats are not guessed")
	}
	if r.Format != "lf-json" && r.Format != "lf-u8" && (r.Decode || r.RateHz != 0 || r.Invert) {
		return fmt.Errorf("HF trace does not accept LF decoding or rate options")
	}
	if r.Decode && (r.Clock < 2 || r.Clock > 1024 || r.Clock%2 != 0 || r.Phase < 0 || r.Phase > 1) {
		return fmt.Errorf("Manchester clock must be even 2..1024; phase 0/1")
	}
	if r.Export != "" && r.Export != "csv" && r.Export != "svg" {
		return fmt.Errorf("export must be csv or svg")
	}
	if r.Export != "" && r.Format != "lf-json" && r.Format != "lf-u8" {
		return fmt.Errorf("CSV/SVG exports apply only to LF samples")
	}
	return nil
}

type RFSource struct {
	Name         string `json:"name"`
	Format       string `json:"format"`
	Bytes        int    `json:"bytes"`
	SHA256       string `json:"sha256"`
	Layout       string `json:"layout,omitempty"`
	ShortMarkers bool   `json:"short_markers,omitempty"`
}
type RFParseIssue struct {
	Code    string `json:"code"`
	Offset  int    `json:"offset"`
	Unit    string `json:"unit"`
	Message string `json:"message"`
}
type RFSampleBucket struct {
	Start int  `json:"start"`
	End   int  `json:"end"`
	Min   byte `json:"min"`
	Max   byte `json:"max"`
}
type RFSampleView struct {
	Total                int                `json:"total"`
	Start                int                `json:"start"`
	RateHz               int                `json:"rate_hz"`
	RateSource           string             `json:"rate_source"`
	DeclaredRequestedMS  int                `json:"declared_requested_ms,omitempty"`
	Data                 HexBytes           `json:"data"`
	Analysis             ModulationAnalysis `json:"analysis"`
	RFHeuristicAvailable bool               `json:"rf_heuristic_available"`
	Manchester           *ManchesterResult  `json:"manchester,omitempty"`
	Buckets              []RFSampleBucket   `json:"buckets"`
}
type RFTraceFrame struct {
	Index           int      `json:"index"`
	Offset          *int     `json:"offset,omitempty"`
	FromCard        bool     `json:"from_card"`
	RawBits         int      `json:"raw_bits"`
	BitLengthSource string   `json:"bit_length_source"`
	Bits            int      `json:"bits"`
	RawData         HexBytes `json:"raw_data,omitempty"`
	Data            HexBytes `json:"data"`
	Parity          HexBytes `json:"parity,omitempty"`
	ParityCheck     string   `json:"parity_check"`
	ParityErrors    int      `json:"parity_errors"`
	CRCACandidate   string   `json:"crc_a_candidate"`
	Label           string   `json:"label"`
	Timestamp       *uint32  `json:"file_timestamp,omitempty"`
	Duration        *uint16  `json:"file_duration,omitempty"`
}
type RFAuthCandidate struct {
	Request      int    `json:"request_frame"`
	Nonce        int    `json:"nonce_frame"`
	ReaderAnswer *int   `json:"reader_answer_frame,omitempty"`
	CardAnswer   *int   `json:"card_answer_frame,omitempty"`
	Block        byte   `json:"block"`
	KeyType      string `json:"key_type"`
}
type RFTraceView struct {
	Parsed         int               `json:"parsed"`
	Start          int               `json:"start"`
	Frames         []RFTraceFrame    `json:"frames"`
	ReaderFrames   int               `json:"reader_frames"`
	CardFrames     int               `json:"card_frames"`
	AuthCandidates []RFAuthCandidate `json:"auth_candidates"`
	Timing         string            `json:"timing"`
}
type RFFileExport struct {
	Format    string `json:"format"`
	MediaType string `json:"media_type"`
	Text      string `json:"text"`
}

// RFAnalysisParameters retains the requested options without duplicating source
// bytes. Effective selection and rate are reported separately in LF/HF views.
type RFAnalysisParameters struct {
	Start  int    `json:"start"`
	Count  int    `json:"count"`
	RateHz int    `json:"rate_hz"`
	Decode bool   `json:"decode"`
	Clock  int    `json:"clock"`
	Phase  int    `json:"phase"`
	Invert bool   `json:"invert"`
	Export string `json:"export,omitempty"`
}
type RFFileAnalysis struct {
	Schema     int                  `json:"schema_version"`
	Kind       string               `json:"kind"`
	Source     RFSource             `json:"source"`
	Parameters RFAnalysisParameters `json:"parameters"`
	Complete   bool                 `json:"complete"`
	Issues     []RFParseIssue       `json:"issues"`
	Notes      []string             `json:"notes"`
	LF         *RFSampleView        `json:"lf,omitempty"`
	HF         *RFTraceView         `json:"hf,omitempty"`
	Export     *RFFileExport        `json:"export,omitempty"`
}

func strictRFJSON(raw []byte, v any, fields ...string) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("JSON source must be UTF-8")
	}
	if err := uniqueFileJSON(string(raw)); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	for field := range object {
		if !allowed[field] {
			return fmt.Errorf("unsupported source field %q", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
func rfSelection(total, start, count int) (int, error) {
	if start >= total {
		return 0, fmt.Errorf("selection starts beyond confirmed data")
	}
	if count == 0 {
		count = total - start
	}
	if count > total-start {
		return 0, fmt.Errorf("selection extends beyond confirmed data")
	}
	return count, nil
}

// AnalyzeRFFile is a bounded file-only workflow. Parse completeness refers to the
// supplied bytes, never to an entire RF exchange or the origin of those bytes.
func AnalyzeRFFile(ctx context.Context, r RFFileRequest) (out RFFileAnalysis, err error) {
	if err = r.Validate(); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out = RFFileAnalysis{Schema: 1, Kind: "offline_rf", Source: RFSource{r.Name, r.Format, len(r.Data), imageDigest(r.Data), r.Layout, r.ShortMarkers}, Complete: true, Issues: []RFParseIssue{}, Notes: []string{"File declarations are not live RF evidence. No capture, transmission, authentication, key recovery or card-library conversion is performed."}}
	out.Parameters = RFAnalysisParameters{r.Start, r.Count, r.RateHz, r.Decode, r.Clock, r.Phase, r.Invert, r.Export}
	if r.Format == "lf-json" || r.Format == "lf-u8" {
		err = analyzeRFLCapture(ctx, r, &out)
	} else {
		err = analyzeRFTrace(ctx, r, &out)
	}
	if err == nil {
		err = ctx.Err()
	}
	return
}
func analyzeRFLCapture(ctx context.Context, r RFFileRequest, out *RFFileAnalysis) error {
	samples := LFSamples{Data: r.Data}
	rateSource := "unknown"
	if r.Format == "lf-json" {
		samples = LFSamples{}
		if err := strictRFJSON(r.Data, &samples, "rate_hz", "data", "requested_ms"); err != nil {
			return err
		}
		if samples.RequestedMS < 0 || samples.RateHz < 0 || samples.RateHz > 10000000 {
			return fmt.Errorf("invalid declared capture metadata")
		}
		if samples.RateHz != 0 {
			rateSource = "file_declared"
			if r.RateHz != 0 && r.RateHz != samples.RateHz {
				return fmt.Errorf("operator rate differs from file; no silent override")
			}
		}
	}
	if samples.RateHz == 0 && r.RateHz != 0 {
		samples.RateHz = r.RateHz
		rateSource = "operator_assigned"
	}
	if len(samples.Data) == 0 || len(samples.Data) > MaxRFSamples {
		return fmt.Errorf("LF input must contain 1..262144 unsigned 8-bit samples")
	}
	count, err := rfSelection(len(samples.Data), r.Start, r.Count)
	if err != nil {
		return err
	}
	data := append(HexBytes(nil), samples.Data[r.Start:r.Start+count]...)
	analysis, err := AnalyzeLFSamples(data)
	if err != nil {
		return err
	}
	view := &RFSampleView{Total: len(samples.Data), Start: r.Start, RateHz: samples.RateHz, RateSource: rateSource, Data: data, Analysis: analysis, RFHeuristicAvailable: samples.RateHz == 125000, Buckets: []RFSampleBucket{}}
	view.DeclaredRequestedMS = samples.RequestedMS
	out.LF = view
	if !view.RFHeuristicAvailable {
		view.Analysis.HalfPeriodSamples = 0
		view.Analysis.EstimatedRFDivider = 0
		view.Analysis.Hint = "RF-divider heuristics require a declared/assigned 125000 Hz rate"
	}
	out.Notes = append(out.Notes, "ADC values have no calibrated voltage scale. Time, if displayed, is calculated from the declared/assigned rate; it is not independently measured. Modulation and Manchester outputs are heuristics, not a protocol/card identity.")
	if r.Decode {
		if !view.RFHeuristicAvailable {
			return fmt.Errorf("Manchester RF-divider decoding requires 125000 Hz; no rate is assumed")
		}
		decoded, err := DecodeManchester(data, r.Clock, r.Invert, r.Phase)
		if err != nil {
			return err
		}
		view.Manchester = &decoded
		out.Notes = append(out.Notes, "Manchester decoding restarts at the selected range. Invalid pairs are omitted from its bit string; error counts mean it is not a lossless or position-preserving decode.")
	}
	for i, n := 0, min(1024, len(data)); i < n; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		a, b := i*len(data)/n, (i+1)*len(data)/n
		lo, hi := byte(255), byte(0)
		for _, v := range data[a:b] {
			lo = min(lo, v)
			hi = max(hi, v)
		}
		view.Buckets = append(view.Buckets, RFSampleBucket{r.Start + a, r.Start + b, lo, hi})
	}
	if r.Export != "" {
		var text, media string
		if r.Export == "csv" {
			if len(data) > 65536 {
				return fmt.Errorf("CSV export is limited to a selection of 65536 samples")
			}
			if samples.RateHz == 0 {
				return fmt.Errorf("CSV time export requires a declared/assigned rate")
			}
			text, err = LFSamplesCSV(data, samples.RateHz)
			media = "text/csv"
		} else {
			text, err = LFSamplesSVG(data)
			media = "image/svg+xml"
		}
		if err != nil {
			return err
		}
		out.Export = &RFFileExport{r.Export, media, text}
		out.Notes = append(out.Notes, "CSV/SVG exports describe the selected range; CSV indices/time restart at zero. The report retains the absolute selection start and source hash.")
	}
	return nil
}
func analyzeRFTrace(ctx context.Context, r RFFileRequest, out *RFFileAnalysis) error {
	view := &RFTraceView{Frames: []RFTraceFrame{}, AuthCandidates: []RFAuthCandidate{}, Timing: "not_captured"}
	out.HF = view
	issue := func(code string, offset int, unit, message string) {
		out.Complete = false
		out.Issues = append(out.Issues, RFParseIssue{code, offset, unit, message})
	}
	frames := []RFTraceFrame{}
	appendFrame := func(raw TraceFrame, offset *int, timestamp *uint32, duration *uint16, parity HexBytes) bool {
		if raw.Bits == 0 || len(raw.Data) != (int(raw.Bits)+7)/8 {
			issue("invalid_frame", len(frames), "frame", "Bits must be positive and agree with payload length")
			return false
		}
		f := RFTraceFrame{Index: len(frames), Offset: offset, FromCard: raw.FromCard, RawBits: int(raw.Bits), Bits: int(raw.Bits), Data: append(HexBytes(nil), raw.Data...), Parity: parity, ParityCheck: "not_captured", CRCACandidate: "not_applicable", Label: "unclassified", Timestamp: timestamp, Duration: duration}
		if r.Layout == "parity9" && f.RawBits >= 8 {
			if f.RawBits%9 != 0 {
				issue("ambiguous_parity_layout", len(frames), "frame", "Parity layout requires complete 9-bit groups or a short frame")
				return false
			}
			n := f.RawBits / 9
			f.RawData = f.Data
			f.Data = make(HexBytes, n)
			f.Parity = make(HexBytes, (n+7)/8)
			f.Bits = n * 8
			for i := 0; i < n; i++ {
				for j := 0; j < 8; j++ {
					p := i*9 + j
					f.Data[i] |= ((raw.Data[p/8] >> uint(p%8)) & 1) << uint(j)
				}
				p := i*9 + 8
				f.Parity[i/8] |= ((raw.Data[p/8] >> uint(p%8)) & 1) << uint(7-i%8)
			}
		}
		if len(f.Parity) > 0 {
			f.ParityCheck = "odd_parity_matches"
			for i, b := range f.Data {
				p := (f.Parity[i/8] >> uint(7-i%8)) & 1
				if (bits.OnesCount8(b)+int(p))%2 != 1 {
					f.ParityErrors++
				}
			}
			if f.ParityErrors > 0 {
				f.ParityCheck = "odd_parity_mismatch"
			}
		}
		if f.Bits%8 == 0 && len(f.Data) >= 3 {
			f.CRCACandidate = "does_not_match_suffix"
			if crcASuffix(f.Data) {
				f.CRCACandidate = "matches_suffix"
			}
		}
		if !f.FromCard {
			switch {
			case f.Bits == 7 && f.Data[0]&0x7f == 0x26:
				f.Label = "REQA candidate"
			case f.Bits == 7 && f.Data[0]&0x7f == 0x52:
				f.Label = "WUPA candidate"
			case f.Bits == 32 && len(f.Data) == 4 && (f.Data[0] == 0x60 || f.Data[0] == 0x61) && f.CRCACandidate == "matches_suffix":
				f.Label = "Classic authentication request candidate"
			}
		}
		f.BitLengthSource = "record_declared"
		if r.Format == "pm3-trace" {
			f.RawBits = 0
			f.BitLengthSource = "record_byte_length"
			if f.Bits < 8 {
				f.BitLengthSource = "gui_short_marker"
			}
		}
		frames = append(frames, f)
		return true
	}
	if r.Format == "hf-json" {
		if !utf8.Valid(r.Data) {
			return fmt.Errorf("JSON source must be UTF-8")
		}
		if err := uniqueFileJSON(string(r.Data)); err != nil {
			return err
		}
		d := json.NewDecoder(bytes.NewReader(r.Data))
		token, err := d.Token()
		if err != nil || token != json.Delim('[') {
			return fmt.Errorf("HF JSON must be a TraceFrame array")
		}
		for d.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(frames) >= MaxRFFrames {
				issue("frame_limit", len(frames), "frame", "Only the bounded confirmed prefix is available")
				break
			}
			var item json.RawMessage
			if err := d.Decode(&item); err != nil {
				return err
			}
			var v struct {
				FromCard *bool     `json:"from_card"`
				Bits     *uint16   `json:"bits"`
				Data     *HexBytes `json:"data"`
			}
			if err := strictRFJSON(item, &v, "from_card", "bits", "data"); err != nil {
				issue("invalid_frame_json", len(frames), "frame", err.Error())
				break
			}
			if v.FromCard == nil || v.Bits == nil || v.Data == nil || *v.Bits > 32767 {
				issue("missing_frame_fields", len(frames), "frame", "Explicit direction, 1..32767 bits and data are required")
				break
			}
			if !appendFrame(TraceFrame{*v.FromCard, *v.Bits, *v.Data}, nil, nil, nil, nil) {
				break
			}
		}
	} else {
		for offset := 0; offset < len(r.Data); {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(frames) >= MaxRFFrames {
				issue("frame_limit", offset, "byte", "Only the bounded confirmed prefix is available")
				break
			}
			start := offset
			if r.Format == "hf-packed" {
				if len(r.Data)-offset < 2 {
					issue("truncated_header", offset, "byte", "Incomplete two-byte frame header")
					break
				}
				header := binary.BigEndian.Uint16(r.Data[offset:])
				n := (int(header&0x7fff) + 7) / 8
				if n > len(r.Data)-offset-2 {
					issue("truncated_payload", offset, "byte", "Declared frame payload is incomplete; no bytes were padded")
					break
				}
				parsed, _ := ParseTrace(r.Data[offset : offset+2+n])
				if !appendFrame(parsed[0], &start, nil, nil, nil) {
					break
				}
				offset += 2 + n
			} else {
				view.Timing = "file_declared_ticks_unverified"
				if len(r.Data)-offset < 8 {
					issue("truncated_header", offset, "byte", "Incomplete eight-byte PM3 record header")
					break
				}
				stamp := binary.LittleEndian.Uint32(r.Data[offset:])
				duration := binary.LittleEndian.Uint16(r.Data[offset+4:])
				header := binary.LittleEndian.Uint16(r.Data[offset+6:])
				n := int(header & 0x7fff)
				p := max(1, (n+7)/8)
				if n == 0 || n > 4095 {
					issue("unsupported_record_length", offset, "byte", "Supported PM3 records contain 1..4095 payload bytes")
					break
				}
				if 8+n+p > len(r.Data)-offset {
					issue("truncated_record", offset, "byte", "Incomplete PM3 data/parity record; no bytes were padded")
					break
				}
				data := r.Data[offset+8 : offset+8+n]
				parity := append(HexBytes(nil), r.Data[offset+8+n:offset+8+n+p]...)
				bitLength := n * 8
				if r.ShortMarkers && n == 1 && parity[0] >= 1 && parity[0] <= 7 {
					bitLength = int(parity[0])
					parity = nil
				}
				if !appendFrame(TraceFrame{header&0x8000 != 0, uint16(bitLength), data}, &start, &stamp, &duration, parity) {
					break
				}
				offset += 8 + n + p
			}
		}
	}
	if len(frames) == 0 && out.Complete {
		return fmt.Errorf("trace contains no frames")
	}
	view.Parsed = len(frames)
	if len(frames) > 0 {
		count, err := rfSelection(len(frames), r.Start, r.Count)
		if err != nil {
			return err
		}
		view.Start = r.Start
		view.Frames = frames[r.Start : r.Start+count]
	}
	for i, f := range view.Frames {
		if f.FromCard {
			view.CardFrames++
		} else {
			view.ReaderFrames++
		}
		if f.Label != "Classic authentication request candidate" || i+1 >= len(view.Frames) {
			continue
		}
		next := view.Frames[i+1]
		if !next.FromCard || next.Bits != 32 {
			continue
		}
		candidate := RFAuthCandidate{Request: f.Index, Nonce: next.Index, Block: f.Data[1], KeyType: "A"}
		if f.Data[0] == 0x61 {
			candidate.KeyType = "B"
		}
		if i+2 < len(view.Frames) && !view.Frames[i+2].FromCard && view.Frames[i+2].Bits == 64 {
			index := view.Frames[i+2].Index
			candidate.ReaderAnswer = &index
			if i+3 < len(view.Frames) && view.Frames[i+3].FromCard && view.Frames[i+3].Bits == 32 {
				index := view.Frames[i+3].Index
				candidate.CardAnswer = &index
			}
		}
		view.AuthCandidates = append(view.AuthCandidates, candidate)
	}
	out.Notes = append(out.Notes, "CRC-A is only a candidate suffix comparison. Missing CRC, encryption or a different protocol can cause a mismatch. Odd-parity comparison does not authenticate frames.", "No timestamps are synthesized. PM3/GUI time fields are retained as file declarations, which may be synthetic; no real-time scale is assumed.", "Authentication fragments are adjacent direction/length candidates only. UID, key, plaintext/encrypted nonce and exchange identity are not verified; no recovery inputs are generated.")
	return nil
}
func crcASuffix(data []byte) bool {
	crc := uint16(0x6363)
	for _, b := range data[:len(data)-2] {
		b ^= byte(crc)
		b ^= b << 4
		crc = (crc >> 8) ^ (uint16(b) << 8) ^ (uint16(b) << 3) ^ (uint16(b) >> 4)
	}
	return data[len(data)-2] == byte(crc) && data[len(data)-1] == byte(crc>>8)
}
