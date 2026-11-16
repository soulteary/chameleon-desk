package chameleon

import (
	"context"
	"fmt"
)

type MagicInspection struct {
	Family   string          `json:"family"`
	Identity ClassicIdentity `json:"identity"`
	Password HexBytes        `json:"password,omitempty"`
}

func (r MagicInspection) Requirements() ([]Command, error) {
	if err := r.Identity.Validate(); err != nil {
		return nil, err
	}
	if r.Family != "gen1a" && r.Family != "gen3" && r.Family != "gen4" {
		return nil, fmt.Errorf("select gen1a, gen3 or gen4 inspection")
	}
	if r.Family == "gen4" && len(r.Password) != 4 || r.Family != "gen4" && len(r.Password) != 0 {
		return nil, fmt.Errorf("only Gen4 accepts an explicit four-byte password")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdHf14aRaw, CmdHf14aSetFieldOff}, nil
}

type MagicInspectionResult struct {
	Schema            int             `json:"schema"`
	Family            string          `json:"family"`
	Identity          ClassicIdentity `json:"identity"`
	Observed          bool            `json:"observed"`
	ChipAuthenticated bool            `json:"chip_authenticated"`
	Config            *Gen4Config     `json:"config,omitempty"`
	Block0            HexBytes        `json:"block0,omitempty"`
	Notes             []string        `json:"notes"`
}

func (c *Client) InspectMagic(ctx context.Context, r MagicInspection) (out MagicInspectionResult, err error) {
	out = MagicInspectionResult{Schema: 1, Family: r.Family, Identity: r.Identity, Notes: []string{"Inspection sends no memory/configuration write. Backdoor responses are protocol observations, not chip authentication."}}
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		identity := r.Identity
		if e = observeClassicIdentity(ctx, s, r.Identity.UID, &identity); e != nil {
			return e
		}
		switch r.Family {
		case "gen1a":
			g := &gen1aSession{ctx: ctx, s: s}
			out.Block0, e = g.readBlock(0)
		case "gen3":
			out.Notes = append(out.Notes, "A readable block 0 cannot positively identify Gen3; no destructive identification is attempted.")
			out.Block0, e = s.Raw14A(ctx, []byte{0x30, 0}, RawOptions{ActivateField: true, AutoSelect: true, WaitResponse: true, AppendCRC: true, CheckResponseCRC: true, KeepField: true, TimeoutMS: 500})
			if e == nil && len(out.Block0) != 16 {
				e = fmt.Errorf("expected sixteen block-0 bytes")
			}
		case "gen4":
			g := newGen4Session(ctx, s, r.Password)
			cfg, err := g.readConfig()
			e = err
			if err == nil {
				out.Config = &cfg
			}
		}
		if e != nil {
			return e
		}
		if e = observeClassicIdentity(ctx, s, r.Identity.UID, &identity); e != nil {
			return e
		}
		out.Observed = true
		return nil
	})
	return
}
