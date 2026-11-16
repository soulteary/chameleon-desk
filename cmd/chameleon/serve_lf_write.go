package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

type lfPhysicalWriteBody struct {
	Request      chameleon.LFPhysicalWriteRequest `json:"request"`
	Confirmation string                           `json:"confirmation,omitempty"`
}
type lfPhysicalWriteResult struct {
	Schema            int                 `json:"schema"`
	Acknowledged      bool                `json:"acknowledged"`
	Verified          bool                `json:"verified"`
	Outcome           string              `json:"outcome"`
	VerificationScope string              `json:"verification_scope"`
	PasswordVerified  bool                `json:"password_verified"`
	TargetUIDBound    bool                `json:"target_uid_bound"`
	Record            *chameleon.LFRecord `json:"record,omitempty"`
	Observed          *chameleon.LFRecord `json:"observed,omitempty"`
}

func (ws *webServer) handleLFWritePlan(w http.ResponseWriter, r *http.Request) {
	var body lfPhysicalWriteBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Confirmation != "" {
		writeErr(w, 400, fmt.Errorf("plan requests must not contain an execution confirmation"))
		return
	}
	plan, err := body.Request.Plan()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		token, err := ws.physicalConfirmation("lf", body.Request)
		if err != nil {
			return nil, err
		}
		return map[string]any{"schema": 1, "plan": plan, "confirmation": token}, nil
	})
}
func (ws *webServer) handleLFWrite(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var body lfPhysicalWriteBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	plan, err := body.Request.Plan()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Confirmation == "" {
		writeErr(w, 400, fmt.Errorf("review the LF write plan and supply its confirmation"))
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		markConfigurationWrite(ctx, ws, false)
		out := lfPhysicalWriteResult{Schema: 1, Outcome: "write_not_started", VerificationScope: "selected_protocol_identity_only"}
		if err := ws.verifyPhysicalConfirmation("lf", body.Request, body.Confirmation); err != nil {
			return out, err
		}
		if err := ws.featurePreflight(ctx, c, "lf.clone", &chameleon.FeatureRequest{LFWrite: &body.Request}); err != nil {
			return out, err
		}
		input, err := body.Request.IdentityInput()
		if err != nil {
			return out, err
		}
		if input.IOProx != nil {
			validated, e := c.EncodeLFIdentity(ctx, input)
			if e != nil {
				return out, e
			}
			if !bytes.Equal(validated.ID, plan.Record.ID) {
				return out, fmt.Errorf("ioProx firmware encoding differs from the source identity")
			}
			// The decoder has proved the supplied encoded identity. CloneLF can now use
			// that exact encoding without a second decoder round trip before the write.
			input.IOProx = nil
			input.ID = validated.ID
		}
		passwords := make([][]byte, len(body.Request.OldPasswords))
		for i, password := range body.Request.OldPasswords {
			passwords[i] = append([]byte(nil), password...)
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		record, err := c.CloneLFObserved(ctx, input, body.Request.NewPassword, passwords, func() {
			markConfigurationWrite(ctx, ws, true)
			out.Outcome = "write_outcome_unknown"
		})
		out.Record = &record
		if err != nil {
			return out, err
		}
		out.Acknowledged = true
		observed, err := c.ReadLF(ctx, plan.Readback)
		if err != nil {
			return out, fmt.Errorf("LF write acknowledgement received but readback did not confirm the identity: %w", err)
		}
		out.Observed = &observed
		if !bytes.Equal(observed.ID, record.ID) || observed.Protocol != plan.Readback.Protocol {
			return out, fmt.Errorf("LF write readback differs from the planned identity; do not repeat the write without inspecting the target")
		}
		if plan.Readback.Protocol == "em410x" && !sameEM410xIdentityType(record.TagType, observed.TagType) {
			return out, fmt.Errorf("LF write readback differs from the selected EM410x identity family")
		}
		out.Verified = true
		out.Outcome = "identity_verified"
		return out, nil
	})
}

// Reader clock/format subtypes 100..103 share the five-byte EM410x identity.
// Electra remains a distinct thirteen-byte identity and must never cross-match.
func sameEM410xIdentityType(expected, observed uint16) bool {
	if expected >= 100 && expected <= 103 {
		return observed >= 100 && observed <= 103
	}
	return expected == 104 && observed == 104
}
