package chameleon

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type DiagnosticCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type Diagnostics struct {
	Time     string            `json:"time"`
	Upstream string            `json:"upstream_commit"`
	Info     Info              `json:"info"`
	Checks   []DiagnosticCheck `json:"checks"`
	Missing  []CommandInfo     `json:"catalog_commands_not_advertised"`
	Unknown  []uint16          `json:"device_commands_not_in_catalog"`
	Stats    Stats             `json:"transport"`
}

// Diagnose queries state and checks no-card behavior without changing persisted
// data. Reader probes restore the original mode. No writes/DFU/reset are tested.
func (c *Client) Diagnose(ctx context.Context) (out Diagnostics, err error) {
	out.Time = time.Now().Format(time.RFC3339)
	out.Upstream = UpstreamCommit
	out.Info, err = c.Info(ctx)
	if err != nil {
		return out, err
	}
	initial := out.Info.Mode
	caps := map[uint16]bool{}
	for _, v := range out.Info.Capabilities {
		caps[v] = true
		if _, ok := LookupCommand(Command(v)); !ok {
			out.Unknown = append(out.Unknown, v)
		}
	}
	for _, m := range Commands() {
		if m.Registered && !caps[uint16(m.ID)] {
			out.Missing = append(out.Missing, m)
		}
	}
	add := func(name string, e error) {
		detail := "ok"
		if e != nil {
			detail = e.Error()
		}
		out.Checks = append(out.Checks, DiagnosticCheck{name, e == nil, detail})
	}
	settings, e := c.Settings(ctx)
	if e == nil {
		out.Checks = append(out.Checks, DiagnosticCheck{"settings", true, fmt.Sprintf("schema=%d bytes=%d (secrets omitted)", settings.Version, len(settings.Raw))})
	} else {
		add("settings", e)
	}
	if caps[2000] {
		cards, e := c.ScanISO14443A(ctx)
		var restore *ModeRestoreError
		if errors.Is(e, ErrNoCard) && !errors.As(e, &restore) {
			out.Checks = append(out.Checks, DiagnosticCheck{"hf-probe", true, "no card (expected when antenna is empty)"})
		} else if e == nil {
			out.Checks = append(out.Checks, DiagnosticCheck{"hf-probe", true, fmt.Sprintf("%d cards detected", len(cards))})
		} else {
			add("hf-probe", e)
		}
	}
	if caps[3000] {
		card, e := c.ScanLF(ctx, "em410x", 1)
		var restore *ModeRestoreError
		if errors.Is(e, ErrNoCard) && !errors.As(e, &restore) {
			out.Checks = append(out.Checks, DiagnosticCheck{"lf-probe", true, "no card (expected when antenna is empty)"})
		} else if e == nil {
			out.Checks = append(out.Checks, DiagnosticCheck{"lf-probe", true, fmt.Sprintf("%s card detected", card.Protocol)})
		} else {
			add("lf-probe", e)
		}
	}
	after, e := c.Mode(ctx)
	if e == nil && after != initial {
		e = fmt.Errorf("mode changed from %s to %s", initial, after)
	}
	add("mode-restored", e)
	out.Stats = c.Stats()
	for _, check := range out.Checks {
		if !check.OK {
			return out, fmt.Errorf("diagnostic %s: %s", check.Name, check.Detail)
		}
	}
	return out, nil
}
