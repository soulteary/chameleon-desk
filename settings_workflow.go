package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
)

type SettingsPatch struct {
	Animation    *byte   `json:"animation,omitempty"`
	SleepSeconds *byte   `json:"sleep_seconds,omitempty"`
	LongPressMS  *uint16 `json:"long_press_ms,omitempty"`
	ShortA       *byte   `json:"short_a,omitempty"`
	ShortB       *byte   `json:"short_b,omitempty"`
	LongA        *byte   `json:"long_a,omitempty"`
	LongB        *byte   `json:"long_b,omitempty"`
	BLEPairing   *bool   `json:"ble_pairing,omitempty"`
	BLEKey       *string `json:"ble_key,omitempty"`
}

func (p SettingsPatch) Validate() error {
	if p.Animation != nil && *p.Animation > 3 {
		return fmt.Errorf("animation must be 0..3")
	}
	for _, v := range []*byte{p.ShortA, p.ShortB, p.LongA, p.LongB} {
		if v != nil && *v > 5 {
			return fmt.Errorf("button action must be 0..5")
		}
	}
	if p.BLEKey != nil {
		if len(*p.BLEKey) != 6 {
			return fmt.Errorf("BLE PIN must have six digits")
		}
		for _, v := range *p.BLEKey {
			if v < '0' || v > '9' {
				return fmt.Errorf("BLE PIN must have six digits")
			}
		}
	}
	return nil
}

// ConfigureSettings preflights all requested getters before any setting write,
// serializes the update, verifies each result, then optionally saves settings.
// A connection loss can leave earlier updates applied; no blind write retries.
func (c *Client) ConfigureSettings(ctx context.Context, p SettingsPatch, persist bool) (out SettingsView, err error) {
	if (p.BLEKey != nil || p.BLEPairing != nil) && !c.transportInfo.AllowsPairingChanges {
		return out, fmt.Errorf("pairing changes require a direct serial connection")
	}
	if e := p.Validate(); e != nil {
		return out, e
	}
	if e := c.acquire(ctx); e != nil {
		return out, e
	}
	defer c.release()
	type operation struct {
		set, get       Command
		request, value []byte
	}
	ops := []operation{}
	addByte := func(v *byte, set, get Command) {
		if v != nil {
			ops = append(ops, operation{set, get, nil, []byte{*v}})
		}
	}
	addByte(p.Animation, 1015, 1016)
	addByte(p.SleepSeconds, 1040, 1039)
	if p.LongPressMS != nil {
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, *p.LongPressMS)
		ops = append(ops, operation{1042, 1041, nil, b})
	}
	for i, v := range []*byte{p.ShortA, p.ShortB, p.LongA, p.LongB} {
		if v == nil {
			continue
		}
		set, get := CmdSetButtonPressConfig, CmdGetButtonPressConfig
		if i >= 2 {
			set, get = CmdSetLongButtonPressConfig, CmdGetLongButtonPressConfig
		}
		ops = append(ops, operation{set, get, []byte{byte('A' + i%2)}, []byte{*v}})
	}
	if p.BLEKey != nil {
		ops = append(ops, operation{1030, 1031, nil, []byte(*p.BLEKey)})
	}
	if p.BLEPairing != nil {
		ops = append(ops, operation{1037, 1036, nil, []byte{boolean(*p.BLEPairing)}})
	}
	for _, op := range ops {
		b, e := c.exchange(ctx, uint16(op.get), op.request, 0x68)
		if e != nil {
			return out, e
		}
		if len(b) != len(op.value) {
			return out, fmt.Errorf("invalid setting getter length before writes: %s", op.get)
		}
	}
	for _, op := range ops {
		payload := append(append([]byte(nil), op.request...), op.value...)
		if _, e := c.exchange(ctx, uint16(op.set), payload, 0x68); e != nil {
			return out, e
		}
		b, e := c.exchange(ctx, uint16(op.get), op.request, 0x68)
		if e != nil {
			return out, e
		}
		if !bytes.Equal(b, op.value) {
			return out, fmt.Errorf("setting readback mismatch at %s", op.set)
		}
	}
	if persist {
		if _, e := c.exchange(ctx, uint16(CmdSaveSettings), nil, 0x68); e != nil {
			return out, e
		}
	}
	return c.settingsView(ctx, false)
}
