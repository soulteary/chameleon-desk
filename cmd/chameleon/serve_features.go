package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) featureContext(device *chameleon.FeatureDevice) chameleon.FeatureContext {
	if ws.nativePort != "" {
		copy := chameleon.FeatureDevice{}
		if device != nil {
			copy = *device
		}
		copy.Transport = chameleon.TransportInfo{Connection: "serial", Medium: "serial", NativeUSBMaintenance: true, AllowsPairingChanges: true, AllowsDeviceRestore: true, SupportsDFU: true}
		device = &copy
	}
	return chameleon.FeatureContext{Device: device, WritesAllowed: ws.allowWrites, BridgeConfigured: ws.bridge != nil, BridgeMedium: ws.bridgeMedium()}
}
func (ws *webServer) featureStates(device *chameleon.FeatureDevice) []chameleon.FeatureAvailability {
	states, _ := chameleon.EvaluateFeatures("web", ws.featureContext(device))
	return states
}
func (ws *webServer) handleFeatureDevice(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		d, e := c.FeatureDevice(ctx)
		if e != nil {
			return nil, e
		}
		return map[string]any{"schema_version": chameleon.FeatureSchemaVersion, "device": d, "states": ws.featureStates(&d)}, nil
	})
}

// Feature checks are offline previews based on submitted observations. They are
// not authorization and never replace the actual device-operation guards.
func (ws *webServer) handleFeatureCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		SlotRead          *chameleon.SlotReadRequest               `json:"slot_read"`
		DeviceMode        *byte                                    `json:"device_mode"`
		LFIdentity        *chameleon.LFIdentityInput               `json:"lf_identity"`
		LFWrite           *chameleon.LFPhysicalWriteRequest        `json:"lf_write"`
		Type2Workflow     *chameleon.Type2WorkflowRead             `json:"type2_workflow"`
		Type2Write        *chameleon.Type2PageWrite                `json:"type2_write"`
		Type2Promotion    *chameleon.Type2PromotionOptions         `json:"type2_promotion"`
		ClassicDictionary *chameleon.ClassicDictionaryRequest      `json:"classic_dictionary"`
		ClassicRepair     *chameleon.ClassicRepairRequest          `json:"classic_repair"`
		ClassicVerify     *chameleon.ClassicKeyVerificationRequest `json:"classic_verify"`
		ClassicRecovery   *chameleon.ClassicRecoveryRequest        `json:"classic_recovery"`
		ClassicWrite      *chameleon.ClassicPhysicalWriteRequest   `json:"classic_write"`
		OtherCard         json.RawMessage                          `json:"other_card"`
		MergeOptions      *chameleon.ClassicMergeOptions           `json:"merge_options"`
		ClassicRead       *chameleon.ClassicRead                   `json:"classic_read"`
		Type2Read         *chameleon.Type2Read                     `json:"type2_read"`
		LFRead            *chameleon.LFRead                        `json:"lf_read"`
		SettingsPatch     *chameleon.SettingsPatch                 `json:"settings_patch"`
		SlotEdit          *chameleon.SlotEdit                      `json:"slot_edit"`
		ID                string                                   `json:"id"`
		Device            *chameleon.FeatureDevice                 `json:"device"`
		Card              json.RawMessage                          `json:"card"`
		Slot              int                                      `json:"slot"`
		Persist           bool                                     `json:"persist"`
		Activate          bool                                     `json:"activate"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	request := chameleon.FeatureRequest{SlotRead: body.SlotRead, DeviceMode: body.DeviceMode, Slot: body.Slot, Persist: body.Persist, Activate: body.Activate, SettingsPatch: body.SettingsPatch, SlotEdit: body.SlotEdit}
	request.ClassicRead, request.Type2Read, request.LFRead = body.ClassicRead, body.Type2Read, body.LFRead
	request.LFWrite = body.LFWrite
	request.LFIdentity = body.LFIdentity
	request.ClassicDictionary = body.ClassicDictionary
	request.Type2Workflow, request.Type2Write, request.Type2Promotion = body.Type2Workflow, body.Type2Write, body.Type2Promotion
	request.ClassicRepair, request.ClassicVerify, request.ClassicRecovery, request.ClassicWrite = body.ClassicRepair, body.ClassicVerify, body.ClassicRecovery, body.ClassicWrite
	request.MergeOptions = body.MergeOptions
	var result chameleon.FeatureAvailability
	var err error
	switch body.ID {
	case "slot.read-current":
		if body.SlotRead == nil {
			err = fmt.Errorf("slot_read is required")
		}
	case "reader.classic-keyed":
		if body.ClassicRead == nil {
			err = fmt.Errorf("classic_read is required")
		}
	case "reader.type2-range":
		if body.Type2Read == nil {
			err = fmt.Errorf("type2_read is required")
		}
	case "reader.lf":
		if body.LFRead == nil {
			err = fmt.Errorf("lf_read is required")
		}
	case "slot.load-card", "library.promote-slot", "library.promote-classic", "library.promote-lf", "library.promote-type2", "library.merge-classic":
		var card chameleon.GUICard
		card, err = chameleon.ParseGUICard(body.Card)
		if err == nil {
			request.Card = &card
		}
	case "device.configure":
		if body.SettingsPatch == nil {
			err = fmt.Errorf("settings_patch is required")
		}
	case "slot.configure":
		if body.SlotEdit == nil {
			err = fmt.Errorf("slot_edit is required")
		}
	case "library.edit-lf-device":
		if body.LFIdentity == nil {
			err = fmt.Errorf("lf_identity required")
		}
	case "lf.clone":
		if body.LFWrite == nil {
			err = fmt.Errorf("lf_write required")
		}
	case "reader.type2-workflow":
		if body.Type2Workflow == nil {
			err = fmt.Errorf("type2_workflow required")
		}
	case "reader.type2-write":
		if body.Type2Write == nil {
			err = fmt.Errorf("type2_write required")
		}
	case "reader.classic-dictionary":
		if body.ClassicDictionary == nil {
			err = fmt.Errorf("classic_dictionary required")
		}
	case "reader.classic-repair":
		if body.ClassicRepair == nil {
			err = fmt.Errorf("classic_repair required")
		}
	case "reader.classic-verify":
		if body.ClassicVerify == nil {
			err = fmt.Errorf("classic_verify required")
		}
	case "reader.classic-recover":
		if body.ClassicRecovery == nil {
			err = fmt.Errorf("classic_recovery required")
		} else {
			err = chameleon.ValidateClassicRecoveryHelpers(ws.recoveryTools, body.ClassicRecovery.Strategy)
		}
	case "reader.classic-write":
		if body.ClassicWrite == nil {
			err = fmt.Errorf("classic_write required")
		}
	default:
		writeErr(w, 400, fmt.Errorf("unsupported parameter preview"))
		return
	}
	if err == nil && body.ID == "library.merge-classic" {
		var other chameleon.GUICard
		other, err = chameleon.ParseGUICard(body.OtherCard)
		if err == nil {
			request.OtherCard = &other
		}
	}
	if err == nil {
		result, err = chameleon.EvaluateFeature(body.ID, "web", ws.featureContext(body.Device), &request)
	}
	if err != nil {
		result = chameleon.FeatureAvailability{ID: body.ID, State: "unavailable", Reasons: []chameleon.FeatureReason{{Code: "invalid_parameters", Message: err.Error()}}, RequiredCommands: []chameleon.Command{}}
	}
	writeJSON(w, map[string]any{"schema_version": chameleon.FeatureSchemaVersion, "state": result})
}
