package chameleon

import (
	"context"
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

type Version struct {
	Major byte `json:"major"`
	Minor byte `json:"minor"`
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

type Battery struct {
	Millivolts uint16 `json:"millivolts"`
	Percent    byte   `json:"percent"`
}
type Slot struct {
	Number    int    `json:"number"`
	Active    bool   `json:"active"`
	HFType    uint16 `json:"hf_type"`
	LFType    uint16 `json:"lf_type"`
	HFName    string `json:"hf_name"`
	LFName    string `json:"lf_name"`
	HFEnabled bool   `json:"hf_enabled"`
	LFEnabled bool   `json:"lf_enabled"`
}
type Info struct {
	Model        string   `json:"model"`
	ModelID      byte     `json:"model_id"`
	Firmware     Version  `json:"firmware"`
	GitVersion   string   `json:"git_version"`
	Mode         string   `json:"mode"`
	Battery      Battery  `json:"battery"`
	Capabilities []uint16 `json:"capabilities"`
	Slots        []Slot   `json:"slots"`
}

func (c *Client) fixed(ctx context.Context, cmd uint16, n int) ([]byte, error) {
	b, err := c.query(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if len(b) != n {
		return nil, fmt.Errorf("command %d: expected %d bytes, got %d", cmd, n, len(b))
	}
	return b, nil
}
func (c *Client) Firmware(ctx context.Context) (Version, error) {
	b, e := c.fixed(ctx, 1000, 2)
	if e != nil {
		return Version{}, e
	}
	return Version{b[0], b[1]}, nil
}
func (c *Client) Model(ctx context.Context) (byte, string, error) {
	b, e := c.fixed(ctx, 1033, 1)
	if e != nil {
		return 0, "", e
	}
	names := map[byte]string{0: "Chameleon Ultra", 1: "Chameleon Lite"}
	name, ok := names[b[0]]
	if !ok {
		name = fmt.Sprintf("Unknown model (%d)", b[0])
	}
	return b[0], name, nil
}
func (c *Client) GitVersion(ctx context.Context) (string, error) {
	b, e := c.query(ctx, 1017)
	if e != nil {
		return "", e
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("invalid UTF-8 git version")
	}
	return string(b), nil
}
func (c *Client) Mode(ctx context.Context) (string, error) {
	b, e := c.fixed(ctx, 1002, 1)
	if e != nil {
		return "", e
	}
	switch b[0] {
	case 0:
		return "emulator", nil
	case 1:
		return "reader", nil
	default:
		return "", fmt.Errorf("invalid mode %d", b[0])
	}
}
func (c *Client) Battery(ctx context.Context) (Battery, error) {
	b, e := c.fixed(ctx, 1025, 3)
	if e != nil {
		return Battery{}, e
	}
	if b[2] > 100 {
		return Battery{}, fmt.Errorf("invalid battery percentage %d", b[2])
	}
	return Battery{binary.BigEndian.Uint16(b), b[2]}, nil
}
func (c *Client) Capabilities(ctx context.Context) ([]uint16, error) {
	b, e := c.query(ctx, 1035)
	if e != nil {
		return nil, e
	}
	if len(b)%2 != 0 {
		return nil, fmt.Errorf("odd capabilities payload length")
	}
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return out, nil
}
func TagName(t uint16) string {
	names := map[uint16]string{0: "None", 100: "EM410X", 101: "EM410X/16", 102: "EM410X/32", 103: "EM410X/64", 104: "EM410X Electra", 150: "PAC", 170: "Viking", 180: "Jablotron", 200: "HIDProx", 201: "ioProx", 202: "Pyramid", 310: "IDTECK", 1000: "MIFARE Mini", 1001: "MIFARE Classic 1K", 1002: "MIFARE Classic 2K", 1003: "MIFARE Classic 4K", 1100: "NTAG213", 1101: "NTAG215", 1102: "NTAG216", 1103: "MIFARE Ultralight", 1104: "MIFARE Ultralight C", 1105: "MIFARE Ultralight EV1 640", 1106: "MIFARE Ultralight EV1 1312", 1107: "NTAG210", 1108: "NTAG212", 3000: "ISO14443-4", 3001: "SEOS"}
	if name, ok := names[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (%d)", t)
}

// Slots uses three consecutive read-only queries. External button changes may
// affect the snapshot; the protocol does not offer an atomic slot snapshot.
func (c *Client) Slots(ctx context.Context) ([]Slot, error) {
	types, e := c.fixed(ctx, 1019, 32)
	if e != nil {
		return nil, e
	}
	enabled, e := c.fixed(ctx, 1023, 16)
	if e != nil {
		return nil, e
	}
	active, e := c.fixed(ctx, 1018, 1)
	if e != nil {
		return nil, e
	}
	if active[0] > 7 {
		return nil, fmt.Errorf("invalid active slot %d", active[0])
	}
	out := make([]Slot, 8)
	for i := range out {
		if enabled[2*i] > 1 || enabled[2*i+1] > 1 {
			return nil, fmt.Errorf("invalid enable flags for slot %d", i+1)
		}
		hf, lf := binary.BigEndian.Uint16(types[4*i:]), binary.BigEndian.Uint16(types[4*i+2:])
		out[i] = Slot{i + 1, int(active[0]) == i, hf, lf, TagName(hf), TagName(lf), enabled[2*i] == 1, enabled[2*i+1] == 1}
	}
	return out, nil
}
func (c *Client) Info(ctx context.Context) (Info, error) {
	var v Info
	var e error
	if v.Firmware, e = c.Firmware(ctx); e != nil {
		return v, e
	}
	if v.ModelID, v.Model, e = c.Model(ctx); e != nil {
		return v, e
	}
	if v.GitVersion, e = c.GitVersion(ctx); e != nil {
		return v, e
	}
	if v.Capabilities, e = c.Capabilities(ctx); e != nil {
		return v, e
	}
	if v.Mode, e = c.Mode(ctx); e != nil {
		return v, e
	}
	if v.Battery, e = c.Battery(ctx); e != nil {
		return v, e
	}
	v.Slots, e = c.Slots(ctx)
	return v, e
}
