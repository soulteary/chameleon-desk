package chameleon

import (
	"context"
	"fmt"
)

type DeviceModeChange struct {
	Expected byte `json:"expected"`
	Mode     byte `json:"mode"`
}

func (r DeviceModeChange) Requirements() ([]Command, error) {
	if r.Expected > 1 || r.Mode > 1 {
		return nil, fmt.Errorf("device modes are emulator 0 or reader 1")
	}
	return []Command{CmdGetDeviceMode, CmdChangeDeviceMode}, nil
}
func (c *Client) ChangeDeviceModeObserved(ctx context.Context, r DeviceModeChange, beforeWrite func()) (mode byte, err error) {
	if _, err = r.Requirements(); err != nil {
		return
	}
	if c.transportInfo.NativeUSBMaintenance {
		return 0, ErrUSBBridgeRequired
	}
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	err = c.acquire(queued)
	cancel()
	if err != nil {
		return
	}
	defer c.release()
	before, e := c.exchange(ctx, uint16(CmdGetDeviceMode), nil, 0x68)
	if e != nil {
		return 0, e
	}
	if len(before) != 1 || before[0] != r.Expected {
		return 0, fmt.Errorf("mode changed; refresh before selecting a mode")
	}
	if r.Mode != r.Expected {
		if beforeWrite != nil {
			beforeWrite()
		}
		if _, e = c.exchange(ctx, uint16(CmdChangeDeviceMode), []byte{r.Mode}, 0x68); e != nil {
			return 0, e
		}
	}
	after, e := c.exchange(ctx, uint16(CmdGetDeviceMode), nil, 0x68)
	if e != nil {
		return 0, e
	}
	if len(after) != 1 || after[0] != r.Mode {
		return 0, fmt.Errorf("device mode readback differs")
	}
	return after[0], nil
}
