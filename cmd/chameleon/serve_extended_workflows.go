package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) handleExtendedWorkflow(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if operation, ok := chameleon.OperationForPath(path); ok && operation.Write {
		markConfigurationWrite(r.Context(), ws, false)
	}
	if path == "/api/device/mode" {
		if !ws.needWrites(w) {
			return
		}
		var input chameleon.DeviceModeChange
		if err := decodeBody(r, &input); err != nil {
			writeErr(w, 400, err)
			return
		}
		if _, err := input.Requirements(); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			markConfigurationWrite(ctx, ws, false)
			if err := ws.featurePreflight(ctx, c, "device.mode", &chameleon.FeatureRequest{ModeChange: &input}); err != nil {
				return nil, err
			}
			mode, err := c.ChangeDeviceModeObserved(ctx, input, func() { markConfigurationWrite(ctx, ws, true) })
			return map[string]any{"mode": mode}, err
		})
		return
	}

	if path == "/api/reader/config-read" {
		var empty struct{}
		if err := decodeBody(r, &empty); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			if err := ws.featurePreflight(ctx, c, "reader.config-read", nil); err != nil {
				return nil, err
			}
			return c.ReaderConfig(ctx)
		})
		return
	}
	if path == "/api/reader/config-update" {
		if !ws.needWrites(w) {
			return
		}
		var input chameleon.ReaderConfigUpdate
		if err := decodeBody(r, &input); err != nil {
			writeErr(w, 400, err)
			return
		}
		if _, err := input.Requirements(); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			markConfigurationWrite(ctx, ws, false)
			if err := ws.featurePreflight(ctx, c, "reader.config-update", &chameleon.FeatureRequest{ReaderConfig: &input}); err != nil {
				return nil, err
			}
			return c.ConfigureReaderObserved(ctx, input, func() { markConfigurationWrite(ctx, ws, true) })
		})
		return
	}
	if path == "/api/reader/magic-inspect" {
		var input chameleon.MagicInspection
		if err := decodeBody(r, &input); err != nil {
			writeErr(w, 400, err)
			return
		}
		if _, err := input.Requirements(); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			if err := ws.featurePreflight(ctx, c, "reader.magic-inspect", &chameleon.FeatureRequest{Magic: &input}); err != nil {
				return nil, err
			}
			return c.InspectMagic(ctx, input)
		})
		return
	}
	if path == "/api/slot/emulator/read" || path == "/api/slot/emulator/configure" || path == "/api/slot/emulator/logs" {
		feature, action := "slot.emulator-read", "configuration"
		if path == "/api/slot/emulator/configure" {
			feature, action = "slot.emulator-configure", "configure"
			if !ws.needWrites(w) {
				return
			}
		} else if path == "/api/slot/emulator/logs" {
			feature, action = "slot.emulator-logs", "logs"
		}
		var input chameleon.CurrentEmulatorRequest
		if err := decodeBody(r, &input); err != nil {
			writeErr(w, 400, err)
			return
		}
		if input.Action != action {
			writeErr(w, 400, fmt.Errorf("action differs from endpoint"))
			return
		}
		if _, err := input.Requirements(); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			if action == "configure" {
				markConfigurationWrite(ctx, ws, false)
			}
			if err := ws.featurePreflight(ctx, c, feature, &chameleon.FeatureRequest{CurrentEmulator: &input}); err != nil {
				return nil, err
			}
			switch action {
			case "configuration":
				return c.ReadCurrentEmulator(ctx, input)
			case "logs":
				return c.ReadCurrentDetectionLogs(ctx, input)
			default:
				return c.ConfigureCurrentEmulator(ctx, input, func() { markConfigurationWrite(ctx, ws, true) })
			}
		})
		return
	}
	var body struct {
		Request      json.RawMessage `json:"request"`
		Confirmation string          `json:"confirmation,omitempty"`
		Confirm      bool            `json:"confirm"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	plan := path == "/api/reader/type2-batch-plan" || path == "/api/reader/value-plan"
	if !plan && !ws.needWrites(w) {
		return
	}
	if plan && (body.Confirmation != "" || body.Confirm) {
		writeErr(w, 400, fmt.Errorf("plans must not contain execution confirmation"))
		return
	}
	if !plan && (!body.Confirm || body.Confirmation == "") {
		writeErr(w, 400, fmt.Errorf("review and confirm the current write plan"))
		return
	}
	if path == "/api/reader/type2-batch-plan" || path == "/api/reader/type2-batch-write" {
		var input chameleon.Type2BatchWrite
		if err := decodeReaderJSON(body.Request, &input); err != nil {
			writeErr(w, 400, err)
			return
		}
		if _, err := input.Requirements(); err != nil {
			writeErr(w, 400, err)
			return
		}
		ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
			feature := "reader.type2-batch-plan"
			if !plan {
				feature = "reader.type2-batch-write"
				markConfigurationWrite(ctx, ws, false)
				if err := ws.verifyPhysicalConfirmation("type2-batch", input, body.Confirmation); err != nil {
					return nil, err
				}
			}
			if err := ws.featurePreflight(ctx, c, feature, &chameleon.FeatureRequest{Type2Batch: &input}); err != nil {
				return nil, err
			}
			if !plan {
				return c.WriteType2UserPagesObserved(ctx, input, func() { markConfigurationWrite(ctx, ws, true) })
			}
			result, err := c.PlanType2UserPages(ctx, input)
			if err != nil {
				return result, err
			}
			input.ExpectedVersion = result.Version
			token, err := ws.physicalConfirmation("type2-batch", input)
			if err != nil {
				return nil, err
			}
			return map[string]any{"plan": result, "confirmation": token}, nil
		})
		return
	}
	var input chameleon.ClassicValueRequest
	if err := decodeReaderJSON(body.Request, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		feature := "reader.value-plan"
		if !plan {
			feature = "reader.value-write"
			markConfigurationWrite(ctx, ws, false)
			if err := ws.verifyPhysicalConfirmation("classic-value", input, body.Confirmation); err != nil {
				return nil, err
			}
		}
		if err := ws.featurePreflight(ctx, c, feature, &chameleon.FeatureRequest{Value: &input}); err != nil {
			return nil, err
		}
		if !plan {
			return c.WriteClassicValueObserved(ctx, input, func() { markConfigurationWrite(ctx, ws, true) })
		}
		result, err := c.PlanClassicValue(ctx, input)
		if err != nil {
			return result, err
		}
		token, err := ws.physicalConfirmation("classic-value", input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"plan": result, "confirmation": token}, nil
	})
}
