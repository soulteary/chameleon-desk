package main

import (
	"context"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
)

func (ws *webServer) configurationPreflight(ctx context.Context, c *chameleon.Client, id string, request *chameleon.FeatureRequest) error {
	// Reject pairing changes before even probing the device on an unsuitable link.
	if request != nil && request.SettingsPatch != nil && (request.SettingsPatch.BLEKey != nil || request.SettingsPatch.BLEPairing != nil) {
		t := c.TransportInfo()
		if t.Connection != "serial" || t.Medium != "serial" || !t.AllowsPairingChanges {
			return fmt.Errorf("pairing changes require a direct serial connection")
		}
	}
	facts, err := c.FeatureDevice(ctx)
	if err != nil {
		return err
	}
	state, err := chameleon.EvaluateFeature(id, "web", ws.featureContext(&facts), request)
	if err != nil {
		return err
	}
	if state.State != "available" {
		return fmt.Errorf("configuration preflight: %v", state.Reasons)
	}
	return nil
}
func (ws *webServer) handleSettingsRead(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.configurationPreflight(ctx, c, "device.settings", nil); err != nil {
			return nil, err
		}
		return c.SettingsView(ctx, false)
	})
}
func (ws *webServer) handleSlotDetails(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.configurationPreflight(ctx, c, "slot.read-details", nil); err != nil {
			return nil, err
		}
		return c.SlotDetails(ctx)
	})
}
func (ws *webServer) handleSettingsConfigure(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var input struct {
		Patch   chameleon.SettingsPatch `json:"patch"`
		Persist bool                    `json:"persist"`
	}
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := chameleon.SettingsRequirements(input.Patch, input.Persist); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		markConfigurationWrite(ctx, ws, false)
		out := struct {
			Settings *chameleon.SettingsView `json:"settings,omitempty"`
			Verified bool                    `json:"readback_verified"`
			Saved    bool                    `json:"save_confirmed"`
		}{}
		if err := ws.configurationPreflight(ctx, c, "device.configure", &chameleon.FeatureRequest{SettingsPatch: &input.Patch, Persist: input.Persist}); err != nil {
			return out, err
		}
		markConfigurationWrite(ctx, ws, true)
		value, err := c.ConfigureSettings(ctx, input.Patch, input.Persist)
		if err == nil {
			out.Settings = &value
			out.Verified = true
			out.Saved = input.Persist
		}
		return out, err
	})
}

type slotEditResult struct {
	Slot                       int             `json:"slot"`
	Sense                      chameleon.Sense `json:"sense"`
	Action                     string          `json:"action"`
	CompletedOperations        int             `json:"completed_operations"`
	Before                     *chameleon.Slot `json:"before,omitempty"`
	After                      *chameleon.Slot `json:"after,omitempty"`
	Nickname                   *string         `json:"nickname"`
	ConfigurationVerified      bool            `json:"configuration_verified"`
	InitializationAcknowledged bool            `json:"initialization_acknowledged"`
	MemoryVerified             bool            `json:"memory_verified"`
	Saved                      bool            `json:"save_confirmed"`
}

func (ws *webServer) handleSlotEdit(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var input chameleon.SlotEdit
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
		out := slotEditResult{Slot: input.Slot, Sense: input.Sense, Action: input.Action}
		if err := ws.configurationPreflight(ctx, c, "slot.configure", &chameleon.FeatureRequest{SlotEdit: &input}); err != nil {
			return out, err
		}
		if input.Action == "nickname" || input.Action == "nickname-delete" {
			markConfigurationWrite(ctx, ws, true)
			if err := c.UpdateSlotNickname(ctx, input.Slot, input.Sense, input.Name); err != nil {
				return out, err
			}
			out.Nickname = input.Name
			out.ConfigurationVerified = true
			out.CompletedOperations++
		} else {
			before, err := c.Slots(ctx)
			if err != nil {
				return out, err
			}
			out.Before = &before[input.Slot-1]
			currentType := out.Before.LFType
			if input.Sense == chameleon.SenseHF {
				currentType = out.Before.HFType
			}
			if input.Action == "initialize" && currentType != *input.Tag {
				return out, fmt.Errorf("initialization type differs from current channel; change its type explicitly first")
			}
			markConfigurationWrite(ctx, ws, true)
			switch input.Action {
			case "type":
				err = c.SetSlotType(ctx, input.Slot, *input.Tag)
			case "enable":
				err = c.EnableSlot(ctx, input.Slot, input.Sense, *input.Enabled)
			case "initialize":
				err = c.InitializeSlot(ctx, input.Slot, *input.Tag)
				out.InitializationAcknowledged = err == nil
			}
			if err != nil {
				return out, err
			}
			out.CompletedOperations++
			after, err := c.Slots(ctx)
			if err != nil {
				return out, err
			}
			out.After = &after[input.Slot-1]
			typ, enabled := out.After.LFType, out.After.LFEnabled
			if input.Sense == chameleon.SenseHF {
				typ, enabled = out.After.HFType, out.After.HFEnabled
			}
			if (input.Tag != nil && typ != *input.Tag) || (input.Enabled != nil && enabled != *input.Enabled) {
				return out, fmt.Errorf("slot configuration readback mismatch")
			}
			out.ConfigurationVerified = true
		}
		if input.Persist {
			if err := c.SaveSlotData(ctx); err != nil {
				return out, err
			}
			out.Saved = true
		}
		return out, nil
	})
}

// This boundary means entering a mutating SDK call, not proof of transmission.
func markConfigurationWrite(ctx context.Context, ws *webServer, attempted bool) {
	if task, ok := ctx.Value(taskKey{}).(*webTask); ok {
		ws.tasks.mu.Lock()
		task.WriteAttempted = &attempted
		ws.tasks.mu.Unlock()
	}
}
