package chameleon

import (
	"bytes"
	"context"
	"fmt"
)

type ReaderConfigUpdate struct {
	Expected ReaderConfig `json:"expected"`
	Patch    ReaderConfig `json:"patch"`
}

func (r ReaderConfigUpdate) Requirements() ([]Command, error) {
	if _, e := r.Expected.payload(); e != nil {
		return nil, e
	}
	if _, e := r.Patch.payload(); e != nil {
		return nil, e
	}
	return []Command{CmdHf14aGetConfig, CmdHf14aSetConfig}, nil
}
func (c *Client) ConfigureReaderObserved(ctx context.Context, r ReaderConfigUpdate, beforeWrite func()) (out ReaderConfig, err error) {
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	if c.transportInfo.NativeUSBMaintenance {
		return out, ErrUSBBridgeRequired
	}
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	err = c.acquire(queued)
	cancel()
	if err != nil {
		return out, err
	}
	defer c.release()
	s := &ReaderSession{c: c}
	defer func() { s.closed = true }()
	old, err := s.expect(ctx, CmdHf14aGetConfig, nil, 0x68)
	if err != nil {
		return out, err
	}
	expected, _ := r.Expected.payload()
	if !bytes.Equal(old, expected) {
		return out, fmt.Errorf("reader settings changed; read them again before updating")
	}
	patch, _ := r.Patch.payload()
	want := append([]byte(nil), patch...)
	for i, v := range want {
		if int8(v) == -1 {
			want[i] = old[i]
		}
	}
	if beforeWrite != nil {
		beforeWrite()
	}
	if _, err = s.expect(ctx, CmdHf14aSetConfig, patch, 0x68); err != nil {
		return out, err
	}
	actual, err := s.expect(ctx, CmdHf14aGetConfig, nil, 0x68)
	if err != nil {
		return out, err
	}
	if !bytes.Equal(actual, want) {
		return out, fmt.Errorf("reader configuration readback differs")
	}
	return ReaderConfig{int8(actual[0]), int8(actual[1]), int8(actual[2]), int8(actual[3])}, nil
}
