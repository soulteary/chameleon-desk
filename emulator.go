package chameleon

import (
	"context"
	"fmt"
)

func (c *Client) memoryRead(ctx context.Context, start, count, width int) (HexBytes, error) {
	if start < 0 || start > 255 || count < 1 || count > 256 || count > 256-start {
		return nil, fmt.Errorf("invalid memory range")
	}
	q, stop := context.WithTimeout(ctx, c.timeout)
	e := c.acquire(q)
	stop()
	if e != nil {
		return nil, e
	}
	defer c.release()
	_, _, units, actual, e := c.activeGeometry(ctx)
	if e != nil {
		return nil, e
	}
	if actual != width || start+count > units {
		return nil, fmt.Errorf("range or kind does not match active slot")
	}
	return c.readImageUnits(ctx, start, count, width)
}
func (c *Client) memoryWrite(ctx context.Context, start, width int, data []byte) error {
	count := len(data) / width
	if start < 0 || start > 255 || count < 1 || count > 256 || len(data)%width != 0 || count > 256-start {
		return fmt.Errorf("invalid memory range or alignment")
	}
	q, stop := context.WithTimeout(ctx, c.timeout)
	e := c.acquire(q)
	stop()
	if e != nil {
		return e
	}
	defer c.release()
	_, _, units, actual, e := c.activeGeometry(ctx)
	if e != nil {
		return e
	}
	if actual != width || start+count > units {
		return fmt.Errorf("range or kind does not match active slot; no data written")
	}
	for pos := 0; pos < count; {
		n := min(32, count-pos)
		p := []byte{byte(start + pos)}
		cmd := uint16(4000)
		if width == 4 {
			cmd = 4022
			p = append(p, byte(n))
		}
		p = append(p, data[pos*width:(pos+n)*width]...)
		if _, e = c.exchange(ctx, cmd, p, 0x68); e != nil {
			return fmt.Errorf("write at unit %d (partial data possible): %w", start+pos, e)
		}
		pos += n
	}
	return nil
}
func (c *Client) ReadEmulatorBlocks(ctx context.Context, start, count int) (HexBytes, error) {
	return c.memoryRead(ctx, start, count, 16)
}
func (c *Client) WriteEmulatorBlocks(ctx context.Context, start int, data []byte) error {
	return c.memoryWrite(ctx, start, 16, data)
}
func (c *Client) ReadEmulatorPages(ctx context.Context, start, count int) (HexBytes, error) {
	return c.memoryRead(ctx, start, count, 4)
}
func (c *Client) WriteEmulatorPages(ctx context.Context, start int, data []byte) error {
	return c.memoryWrite(ctx, start, 4, data)
}

func (c *Client) AntiCollision(ctx context.Context) (Card, error) {
	b, e := c.request(ctx, CmdHf14aGetAntiCollData, nil, 0x68)
	if e != nil {
		return Card{}, e
	}
	cards, e := parseCards(b)
	if e != nil {
		return Card{}, e
	}
	if len(cards) != 1 {
		return Card{}, fmt.Errorf("expected one anti-collision record")
	}
	return cards[0], nil
}
func antiCollisionPayload(card Card) ([]byte, error) {
	n := len(card.UID)
	if n != 4 && n != 7 && n != 10 {
		return nil, fmt.Errorf("UID requires 4,7,10 bytes")
	}
	if len(card.ATS) > 255 {
		return nil, fmt.Errorf("ATS exceeds 255 bytes")
	}
	p := append([]byte{byte(n)}, card.UID...)
	p = append(p, byte(card.ATQA), byte(card.ATQA>>8), card.SAK, byte(len(card.ATS)))
	return append(p, card.ATS...), nil
}
func (c *Client) SetAntiCollision(ctx context.Context, card Card) error {
	p, e := antiCollisionPayload(card)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdHf14aSetAntiCollData, p...)
}

type EmulatorConfig struct {
	Detection         bool `json:"detection"`
	Gen1A             bool `json:"gen1a"`
	Gen2              bool `json:"gen2"`
	BlockZeroIdentity bool `json:"block_zero_identity"`
	WriteMode         byte `json:"write_mode"`
}

func (c *Client) EmulatorConfig(ctx context.Context) (EmulatorConfig, error) {
	b, e := c.request(ctx, CmdMf1GetEmulatorConfig, nil, 0x68)
	if e != nil {
		return EmulatorConfig{}, e
	}
	if len(b) != 5 {
		return EmulatorConfig{}, fmt.Errorf("emulator config requires 5 bytes")
	}
	for _, v := range b[:4] {
		if v > 1 {
			return EmulatorConfig{}, fmt.Errorf("invalid config flag")
		}
	}
	if b[4] > 4 {
		return EmulatorConfig{}, fmt.Errorf("invalid write mode")
	}
	return EmulatorConfig{b[0] == 1, b[1] == 1, b[2] == 1, b[3] == 1, b[4]}, nil
}
func (c *Client) SetEmulatorFlag(ctx context.Context, name string, value bool) error {
	cmds := map[string]Command{"detection": CmdMf1SetDetectionEnable, "gen1a": CmdMf1SetGen1aMode, "gen2": CmdMf1SetGen2Mode, "block-zero": CmdMf1SetBlockAntiCollMode, "field-reset": CmdMf1SetFieldOffDoReset, "type2-magic": CmdMf0NtagSetUidMagicMode, "type2-detection": CmdMf0NtagSetDetectionEnable}
	cmd, ok := cmds[name]
	if !ok {
		return fmt.Errorf("unknown emulator flag %q", name)
	}
	return c.set(ctx, cmd, boolean(value))
}
func (c *Client) SetEmulatorWriteMode(ctx context.Context, type2 bool, mode byte) error {
	if mode > 4 {
		return fmt.Errorf("write mode must be 0..4")
	}
	cmd := CmdMf1SetWriteMode
	if type2 {
		cmd = CmdMf0NtagSetWriteMode
	}
	return c.set(ctx, cmd, mode)
}
func (c *Client) EmulatorLFID(ctx context.Context, protocol string) (HexBytes, error) {
	get, _, e := lfIDCommands(protocol)
	if e != nil {
		return nil, e
	}
	b, e := c.request(ctx, get, nil, 0x68)
	if e == nil && protocol == "em410x" {
		b, e = normalizeEM410xID(0, b)
	}
	return HexBytes(b), e
}
func lfIDCommands(protocol string) (Command, Command, error) {
	m := map[string][2]Command{"em410x": {CmdEm410xGetEmuId, CmdEm410xSetEmuId}, "hidprox": {CmdHidproxGetEmuId, CmdHidproxSetEmuId}, "ioprox": {CmdIoproxGetEmuId, CmdIoproxSetEmuId}, "viking": {CmdVikingGetEmuId, CmdVikingSetEmuId}, "pac": {CmdPacGetEmuId, CmdPacSetEmuId}, "jablotron": {CmdJablotronGetEmuId, CmdJablotronSetEmuId}, "idteck": {CmdIdteckGetEmuId, CmdIdteckSetEmuId}}
	p, ok := m[protocol]
	if !ok {
		return 0, 0, fmt.Errorf("unknown LF protocol")
	}
	return p[0], p[1], nil
}
func (c *Client) SetEmulatorLFID(ctx context.Context, protocol string, id []byte) error {
	_, set, e := lfIDCommands(protocol)
	if e != nil {
		return e
	}
	lengths := map[string]int{"hidprox": 13, "ioprox": 16, "viking": 4, "pac": 8, "jablotron": 5, "idteck": 8}
	if protocol == "em410x" {
		if len(id) != 5 && len(id) != 13 {
			return fmt.Errorf("EM410x ID requires 5 or 13 bytes")
		}
	} else if len(id) != lengths[protocol] {
		return fmt.Errorf("%s ID requires %d bytes", protocol, lengths[protocol])
	}
	return c.set(ctx, set, id...)
}
func (c *Client) EmulatorCounter(ctx context.Context, index int) (value uint32, tearingOK bool, err error) {
	if index < 0 || index > 2 {
		return 0, false, fmt.Errorf("counter index must be 0..2")
	}
	b, e := c.request(ctx, CmdMf0NtagGetCounterData, []byte{byte(index)}, 0x68)
	if e != nil {
		return 0, false, e
	}
	if len(b) != 4 {
		return 0, false, fmt.Errorf("counter response requires four bytes")
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16, b[3] == 0xbd, nil
}
func (c *Client) SetEmulatorCounter(ctx context.Context, index int, value uint32, resetTearing bool) error {
	if index < 0 || index > 2 || value > 0xffffff {
		return fmt.Errorf("invalid counter index or 24-bit value")
	}
	return c.set(ctx, CmdMf0NtagSetCounterData, byte(index)|boolean(resetTearing)<<7, byte(value), byte(value>>8), byte(value>>16))
}

// Current GET responses include a two-byte tag type; SET accepts ID bytes only.
// Older firmware returned bare five/thirteen-byte IDs.
func normalizeEM410xID(expected uint16, b []byte) ([]byte, error) {
	if len(b) == 7 || len(b) == 15 {
		tag := uint16(b[0])<<8 | uint16(b[1])
		if tag < 100 || tag > 104 || (expected != 0 && tag != expected) {
			return nil, fmt.Errorf("EM410x response tag type mismatch")
		}
		return append([]byte(nil), b[2:]...), nil
	}
	if len(b) != 5 && len(b) != 13 {
		return nil, fmt.Errorf("invalid EM410x ID response length")
	}
	return b, nil
}
