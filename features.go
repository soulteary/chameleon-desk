package chameleon

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

//go:embed internal/features/catalog.json
var featureCatalogJSON []byte

const FeatureSchemaVersion = 1

type FeatureEntry struct {
	Status   string   `json:"status"`
	Entries  []string `json:"entries"`
	Controls []string `json:"controls,omitempty"`
}
type ConditionalCommands struct {
	When     string    `json:"when"`
	Commands []Command `json:"commands"`
}

// FeatureDefinition describes a workflow, not a claim of hardware compatibility.
// Commands are unconditional; Conditional lists alternatives/options, never their union.
type FeatureDefinition struct {
	Effect string `json:"effect"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Device bool   `json:"device"`
	// Write is unconditional permission gating; parameter-dependent effects are resolved separately.
	Write           bool                  `json:"write"`
	Models          []string              `json:"models"`
	Transport       string                `json:"transport"`
	RequirementMode string                `json:"requirement_mode"`
	Commands        []Command             `json:"commands"`
	Conditional     []ConditionalCommands `json:"conditional"`
	Web             FeatureEntry          `json:"web"`
	CLI             FeatureEntry          `json:"cli"`
	SDK             FeatureEntry          `json:"sdk"`
	Notes           []string              `json:"notes"`
	Sources         []string              `json:"sources"`
}

// Features returns independent copies of the versioned workflow catalog.
func Features() []FeatureDefinition {
	var doc struct {
		Features []FeatureDefinition `json:"features"`
	}
	if err := json.Unmarshal(featureCatalogJSON, &doc); err != nil {
		panic(err)
	}
	return doc.Features
}

// FeatureDevice contains observations from one device session. Nil capabilities
// mean unknown; an empty non-nil list means the device advertised no commands.
type FeatureDevice struct {
	ModelID      *byte         `json:"model_id"`
	Capabilities []uint16      `json:"capabilities"`
	Transport    TransportInfo `json:"transport"`
}

func (c *Client) FeatureDevice(ctx context.Context) (FeatureDevice, error) {
	var d FeatureDevice
	id, _, err := c.Model(ctx)
	if err != nil {
		return d, err
	}
	d.ModelID = &id
	d.Capabilities, err = c.Capabilities(ctx)
	d.Transport = c.TransportInfo()
	return d, err
}

type FeatureContext struct {
	Device           *FeatureDevice `json:"device"`
	WritesAllowed    bool           `json:"writes_allowed"`
	BridgeConfigured bool           `json:"bridge_configured"`
	BridgeMedium     string         `json:"bridge_medium,omitempty"`
}
type FeatureReason struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Commands []Command `json:"commands,omitempty"`
}
type FeatureAvailability struct {
	ID               string          `json:"id"`
	State            string          `json:"state"` // available, unavailable, unknown
	Reasons          []FeatureReason `json:"reasons"`
	RequiredCommands []Command       `json:"required_commands"`
}

// FeatureRequest resolves requirements for selected parameters without I/O.
// Not all CLI workflows have resolvers; unresolved parameterized entries remain unknown.
type FeatureRequest struct {
	SettingsPatch     *SettingsPatch `json:"settings_patch,omitempty"`
	SlotEdit          *SlotEdit      `json:"slot_edit,omitempty"`
	Card              *GUICard       `json:"-"`
	Slot              int            `json:"slot"`
	Persist           bool           `json:"persist"`
	Activate          bool           `json:"activate"`
	Command           *Command       `json:"command"`
	AllowUnregistered bool           `json:"allow_unregistered"`
	Model             string         `json:"model"`
	Version           string         `json:"version"`
}

func uniqueCommands(v []Command) []Command {
	set := map[Command]bool{}
	for _, c := range v {
		set[c] = true
	}
	out := make([]Command, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func featureEntry(f FeatureDefinition, surface string) (FeatureEntry, error) {
	switch surface {
	case "web":
		return f.Web, nil
	case "cli":
		return f.CLI, nil
	case "sdk":
		return f.SDK, nil
	}
	return FeatureEntry{}, fmt.Errorf("unknown feature surface %q", surface)
}

// EvaluateFeature is advisory: it never probes a device, authorizes a request or
// replaces the SDK's parameter checks, transport guards and write verification.
func EvaluateFeature(id, surface string, context FeatureContext, request *FeatureRequest) (FeatureAvailability, error) {
	for _, f := range Features() {
		if f.ID == id {
			return evaluateFeature(f, surface, context, request)
		}
	}
	return FeatureAvailability{}, fmt.Errorf("unknown feature %q", id)
}
func EvaluateFeatures(surface string, context FeatureContext) ([]FeatureAvailability, error) {
	out := []FeatureAvailability{}
	for _, f := range Features() {
		v, e := evaluateFeature(f, surface, context, nil)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func evaluateFeature(f FeatureDefinition, surface string, ctx FeatureContext, request *FeatureRequest) (FeatureAvailability, error) {
	a := FeatureAvailability{ID: f.ID, State: "available", Reasons: []FeatureReason{}, RequiredCommands: uniqueCommands(f.Commands)}
	entry, err := featureEntry(f, surface)
	if err != nil {
		return a, err
	}
	reason := func(state, code, message string, commands ...Command) {
		if state == "unavailable" || a.State == "available" {
			a.State = state
		}
		a.Reasons = append(a.Reasons, FeatureReason{code, message, commands})
	}
	if entry.Status == "missing" || entry.Status == "removed" {
		reason("unavailable", "no_entry", "This surface has no supported entry for this feature")
		return a, nil
	}

	if f.RequirementMode == "bridge" {
		medium := ctx.BridgeMedium
		if medium == "" {
			medium = "ble"
		} // Preserve existing BLE evaluator callers.
		expected := "ble"
		if f.Transport == "managed-usb" {
			expected = "usb-serial"
		}
		if !ctx.BridgeConfigured || medium != expected {
			reason("unavailable", "bridge_required", "Configure the matching managed device bridge first")
		}
	}
	unresolved := f.RequirementMode == "parameterized" || f.RequirementMode == "card-plan" || f.RequirementMode == "command" || f.RequirementMode == "external"
	if request != nil {
		switch f.RequirementMode {
		case "card-plan":
			if request.Card != nil {
				cmds, e := GUICardLoadRequirements(*request.Card, request.Slot, GUICardLoadOptions{Persist: request.Persist, Activate: request.Activate})
				if e != nil {
					reason("unavailable", "invalid_parameters", e.Error())
				} else {
					a.RequiredCommands = cmds
					unresolved = false
				}
			}
		case "command":
			if request.Command != nil {
				m, ok := LookupCommand(*request.Command)
				if !ok || (!m.Registered && !request.AllowUnregistered) {
					reason("unavailable", "unregistered_command", "Select a registered command or an explicit compatibility override")
				} else {
					a.RequiredCommands = []Command{*request.Command}
					f.Write = m.Effect != "read" && m.Effect != "rf-read"
					unresolved = false
					if ctx.Device != nil {
						transport := ctx.Device.Transport
						if transport.Connection != "serial" || transport.Medium != "serial" {
							transport.AllowsPairingChanges = false
						}
						if e := checkTransportCommand(transport, uint16(*request.Command)); e != nil {
							code := "transport_restricted"
							if errors.Is(e, ErrUSBBridgeRequired) {
								code = "usb_bridge_required"
							}
							reason("unavailable", code, e.Error())
						}
					}
				}
			}
		}

		if f.ID == "device.configure" && request.SettingsPatch != nil {
			cmds, e := SettingsRequirements(*request.SettingsPatch, request.Persist)
			if e != nil {
				reason("unavailable", "invalid_parameters", e.Error())
			} else {
				a.RequiredCommands = cmds
				unresolved = false
			}
			if request.SettingsPatch.BLEKey != nil || request.SettingsPatch.BLEPairing != nil {
				f.Transport = "pairing"
			}
		}
		if f.ID == "slot.configure" && request.SlotEdit != nil {
			cmds, e := request.SlotEdit.Requirements()
			if e != nil {
				reason("unavailable", "invalid_parameters", e.Error())
			} else {
				a.RequiredCommands = cmds
				unresolved = false
			}
		}
		if f.ID == "firmware.check" && request.Model != "" && request.Version != "" {
			if _, e := FirmwareModel(request.Model); e != nil {
				reason("unavailable", "invalid_parameters", e.Error())
			} else {
				f.Device = false
				a.RequiredCommands = []Command{}
				unresolved = false
			}
		}
	}
	if f.Write && !ctx.WritesAllowed {
		reason("unavailable", "writes_disabled", "Device/card writes are not enabled")
	}
	if f.Device {
		if ctx.Device == nil {
			reason("unknown", "device_unknown", "Read device information or feature status first")
		} else {
			model := "unknown"
			if ctx.Device.ModelID != nil && *ctx.Device.ModelID == 0 {
				model = "ultra"
			} else if ctx.Device.ModelID != nil && *ctx.Device.ModelID == 1 {
				model = "lite"
			}
			allowed := false
			for _, v := range f.Models {
				if v == model || v == "any" {
					allowed = true
				}
			}
			if !allowed {
				if ctx.Device.ModelID == nil {
					reason("unknown", "model_unknown", "The device model is unknown")
				} else {
					reason("unavailable", "unsupported_model", "The observed device model is not supported by this workflow")
				}
			}
			if ctx.Device.Capabilities == nil {
				reason("unknown", "capabilities_unknown", "The device command list is unknown")
			} else {
				available := map[Command]bool{}
				for _, c := range ctx.Device.Capabilities {
					available[Command(c)] = true
				}
				missing := []Command{}
				for _, c := range a.RequiredCommands {
					if !available[c] {
						missing = append(missing, c)
					}
				}
				if len(missing) > 0 {
					reason("unavailable", "missing_commands", "The device does not advertise required commands", missing...)
				}
			}
		}
	}
	if f.RequirementMode != "command" && ctx.Device != nil && ctx.Device.Transport.Connection == "tcp" && ctx.Device.Transport.Medium == "usb-serial" {
		for _, cmd := range a.RequiredCommands {
			if cmd != CmdEnterBootloader {
				continue
			}
			if err := checkTransportCommand(ctx.Device.Transport, uint16(cmd)); err != nil {
				reason("unavailable", "transport_restricted", err.Error())
			}
		}
	}
	if ctx.Device != nil && ctx.Device.Transport.NativeUSBMaintenance && f.Device && f.RequirementMode != "command" {
		allowed := false
		switch f.ID {
		case "device.features", "device.settings", "device.pairing", "device.reset", "backup.restore", "firmware.enter":
			allowed = true
		case "device.configure":
			allowed = request == nil || request.SettingsPatch == nil || request.SettingsPatch.PairingOnly()
		}
		if !allowed {
			reason("unavailable", "usb_bridge_required", ErrUSBBridgeRequired.Error())
		}
	}
	if f.Transport == "pairing" || f.Transport == "restore" || f.Transport == "usb-dfu" {
		if ctx.Device == nil || ctx.Device.Transport.Connection == "" {
			reason("unknown", "transport_unknown", "The underlying transport is unknown")
		} else {
			t := ctx.Device.Transport
			allowed := t.Connection == "serial" && t.Medium == "serial"
			if f.Transport == "pairing" {
				allowed = allowed && t.AllowsPairingChanges
			}
			if f.Transport == "restore" {
				allowed = allowed && t.AllowsDeviceRestore
			}
			if f.Transport == "usb-dfu" {
				allowed = allowed && t.SupportsDFU
			}
			if !allowed {
				reason("unavailable", "transport_restricted", "This operation requires a direct serial connection; TCP cannot grant serial permissions")
			}
		}
	}
	if unresolved {
		reason("unknown", "parameters_required", "Selected parameters or an external runtime are required; conditional command sets are not all required")
	}
	return a, nil
}
