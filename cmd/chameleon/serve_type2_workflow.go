package main

import (
	"context"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) handleType2WorkflowRead(w http.ResponseWriter, r *http.Request) {
	var input chameleon.Type2WorkflowRead
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.type2-workflow", &chameleon.FeatureRequest{Type2Workflow: &input}); err != nil {
			return nil, err
		}
		return c.ReadType2Workflow(ctx, input)
	})
}

type type2PhysicalWriteBody struct {
	Request      chameleon.Type2PageWrite `json:"request"`
	Confirmation string                   `json:"confirmation,omitempty"`
	Confirm      bool                     `json:"confirm"`
}

func (ws *webServer) handleType2WritePlan(w http.ResponseWriter, r *http.Request) {
	var body type2PhysicalWriteBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Confirmation != "" || body.Confirm {
		writeErr(w, 400, fmt.Errorf("plan requests must not contain an execution confirmation"))
		return
	}
	if _, err := body.Request.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		probe := chameleon.Type2WorkflowRead{UID: body.Request.UID, Start: body.Request.Page, Count: 1, Password: body.Request.Password}
		if err := ws.featurePreflight(ctx, c, "reader.type2-workflow", &chameleon.FeatureRequest{Type2Workflow: &probe}); err != nil {
			return nil, err
		}
		plan, err := c.PlanType2UserPage(ctx, body.Request)
		if err != nil {
			return nil, err
		}
		token, err := ws.physicalConfirmation("type2", body.Request)
		if err != nil {
			return nil, err
		}
		return map[string]any{"schema": 1, "plan": plan, "confirmation": token}, nil
	})
}

func (ws *webServer) handleType2UserPageWrite(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var body type2PhysicalWriteBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := body.Request.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !body.Confirm || body.Confirmation == "" {
		writeErr(w, 400, fmt.Errorf("review the Type2 page-write plan and confirm its execution"))
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		markConfigurationWrite(ctx, ws, false)
		if err := ws.verifyPhysicalConfirmation("type2", body.Request, body.Confirmation); err != nil {
			return nil, err
		}
		if err := ws.featurePreflight(ctx, c, "reader.type2-write", &chameleon.FeatureRequest{Type2Write: &body.Request}); err != nil {
			return nil, err
		}
		return c.WriteType2UserPageObserved(ctx, body.Request, func() { markConfigurationWrite(ctx, ws, true) })
	})
}
