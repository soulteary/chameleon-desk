package chameleon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// EmulatorImage is an active HF-slot memory image, not an eight-slot/settings
// backup. UID/configuration/LF data are deliberately separate APIs.
type EmulatorImage struct {
	Schema     int      `json:"schema"`
	TagType    uint16   `json:"tag_type"`
	UnitBytes  int      `json:"unit_bytes"`
	SourceSlot int      `json:"source_slot"`
	Data       HexBytes `json:"data"`
	SHA256     string   `json:"sha256"`
}

func imageDigest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func (i EmulatorImage) Validate() error {
	if i.Schema != 1 {
		return fmt.Errorf("unsupported image schema")
	}
	if i.SHA256 != imageDigest(i.Data) {
		return fmt.Errorf("image checksum mismatch")
	}
	if i.TagType >= 1000 && i.TagType <= 1003 {
		sizes := map[uint16]int{1000: 320, 1001: 1024, 1002: 2048, 1003: 4096}
		if i.UnitBytes != 16 || len(i.Data) != sizes[i.TagType] {
			return fmt.Errorf("invalid Classic image geometry")
		}
	} else if i.TagType >= 1100 && i.TagType <= 1108 {
		if i.UnitBytes != 4 || len(i.Data) != guiType2Pages[i.TagType]*4 {
			return fmt.Errorf("invalid Type2 image geometry")
		}
	} else {
		return fmt.Errorf("unsupported image tag type %d", i.TagType)
	}
	return nil
}
func (c *Client) activeGeometry(ctx context.Context) (slot int, tag uint16, units, width int, err error) {
	active, e := c.exchange(ctx, 1018, nil, 0x68)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	if len(active) != 1 || active[0] > 7 {
		return 0, 0, 0, 0, fmt.Errorf("invalid active slot")
	}
	types, e := c.exchange(ctx, 1019, nil, 0x68)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	if len(types) != 32 {
		return 0, 0, 0, 0, fmt.Errorf("invalid slot types")
	}
	slot = int(active[0]) + 1
	pos := int(active[0]) * 4
	tag = uint16(types[pos])<<8 | uint16(types[pos+1])
	if n, ok := map[uint16]int{1000: 20, 1001: 64, 1002: 128, 1003: 256}[tag]; ok {
		return slot, tag, n, 16, nil
	}
	if tag >= 1100 && tag <= 1108 {
		b, e := c.exchange(ctx, 4030, nil, 0x68)
		if e != nil {
			return 0, 0, 0, 0, e
		}
		if len(b) != 1 || int(b[0]) != guiType2Pages[tag] {
			return 0, 0, 0, 0, fmt.Errorf("invalid page count")
		}
		return slot, tag, int(b[0]), 4, nil
	}
	return 0, 0, 0, 0, fmt.Errorf("active HF type %d has no supported memory image", tag)
}
func (c *Client) readImageUnits(ctx context.Context, start, count, width int) (HexBytes, error) {
	cmd := uint16(4008)
	if width == 4 {
		cmd = 4021
	}
	out := make(HexBytes, 0, count*width)
	for pos := start; pos < start+count; {
		n := min(32, start+count-pos)
		b, e := c.exchange(ctx, cmd, []byte{byte(pos), byte(n)}, 0x68)
		if e != nil {
			return nil, e
		}
		if len(b) != n*width {
			return nil, fmt.Errorf("memory response length mismatch")
		}
		out = append(out, b...)
		pos += n
	}
	return out, nil
}
func (c *Client) ExportEmulator(ctx context.Context) (EmulatorImage, error) {
	q, stop := context.WithTimeout(ctx, c.timeout)
	e := c.acquire(q)
	stop()
	if e != nil {
		return EmulatorImage{}, e
	}
	defer c.release()
	slot, tag, units, width, e := c.activeGeometry(ctx)
	if e != nil {
		return EmulatorImage{}, e
	}
	b, e := c.readImageUnits(ctx, 0, units, width)
	if e != nil {
		return EmulatorImage{}, e
	}
	return EmulatorImage{1, tag, width, slot, b, imageDigest(b)}, nil
}

// ImportEmulator validates before writing and verifies all bytes afterwards.
// The active slot's type/size must already match. It does not save to flash.
// A failed transfer can leave partial RAM data; re-import the original image.
func (c *Client) ImportEmulator(ctx context.Context, image EmulatorImage) error {
	if e := image.Validate(); e != nil {
		return e
	}
	q, stop := context.WithTimeout(ctx, c.timeout)
	e := c.acquire(q)
	stop()
	if e != nil {
		return e
	}
	defer c.release()
	_, tag, units, width, e := c.activeGeometry(ctx)
	if e != nil {
		return e
	}
	if tag != image.TagType || width != image.UnitBytes || units*width != len(image.Data) {
		return fmt.Errorf("active slot geometry does not match image; no data written")
	}
	for pos := 0; pos < units; {
		n := min(32, units-pos)
		p := []byte{byte(pos)}
		cmd := uint16(4000)
		if width == 4 {
			cmd = 4022
			p = append(p, byte(n))
		}
		p = append(p, image.Data[pos*width:(pos+n)*width]...)
		if _, e = c.exchange(ctx, cmd, p, 0x68); e != nil {
			return fmt.Errorf("partial import at unit %d: %w", pos, e)
		}
		pos += n
	}
	b, e := c.readImageUnits(ctx, 0, units, width)
	if e != nil {
		return fmt.Errorf("import readback: %w", e)
	}
	if !bytes.Equal(b, image.Data) {
		return fmt.Errorf("import readback verification mismatch")
	}
	return nil
}
