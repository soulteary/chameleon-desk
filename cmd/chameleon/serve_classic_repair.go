package main

import (
	"context"
	"errors"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
)

var errClassicWriteConfirmation = errors.New("explicit confirmation and current plan digest are required")

func (ws *webServer) handleClassicRepair(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicRepairRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.classic-repair", &chameleon.FeatureRequest{ClassicRepair: &input}); err != nil {
			return nil, err
		}
		if len(input.Blocks) != 0 {
			return c.ReadClassicSelectedRepair(ctx, input)
		}
		return c.ReadClassicRepair(ctx, input)
	})
}
func (ws *webServer) handleClassicVerify(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicKeyVerificationRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.classic-verify", &chameleon.FeatureRequest{ClassicVerify: &input}); err != nil {
			return nil, err
		}
		return c.VerifyClassicKeys(ctx, input)
	})
}
func (ws *webServer) handleClassicRecover(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicRecoveryRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := chameleon.ValidateClassicRecoveryHelpers(ws.recoveryTools, input.Strategy); err != nil {
		writeErr(w, 422, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.classic-recover", &chameleon.FeatureRequest{ClassicRecovery: &input}); err != nil {
			return nil, err
		}
		return c.RecoverClassicBound(ctx, input, ws.recoveryTools)
	})
}
func classicWriteConfirmationPayload(input chameleon.ClassicPhysicalWriteRequest) chameleon.ClassicPhysicalWriteRequest {
	input.Confirm = false
	input.PlanDigest = ""
	return input
}
func (ws *webServer) handleClassicWritePlan(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicPhysicalWriteRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	plan, err := chameleon.PlanClassicPhysicalWrite(input)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	confirmation, err := ws.physicalConfirmation("classic", classicWriteConfirmationPayload(input))
	if err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, map[string]any{"plan": plan, "confirmation": confirmation})
}
func (ws *webServer) handleClassicWrite(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var input struct {
		Request      chameleon.ClassicPhysicalWriteRequest `json:"request"`
		Confirmation string                                `json:"confirmation"`
	}
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	plan, err := chameleon.PlanClassicPhysicalWrite(input.Request)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if !input.Request.Confirm || input.Request.PlanDigest != plan.Digest {
		writeErr(w, 400, errClassicWriteConfirmation)
		return
	}
	if err = ws.verifyPhysicalConfirmation("classic", classicWriteConfirmationPayload(input.Request), input.Confirmation); err != nil {
		writeErr(w, 409, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		markConfigurationWrite(ctx, ws, false)
		if err := ws.featurePreflight(ctx, c, "reader.classic-write", &chameleon.FeatureRequest{ClassicWrite: &input.Request}); err != nil {
			return nil, err
		}
		return c.WriteClassicPlanned(ctx, input.Request, func() { markConfigurationWrite(ctx, ws, true) })
	})
}

func (ws *webServer) handleClassicDictionary(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicDictionaryRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.classic-dictionary", &chameleon.FeatureRequest{ClassicDictionary: &input}); err != nil {
			return nil, err
		}
		return c.CheckClassicDictionaryBound(ctx, input)
	})
}
