package chameleon

import (
	"context"
	"encoding/binary"
	"fmt"
)

type SettingsView struct {
	Settings
	LongPressMS *uint16   `json:"long_press_ms,omitempty"`
	Unsupported []Command `json:"unsupported,omitempty"`
}

func (c *Client) SettingsView(ctx context.Context, secrets bool) (out SettingsView, err error) {
	if e := c.acquire(ctx); e != nil {
		return out, e
	}
	defer c.release()
	return c.settingsView(ctx, secrets)
}
func (c *Client) settingsView(ctx context.Context, secrets bool) (out SettingsView, err error) {
	b, e := c.exchange(ctx, 1034, nil, 0x68)
	if e != nil {
		return out, e
	}
	out.Settings, e = parseSettings(b)
	if e != nil {
		return out, e
	}
	if !secrets {
		out.BLEKey = ""
		out.Raw = nil
	}
	for _, cmd := range []Command{1039, 1041} {
		f, e := c.exchangeFrame(ctx, uint16(cmd), nil)
		if e != nil {
			return out, e
		}
		r := Response{Command: cmd, Status: f.status}
		if r.Status == 103 || r.Status == 105 {
			out.Unsupported = append(out.Unsupported, cmd)
			continue
		}
		if e = r.Check(0x68); e != nil {
			return out, e
		}
		if cmd == 1039 {
			if len(f.data) != 1 {
				return out, fmt.Errorf("invalid sleep timeout")
			}
			v := f.data[0]
			out.SleepSeconds = &v
		} else {
			if len(f.data) != 2 {
				return out, fmt.Errorf("invalid long-press threshold")
			}
			v := binary.BigEndian.Uint16(f.data)
			out.LongPressMS = &v
		}
	}
	return out, nil
}
