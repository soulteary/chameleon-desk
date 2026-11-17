package chameleon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func captureRequest(action string) RFCaptureRequest {
	r := RFCaptureRequest{Action: action, DurationMS: 1, AcknowledgeSlotReload: true}
	if action == "lf-adc" {
		r.DurationMS = 0
	}
	if action == "hf-auth" {
		r.Block = 4
		r.KeyType = KeyA
		r.Key = HexBytes{1, 2, 3, 4, 5, 6}
	}
	return r
}
func TestRFCaptureReusesAcquisitionAndReportsModeReadback(t *testing.T) {
	for _, action := range []string{"hf-sniff", "hf-auth", "lf-samples", "lf-adc"} {
		t.Run(action, func(t *testing.T) {
			r := captureRequest(action)
			original, desired := byte(0), byte(1)
			cmd, status := uint16(3031), uint16(0x40)
			data, payload := []byte{0, 200, 0, 200}, []byte{0, 1}
			format := "lf-json"
			switch action {
			case "hf-sniff":
				original, desired = 1, 0
				cmd, status = 2020, 0x68
				data = []byte{0, 7, 0x26}
				format = "hf-json"
			case "hf-auth":
				cmd, status = 2017, 0
				data = []byte{0, 7, 0x26}
				payload = append([]byte{0x60, 4}, r.Key...)
				payload = append(payload, 0, 1)
				format = "hf-json"
			case "lf-adc":
				cmd, payload = 3009, nil
				format = "lf-u8"
			}
			c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{original}}, {command: 1002, status: 0x68, data: []byte{original}}, {command: 1001, status: 0x68, request: []byte{desired}}, {command: cmd, status: status, request: payload, data: data}, {command: 1001, status: 0x68, request: []byte{original}}, {command: 1002, status: 0x68, data: []byte{original}}})
			c.timeout = 3 * time.Second
			out, err := c.CaptureRF(context.Background(), r)
			if err != nil || out.Status != "captured" || !out.ModeRestored || out.ModeBefore != out.ModeAfter || out.File == nil || out.File.Format != format {
				t.Fatal(out, err)
			}
			report, err := AnalyzeRFFile(context.Background(), RFFileRequest{Format: format, Name: out.File.Name, Data: out.File.Data, Layout: func() string {
				if format == "hf-json" {
					return "data"
				}
				return ""
			}()})
			if err != nil {
				t.Fatal("capture file not accepted by offline analyzer", err)
			}
			if action == "lf-adc" && report.LF.RateHz != 0 || action == "lf-samples" && (report.LF.RateHz != 125000 || report.LF.DeclaredRequestedMS != 1) {
				t.Fatal(report)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "010203040506") {
				t.Fatal("key retained in capture metadata")
			}
		})
	}
}
func TestRFCaptureNoObservationAndCleanupFailureAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status, cleanup uint16
		data            []byte
		wantErr         bool
		wantFile        bool
	}{
		{"empty", 1, 0x68, nil, false, false}, {"cleanup", 0x68, 0x67, []byte{0, 7, 0x26}, true, true}, {"no-data-cleanup", 1, 0x67, nil, true, false}, {"malformed", 0x68, 0x68, []byte{0, 9, 0x26}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, status: 0x68, request: []byte{0}}, {command: 2020, status: tc.status, request: []byte{0, 1}, data: tc.data}, {command: 1001, status: tc.cleanup, request: []byte{1}}}
			if tc.cleanup == 0x68 {
				steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}})
			}
			c := readerSimulator(t, steps)
			c.timeout = 3 * time.Second
			out, err := c.CaptureRF(context.Background(), captureRequest("hf-sniff"))
			if (err != nil) != tc.wantErr || (out.File != nil) != tc.wantFile || out.ModeRestored != (tc.cleanup == 0x68) {
				t.Fatal(out, err)
			}
			if tc.status == 1 && out.Status != "no_capture" {
				t.Fatal(out)
			}
		})
	}
}
func TestRFCaptureValidationCapabilitiesAndTransport(t *testing.T) {
	r := captureRequest("hf-sniff")
	for _, change := range []func(*RFCaptureRequest){func(r *RFCaptureRequest) { r.AcknowledgeSlotReload = false }, func(r *RFCaptureRequest) { r.DurationMS = 30001 }, func(r *RFCaptureRequest) { r.Key = HexBytes{1} }, func(r *RFCaptureRequest) { r.Action = "unknown" }} {
		bad := r
		change(&bad)
		if _, err := bad.Requirements(); err == nil {
			t.Fatal("invalid request accepted", bad)
		}
	}
	if err := r.ValidateTimeout(time.Second); err == nil {
		t.Fatal("timeout margin missing")
	}
	c := readerSimulator(t, nil)
	c.timeout = 3 * time.Second
	c.transportInfo.NativeUSBMaintenance = true
	if _, err := c.CaptureRF(context.Background(), r); !errors.Is(err, ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
	for _, action := range []string{"hf-sniff", "hf-auth", "lf-samples", "lf-adc"} {
		r := captureRequest(action)
		cmds, _ := r.Requirements()
		model := byte(0)
		capabilities := []uint16{}
		for _, command := range cmds {
			capabilities = append(capabilities, uint16(command))
		}
		device := &FeatureDevice{ModelID: &model, Capabilities: capabilities, Transport: TransportInfo{Connection: "tcp", Medium: "ble"}}
		out, err := EvaluateFeature(r.FeatureID(), "web", FeatureContext{Device: device, WritesAllowed: true}, &FeatureRequest{RFCapture: &r})
		if err != nil || out.State != "available" || len(out.RequiredCommands) != 3 {
			t.Fatal(out, err)
		}
		readOnly, _ := EvaluateFeature(r.FeatureID(), "web", FeatureContext{Device: device}, &FeatureRequest{RFCapture: &r})
		if action == "hf-sniff" && readOnly.State != "unavailable" || action != "hf-sniff" && readOnly.State != "available" {
			t.Fatal("capture write boundary", action, readOnly)
		}
		device.Capabilities = device.Capabilities[:2]
		out, _ = EvaluateFeature(r.FeatureID(), "web", FeatureContext{Device: device, WritesAllowed: true}, &FeatureRequest{RFCapture: &r})
		if out.State != "unavailable" {
			t.Fatal(out)
		}
	}
}

func TestRFCaptureCancellationAfterTransmissionClosesWithoutReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c *Client
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 1001, status: 0x68, request: []byte{0}},
		{command: 2020, status: 0x68, request: []byte{0, 1}, before: func() { cancel(); <-c.done }},
	})
	c.timeout = 3 * time.Second
	out, err := c.CaptureRF(ctx, captureRequest("hf-sniff"))
	if !errors.Is(err, context.Canceled) || !c.IsClosed() || out.ModeRestored || out.File != nil || c.Stats().Requests != 4 {
		t.Fatal(out, err, c.Stats())
	}
}
