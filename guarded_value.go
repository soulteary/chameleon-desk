package chameleon

import (
	"bytes"
	"context"
	"fmt"
)

type ClassicValueRequest struct {
	Identity ClassicIdentity `json:"identity"`
	Block    int             `json:"block"`
	KeyType  KeyType         `json:"key_type"`
	Key      HexBytes        `json:"key"`
	Expected HexBytes        `json:"expected"`
	Value    int32           `json:"value"`
	Address  byte            `json:"address"`
}

func (r ClassicValueRequest) Requirements() ([]Command, error) {
	if err := r.Identity.Validate(); err != nil {
		return nil, err
	}
	if _, err := keyPayload(r.Block, r.KeyType, r.Key); err != nil {
		return nil, err
	}
	if r.Block == 0 || r.Block < 128 && r.Block%4 == 3 || r.Block >= 128 && (r.Block-128)%16 == 15 {
		return nil, fmt.Errorf("manufacturer and trailer blocks are excluded from value writes")
	}
	if _, _, err := DecodeClassicValue(r.Expected); err != nil {
		return nil, err
	}
	if *r.Identity.SAK&0x7f != 8 && *r.Identity.SAK&0x7f != 9 && *r.Identity.SAK&0x7f != 0x18 {
		return nil, fmt.Errorf("expected Classic identity required")
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdMf1ReadOneBlock, CmdMf1WriteOneBlock, CmdHf14aSetFieldOff}, nil
}

type ClassicValueResult struct {
	OldValue       int32    `json:"old_value"`
	OldAddress     byte     `json:"old_address"`
	Schema         int      `json:"schema"`
	Block          int      `json:"block"`
	Before         HexBytes `json:"before,omitempty"`
	After          HexBytes `json:"after,omitempty"`
	Value          int32    `json:"value"`
	Address        byte     `json:"address"`
	WriteAttempted bool     `json:"write_attempted"`
	Acknowledged   bool     `json:"acknowledged"`
	Verified       bool     `json:"verified"`
	Outcome        string   `json:"outcome"`
}

func (c *Client) classicValueOperation(ctx context.Context, r ClassicValueRequest, plan bool, beforeWrite func()) (out ClassicValueResult, err error) {
	out = ClassicValueResult{Schema: 1, Block: r.Block, Value: r.Value, Address: r.Address, Outcome: "write_not_started"}
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		identity := r.Identity
		if e = observeClassicIdentity(ctx, s, r.Identity.UID, &identity); e != nil {
			return e
		}
		data, e := s.ReadClassicBlock(ctx, r.Block, r.KeyType, r.Key)
		if e != nil {
			return e
		}
		out.Before = append(HexBytes(nil), data...)
		if !bytes.Equal(data, r.Expected) {
			return fmt.Errorf("value block differs from explicit backup; write not attempted")
		}
		if out.OldValue, out.OldAddress, e = DecodeClassicValue(data); e != nil {
			return e
		}
		if e = observeClassicIdentity(ctx, s, r.Identity.UID, &identity); e != nil {
			return e
		}
		after := EncodeClassicValue(r.Value, r.Address)
		if plan {
			out.After = after
			out.Outcome = "planned"
			return nil
		}
		if !bytes.Equal(after, data) {
			if e = ctx.Err(); e != nil {
				return e
			}
			out.WriteAttempted = true
			out.Outcome = "write_outcome_unknown"
			if beforeWrite != nil {
				beforeWrite()
			}
			if e = s.WriteClassicBlock(ctx, r.Block, r.KeyType, r.Key, after); e != nil {
				return e
			}
			out.Acknowledged = true
		}
		read, e := s.ReadClassicBlock(ctx, r.Block, r.KeyType, r.Key)
		if e != nil {
			return e
		}
		out.After = append(HexBytes(nil), read...)
		if !bytes.Equal(read, after) {
			return fmt.Errorf("value-block readback differs; do not replay")
		}
		if e = observeClassicIdentity(ctx, s, r.Identity.UID, &identity); e != nil {
			return e
		}
		return nil
	})
	if err == nil && !plan {
		out.Verified = true
		out.Outcome = "verified"
	}
	return
}
func (c *Client) PlanClassicValue(ctx context.Context, r ClassicValueRequest) (ClassicValueResult, error) {
	return c.classicValueOperation(ctx, r, true, nil)
}
func (c *Client) WriteClassicValueObserved(ctx context.Context, r ClassicValueRequest, beforeWrite func()) (ClassicValueResult, error) {
	return c.classicValueOperation(ctx, r, false, beforeWrite)
}
