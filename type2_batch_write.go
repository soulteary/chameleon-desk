package chameleon

import (
	"bytes"
	"context"
	"fmt"
)

type Type2PageChange struct {
	Page     int      `json:"page"`
	Expected HexBytes `json:"expected"`
	Data     HexBytes `json:"data"`
}
type Type2BatchWrite struct {
	UID             HexBytes          `json:"uid"`
	ExpectedVersion HexBytes          `json:"expected_version,omitempty"`
	Password        HexBytes          `json:"password,omitempty"`
	Pages           []Type2PageChange `json:"pages"`
}

func (r Type2BatchWrite) Requirements() ([]Command, error) {
	if len(r.Pages) < 1 || len(r.Pages) > 32 {
		return nil, fmt.Errorf("multi-page writes require 1..32 explicit ordinary pages")
	}
	if len(r.ExpectedVersion) != 0 && len(r.ExpectedVersion) != 8 {
		return nil, fmt.Errorf("expected version must contain eight bytes")
	}
	if len(r.ExpectedVersion) > 0 {
		if _, err := type2LayoutForVersion(r.ExpectedVersion); err != nil {
			return nil, err
		}
	}
	last := -1
	for _, p := range r.Pages {
		if p.Page <= last {
			return nil, fmt.Errorf("page addresses must be unique and ascending")
		}
		last = p.Page
		if _, err := (Type2PageWrite{UID: r.UID, ExpectedVersion: r.ExpectedVersion, Password: r.Password, Page: p.Page, Expected: p.Expected, Data: p.Data}).Requirements(); err != nil {
			return nil, err
		}
	}
	return (Type2PageWrite{UID: r.UID, ExpectedVersion: r.ExpectedVersion, Password: r.Password, Page: r.Pages[0].Page, Expected: r.Pages[0].Expected, Data: r.Pages[0].Data}).Requirements()
}

type Type2BatchPlan struct {
	Schema  int               `json:"schema"`
	UID     HexBytes          `json:"uid"`
	Version HexBytes          `json:"version"`
	Model   string            `json:"model"`
	Pages   []Type2PageChange `json:"pages"`
	Scope   string            `json:"scope"`
}
type Type2BatchPageResult struct {
	Page    int                   `json:"page"`
	Outcome string                `json:"outcome"`
	Result  *Type2PageWriteResult `json:"result,omitempty"`
}
type Type2BatchWriteResult struct {
	Schema        int                    `json:"schema"`
	Complete      bool                   `json:"complete"`
	Outcome       string                 `json:"outcome"`
	VerifiedPages int                    `json:"verified_pages"`
	Pages         []Type2BatchPageResult `json:"pages"`
}

func preflightType2Pages(ctx context.Context, s *ReaderSession, r Type2BatchWrite) (Type2BatchPlan, error) {
	out := Type2BatchPlan{Schema: 1, UID: append(HexBytes(nil), r.UID...), Pages: r.Pages, Scope: "ordinary_user_pages_only"}
	for _, p := range r.Pages {
		result, err := type2UserPageSession(ctx, s, Type2PageWrite{UID: r.UID, ExpectedVersion: r.ExpectedVersion, Password: r.Password, Page: p.Page, Expected: p.Expected, Data: p.Data}, nil, true)
		if err != nil {
			return out, err
		}
		if len(out.Version) == 0 {
			out.Version = append(HexBytes(nil), result.Version...)
			out.Model = result.Model
		}
		if !bytes.Equal(out.Version, result.Version) || len(r.ExpectedVersion) > 0 && !bytes.Equal(r.ExpectedVersion, result.Version) {
			return out, fmt.Errorf("Type2 model differs from the planned version")
		}
	}
	return out, nil
}

// PlanType2UserPages checks every expected page before allowing execution. It
// never changes memory. Mode/authentication and cleanup retain Reader semantics.
func (c *Client) PlanType2UserPages(ctx context.Context, r Type2BatchWrite) (out Type2BatchPlan, err error) {
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	err = c.Reader(ctx, func(s *ReaderSession) error { var e error; out, e = preflightType2Pages(ctx, s, r); return e })
	return
}

// WriteType2UserPagesObserved holds one Reader gate for all preflight and writes.
// A failed attempt stops the sequence. There is no replay, automatic rollback or
// resume; verified prefixes and unattempted suffixes remain distinguishable.
func (c *Client) WriteType2UserPagesObserved(ctx context.Context, r Type2BatchWrite, beforeWrite func()) (out Type2BatchWriteResult, err error) {
	out = Type2BatchWriteResult{Schema: 1, Outcome: "write_not_started", Pages: make([]Type2BatchPageResult, len(r.Pages))}
	for i, p := range r.Pages {
		out.Pages[i] = Type2BatchPageResult{Page: p.Page, Outcome: "not_started"}
	}
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	if len(r.ExpectedVersion) != 8 {
		return out, fmt.Errorf("execution requires the eight-byte version from the reviewed plan")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		if _, err := preflightType2Pages(ctx, s, r); err != nil {
			return err
		}
		for i, p := range r.Pages {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, e := type2UserPageSession(ctx, s, Type2PageWrite{UID: r.UID, ExpectedVersion: r.ExpectedVersion, Password: r.Password, Page: p.Page, Expected: p.Expected, Data: p.Data}, beforeWrite, false)
			out.Pages[i].Result = &result
			if result.WriteAttempted {
				out.Outcome = "write_outcome_unknown"
				out.Pages[i].Outcome = "write_outcome_unknown"
			}
			if e != nil {
				return e
			}
			if !bytes.Equal(result.Version, r.ExpectedVersion) {
				return fmt.Errorf("Type2 model changed during page execution")
			}
			out.Pages[i].Outcome = "verified"
			out.VerifiedPages++
			if result.Unchanged {
				out.Pages[i].Outcome = "unchanged"
			}
			out.Outcome = "verified_prefix_interrupted"
		}
		return nil
	})
	out.Complete = err == nil
	if out.Complete {
		out.Outcome = "verified"
	}
	return
}
