package chameleon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RFCaptureRequest selects one existing acquisition API. Acknowledgment covers
// temporary mode changes and possible reload of unsaved emulator RAM, not a
// grant to persist slots or write a physical card.
type RFCaptureRequest struct {
	Action                string   `json:"action"`
	DurationMS            int      `json:"duration_ms"`
	Block                 int      `json:"block,omitempty"`
	KeyType               KeyType  `json:"key_type,omitempty"`
	Key                   HexBytes `json:"key,omitempty"`
	AcknowledgeSlotReload bool     `json:"acknowledge_slot_reload"`
}

func (r RFCaptureRequest) FeatureID() string {
	if r.Action == "hf-sniff" || r.Action == "hf-auth" {
		return "rf.hf-trace"
	}
	return "rf.lf-samples"
}
func (r RFCaptureRequest) Requirements() ([]Command, error) {
	var command Command
	limit := 30000
	switch r.Action {
	case "hf-sniff":
		command = CmdHf14aSniff
	case "hf-auth":
		command = CmdHf14aAuthTrace
		if _, err := keyPayload(r.Block, r.KeyType, r.Key); err != nil {
			return nil, err
		}
	case "lf-samples":
		command, limit = CmdLfSniff, 10000
	case "lf-adc":
		command, limit = CmdAdcGenericRead, 0
	default:
		return nil, fmt.Errorf("choose hf-sniff, hf-auth, lf-samples or lf-adc")
	}
	if !r.AcknowledgeSlotReload {
		return nil, fmt.Errorf("acknowledge possible loss of unsaved slot RAM before capture")
	}
	if r.Action != "hf-auth" && (r.Block != 0 || r.KeyType != 0 || len(r.Key) != 0) {
		return nil, fmt.Errorf("authentication options apply only to hf-auth")
	}
	if limit == 0 && r.DurationMS != 0 || limit > 0 && (r.DurationMS < 1 || r.DurationMS > limit) {
		return nil, fmt.Errorf("invalid duration for %s (maximum %d ms; ADC accepts zero only)", r.Action, limit)
	}
	return []Command{CmdGetDeviceMode, CmdChangeDeviceMode, command}, nil
}

func (r RFCaptureRequest) ValidateTimeout(timeout time.Duration) error {
	if _, err := r.Requirements(); err != nil {
		return err
	}
	margin := time.Second
	if r.Action == "hf-auth" {
		margin = 2 * time.Second
	}
	if r.Action != "lf-adc" && timeout < time.Duration(r.DurationMS)*time.Millisecond+margin {
		return fmt.Errorf("capture duration exceeds command timeout; start Desk with --timeout at least %s or shorten duration", time.Duration(r.DurationMS)*time.Millisecond+margin)
	}
	return nil
}

type RFCaptureFile struct {
	Name   string   `json:"name"`
	Format string   `json:"format"`
	Data   HexBytes `json:"data"`
}
type RFCaptureResult struct {
	Schema       int            `json:"schema_version"`
	Kind         string         `json:"kind"`
	Action       string         `json:"action"`
	SessionID    string         `json:"session_id,omitempty"`
	RequestedMS  int            `json:"requested_ms"`
	Status       string         `json:"status"`
	ModeBefore   string         `json:"mode_before,omitempty"`
	ModeAfter    string         `json:"mode_after,omitempty"`
	ModeRestored bool           `json:"mode_restored"`
	File         *RFCaptureFile `json:"file,omitempty"`
	Notes        []string       `json:"notes"`
}

// CaptureRF reuses the existing mode-scoped acquisition APIs. A returned file
// can survive a cleanup error; callers must inspect both result and error.
// SDK callers must not interleave other workflows on the client between its
// mode observations. The Web session gate serializes this entire workflow.
func (c *Client) CaptureRF(ctx context.Context, r RFCaptureRequest) (out RFCaptureResult, err error) {
	if err = r.ValidateTimeout(c.timeout); err != nil {
		return
	}
	if c.transportInfo.NativeUSBMaintenance {
		return out, ErrUSBBridgeRequired
	}
	out = RFCaptureResult{Schema: 1, Kind: "rf_capture", Action: r.Action, RequestedMS: r.DurationMS, Status: "failed", Notes: []string{
		"Mode transitions suspend/resume emulation and can reset protocol state. HF sniff additionally reloads stored slot data and may discard unsaved RAM. Desk does not save or switch slots or restore a backup.",
		"A successful response is not proof of a complete RF exchange or a full requested interval. Firmware buffers may stop early or omit frames without reporting overflow.",
		"Cancellation cannot abort the blocking firmware capture command. A lost response closes the connection; reconnect and verify mode/slot state. No capture or mode-change command is automatically replayed.",
	}}
	out.ModeBefore, err = c.Mode(ctx)
	if err != nil {
		return
	}
	var data []byte
	format := "hf-json"
	duration := time.Duration(r.DurationMS) * time.Millisecond
	switch r.Action {
	case "hf-sniff", "hf-auth":
		var frames []TraceFrame
		if r.Action == "hf-sniff" {
			out.Notes = append(out.Notes, "HF sniff runs the active emulator, responds as a card and unconditionally reloads active slot data. It is not a passive third-party sniffer; an external reader may modify emulator RAM through normal card commands. RX/TX parity layout depends on firmware; inspect raw data before selecting an offline layout.")
			frames, err = c.SniffHF(ctx, uint16(r.DurationMS))
		} else {
			out.Notes = append(out.Notes, "HF auth trace actively selects/authenticates a Classic card with the supplied key. Firmware may synthesize anticollision frames; this is not an entirely observed exchange.")
			frames, err = c.AuthTrace(ctx, r.Block, r.KeyType, r.Key, duration)
		}
		if len(frames) > 0 {
			data, _ = json.Marshal(frames)
		}
	case "lf-samples":
		var samples LFSamples
		samples, err = c.CaptureLF(ctx, duration)
		format = "lf-json"
		if len(samples.Data) > 0 {
			data, _ = json.Marshal(samples)
		}
		out.Notes = append(out.Notes, "LF samples enable the reader field. 125000 Hz is an SDK/firmware convention, not an independent rate measurement; requested duration is a timeout ceiling, not sample duration.")
	case "lf-adc":
		data, err = c.ReadLFADC(ctx)
		format = "lf-u8"
		out.Notes = append(out.Notes, "ADC uses the existing fixed firmware acquisition. Its rate and actual duration are not returned and remain unknown.")
	}
	if errors.Is(err, ErrNoCapture) || err == nil && len(data) == 0 {
		out.Status = "no_capture"
	} else if len(data) > 0 {
		out.Status = "captured"
	}
	if len(data) > 0 {
		out.File = &RFCaptureFile{Name: "desk-" + r.Action + ".json", Format: format, Data: HexBytes(data)}
		if format == "lf-u8" {
			out.File.Name = "desk-lf-adc.bin"
		}
	}
	var restore *ModeRestoreError
	if errors.As(err, &restore) {
		return
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
		return
	}
	if c.IsClosed() {
		err = errors.Join(err, ErrClosed)
		return
	}
	var modeErr error
	out.ModeAfter, modeErr = c.Mode(ctx)
	out.ModeRestored = modeErr == nil && out.ModeAfter == out.ModeBefore
	if modeErr == nil && !out.ModeRestored {
		modeErr = fmt.Errorf("mode readback differs from initial mode; verify device state")
	}
	err = errors.Join(err, modeErr)
	// No observation is a normal acquisition outcome, but cleanup errors are not.
	if errors.Is(err, ErrNoCapture) && modeErr == nil {
		err = nil
	}
	return
}
