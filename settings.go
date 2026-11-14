package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

type Sense byte

const (
	SenseLF Sense = 1
	SenseHF Sense = 2
)

func slotByte(slot int) (byte, error) {
	if slot < 1 || slot > 8 {
		return 0, fmt.Errorf("slot must be 1..8")
	}
	return byte(slot - 1), nil
}
func senseByte(s Sense) (byte, error) {
	if s != SenseLF && s != SenseHF {
		return 0, fmt.Errorf("sense must be LF=1 or HF=2")
	}
	return byte(s), nil
}
func boolean(v bool) byte {
	if v {
		return 1
	}
	return 0
}
func (c *Client) set(ctx context.Context, cmd Command, data ...byte) error {
	_, e := c.request(ctx, cmd, data, 0x68)
	return e
}
func (c *Client) SetReaderMode(ctx context.Context, reader bool) error {
	return c.set(ctx, CmdChangeDeviceMode, boolean(reader))
}
func (c *Client) SetActiveSlot(ctx context.Context, slot int) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdSetActiveSlot, s)
}
func (c *Client) SetSlotType(ctx context.Context, slot int, tag uint16) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdSetSlotTagType, s, byte(tag>>8), byte(tag))
}
func (c *Client) InitializeSlot(ctx context.Context, slot int, tag uint16) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdSetSlotDataDefault, s, byte(tag>>8), byte(tag))
}
func (c *Client) EnableSlot(ctx context.Context, slot int, sense Sense, enabled bool) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	f, e := senseByte(sense)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdSetSlotEnable, s, f, boolean(enabled))
}
func (c *Client) DeleteSlot(ctx context.Context, slot int, sense Sense) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	f, e := senseByte(sense)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdDeleteSlotSenseType, s, f)
}
func (c *Client) SlotNickname(ctx context.Context, slot int, sense Sense) (string, error) {
	s, e := slotByte(slot)
	if e != nil {
		return "", e
	}
	f, e := senseByte(sense)
	if e != nil {
		return "", e
	}
	b, e := c.request(ctx, CmdGetSlotTagNick, []byte{s, f}, 0x68)
	if e != nil {
		return "", e
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("invalid UTF-8 nickname")
	}
	return string(b), nil
}
func (c *Client) SetSlotNickname(ctx context.Context, slot int, sense Sense, name string) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	f, e := senseByte(sense)
	if e != nil {
		return e
	}
	if !utf8.ValidString(name) || len(name) > 32 {
		return fmt.Errorf("nickname must be valid UTF-8 of at most 32 bytes")
	}
	return c.set(ctx, CmdSetSlotTagNick, append([]byte{s, f}, []byte(name)...)...)
}
func (c *Client) DeleteSlotNickname(ctx context.Context, slot int, sense Sense) error {
	s, e := slotByte(slot)
	if e != nil {
		return e
	}
	f, e := senseByte(sense)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdDeleteSlotTagNick, s, f)
}

// ClearSlot deletes one sense of a slot and the nickname the firmware leaves
// behind (DELETE_SLOT_SENSE_TYPE keeps it). A slot without a nickname answers
// GET_SLOT_TAG_NICK with FLASH_READ_FAIL, which is not an error here.
func (c *Client) ClearSlot(ctx context.Context, slot int, sense Sense) error {
	if e := c.DeleteSlot(ctx, slot, sense); e != nil {
		return e
	}
	if _, e := c.SlotNickname(ctx, slot, sense); e != nil {
		var status *StatusError
		if errors.As(e, &status) && status.Status == 0x71 {
			return nil
		}
		return e
	}
	return c.DeleteSlotNickname(ctx, slot, sense)
}
func (c *Client) SaveSlotData(ctx context.Context) error  { return c.set(ctx, CmdSlotDataConfigSave) }
func (c *Client) SaveSettings(ctx context.Context) error  { return c.set(ctx, CmdSaveSettings) }
func (c *Client) ResetSettings(ctx context.Context) error { return c.set(ctx, CmdResetSettings) }
func (c *Client) FactoryReset(ctx context.Context) error  { return c.set(ctx, CmdWipeFds) }
func (c *Client) ChipID(ctx context.Context) (HexBytes, error) {
	b, e := c.request(ctx, CmdGetDeviceChipId, nil, 0x68)
	return HexBytes(b), e
}
func (c *Client) Address(ctx context.Context) (HexBytes, error) {
	b, e := c.request(ctx, CmdGetDeviceAddress, nil, 0x68)
	return HexBytes(b), e
}

type Settings struct {
	Version      byte     `json:"version"`
	Animation    byte     `json:"animation"`
	ShortA       byte     `json:"short_a"`
	ShortB       byte     `json:"short_b"`
	LongA        byte     `json:"long_a"`
	LongB        byte     `json:"long_b"`
	BLEPairing   bool     `json:"ble_pairing"`
	BLEKey       string   `json:"ble_key,omitempty"`
	SleepSeconds *byte    `json:"sleep_seconds,omitempty"`
	Raw          HexBytes `json:"raw,omitempty"`
}

func (c *Client) Settings(ctx context.Context) (Settings, error) {
	b, e := c.request(ctx, CmdGetDeviceSettings, nil, 0x68)
	if e != nil {
		return Settings{}, e
	}
	return parseSettings(b)
}
func parseSettings(b []byte) (Settings, error) {
	if len(b) != 13 && len(b) != 14 {
		return Settings{}, fmt.Errorf("settings require 13 or 14 bytes, received %d", len(b))
	}
	if b[0] != 6 && b[0] != 7 {
		return Settings{}, fmt.Errorf("unsupported settings schema %d", b[0])
	}
	if b[6] > 1 {
		return Settings{}, fmt.Errorf("invalid pairing flag")
	}
	var sleep *byte
	if len(b) == 14 {
		v := b[13]
		sleep = &v
	}
	return Settings{b[0], b[1], b[2], b[3], b[4], b[5], b[6] == 1, string(b[7:13]), sleep, HexBytes(b)}, nil
}
func (c *Client) SetAnimation(ctx context.Context, mode byte) error {
	if mode > 3 {
		return fmt.Errorf("animation mode must be 0..3")
	}
	return c.set(ctx, CmdSetAnimationMode, mode)
}
func (c *Client) SetSleepTimeout(ctx context.Context, seconds byte) error {
	return c.set(ctx, CmdSetSleepTimeout, seconds)
}
func (c *Client) SetLongPressThreshold(ctx context.Context, ms uint16) error {
	return c.set(ctx, CmdSetLongPressThreshold, byte(ms>>8), byte(ms))
}
func (c *Client) SetButton(ctx context.Context, button byte, long bool, action byte) error {
	if button != 'A' && button != 'B' {
		return fmt.Errorf("button must be A or B")
	}
	if action > 5 {
		return fmt.Errorf("button action must be 0..5")
	}
	cmd := CmdSetButtonPressConfig
	if long {
		cmd = CmdSetLongButtonPressConfig
	}
	return c.set(ctx, cmd, button, action)
}
func (c *Client) SetBLEPairing(ctx context.Context, enabled bool) error {
	return c.set(ctx, CmdSetBlePairingEnable, boolean(enabled))
}
func (c *Client) SetBLEKey(ctx context.Context, key string) error {
	if len(key) != 6 {
		return fmt.Errorf("BLE key requires six decimal digits")
	}
	for _, b := range []byte(key) {
		if b < '0' || b > '9' {
			return fmt.Errorf("BLE key requires six decimal digits")
		}
	}
	return c.set(ctx, CmdSetBlePairingKey, []byte(key)...)
}
func (c *Client) ClearBLEBonds(ctx context.Context) error { return c.set(ctx, CmdDeleteAllBleBonds) }

type ExtendedBattery struct {
	Millivolts uint16 `json:"millivolts"`
	Percent    byte   `json:"percent"`
	Health     byte   `json:"health"`
}

func (c *Client) ExtendedBattery(ctx context.Context) (ExtendedBattery, error) {
	b, e := c.request(ctx, CmdGetBatteryInfoEx, nil, 0x68)
	if e != nil {
		return ExtendedBattery{}, e
	}
	if len(b) != 4 || b[2] > 100 || b[3] > 4 {
		return ExtendedBattery{}, fmt.Errorf("invalid extended battery response")
	}
	return ExtendedBattery{binary.BigEndian.Uint16(b), b[2], b[3]}, nil
}
