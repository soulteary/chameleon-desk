package main

import (
	"context"
	"errors"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

type readerResult = chameleon.ReadResult

func (ws *webServer) handleClassicRead(w http.ResponseWriter, r *http.Request) {
	var input chameleon.ClassicRead
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	keys, err := input.KeyMap()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.classic-keyed", &chameleon.FeatureRequest{ClassicRead: &input}); err != nil {
			return nil, err
		}
		first, count, _ := chameleon.ClassicSectorGeometry(len(keys.Sectors) - 1)
		dump, err := c.DumpClassicWithKeys(ctx, keys)
		return readerResult{Schema: 1, Kind: "classic", Complete: err == nil, Requested: first + count, Read: len(dump.Data) / 16, UID: dump.UID, Dump: &dump}, err
	})
}

func (ws *webServer) handleType2Read(w http.ResponseWriter, r *http.Request) {
	var input chameleon.Type2Read
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.type2-range", &chameleon.FeatureRequest{Type2Read: &input}); err != nil {
			return nil, err
		}
		data, err := c.ReadType2Range(ctx, input)
		return readerResult{Schema: 1, Kind: "type2", Complete: err == nil, Start: input.Start, Requested: input.Count, Read: len(data) / 4, UID: input.UID, Data: data}, err
	})
}

func (ws *webServer) handleLFRead(w http.ResponseWriter, r *http.Request) {
	var input chameleon.LFRead
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "reader.lf", &chameleon.FeatureRequest{LFRead: &input}); err != nil {
			return nil, err
		}
		record, err := c.ReadLF(ctx, input)
		var restore *chameleon.ModeRestoreError
		present := false
		out := readerResult{Schema: 1, Kind: "lf", Complete: err == nil, Requested: 1}
		if errors.Is(err, chameleon.ErrNoCard) && !errors.As(err, &restore) {
			out.Complete = true
			out.Present = &present
			return out, nil
		}
		if err == nil {
			present = true
			out.Present = &present
			out.Read = 1
			out.Record = &record
		}
		return out, err
	})
}
