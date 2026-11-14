package chameleon

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func DecodeMemoryFile(data []byte, format string) (HexBytes, error) {
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("memory file exceeds 1MiB")
	}
	switch format {
	case "bin":
		return append(HexBytes(nil), data...), nil
	case "hex", "eml":
		text := strings.Join(strings.Fields(string(data)), "")
		b, e := hex.DecodeString(text)
		if e != nil {
			return nil, e
		}
		return b, nil
	case "json":
		var i EmulatorImage
		if e := json.Unmarshal(data, &i); e != nil {
			return nil, e
		}
		if e := i.Validate(); e != nil {
			return nil, e
		}
		return i.Data, nil
	default:
		return nil, fmt.Errorf("memory format must be bin,eml,hex,json")
	}
}
func EncodeMemoryFile(image EmulatorImage, format string) ([]byte, error) {
	if e := image.Validate(); e != nil {
		return nil, e
	}
	switch format {
	case "bin":
		return append([]byte(nil), image.Data...), nil
	case "eml", "hex":
		var b strings.Builder
		for i := 0; i < len(image.Data); i += image.UnitBytes {
			b.WriteString(strings.ToUpper(hex.EncodeToString(image.Data[i : i+image.UnitBytes])))
			b.WriteByte('\n')
		}
		return []byte(b.String()), nil
	case "json":
		b, e := json.MarshalIndent(image, "", "  ")
		return append(b, '\n'), e
	default:
		return nil, fmt.Errorf("memory format must be bin,eml,hex,json")
	}
}
func (c *Client) ExportSlotMemory(ctx context.Context, slot int) (out EmulatorImage, err error) {
	err = c.slotSession(ctx, slot, false, func(s *ReaderSession) error {
		n, tag, units, width, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		b, e := c.readImageUnits(ctx, 0, units, width)
		if e != nil {
			return e
		}
		out = EmulatorImage{1, tag, width, n, b, imageDigest(b)}
		return out.Validate()
	})
	return
}

type MemoryLoadResult struct {
	Slot     int  `json:"slot"`
	Start    int  `json:"start"`
	Units    int  `json:"units"`
	Verified bool `json:"verified"`
	Saved    bool `json:"saved"`
}

func (c *Client) LoadSlotMemory(ctx context.Context, slot, start int, data []byte, persist bool) (MemoryLoadResult, error) {
	return c.loadSlotMemory(ctx, slot, start, data, persist, 0, "")
}
func (c *Client) LoadSlotMemoryFile(ctx context.Context, slot, start int, file []byte, format, family string, persist bool) (MemoryLoadResult, error) {
	if family != "" && family != "classic" && family != "type2" {
		return MemoryLoadResult{}, fmt.Errorf("unknown memory family")
	}
	data, e := DecodeMemoryFile(file, format)
	if e != nil {
		return MemoryLoadResult{}, e
	}
	tag := uint16(0)
	if format == "json" {
		var image EmulatorImage
		if e = json.Unmarshal(file, &image); e != nil {
			return MemoryLoadResult{}, e
		}
		tag = image.TagType
	}
	return c.loadSlotMemory(ctx, slot, start, data, persist, tag, family)
}
func (c *Client) loadSlotMemory(ctx context.Context, slot, start int, data []byte, persist bool, expectedTag uint16, expectedFamily string) (out MemoryLoadResult, err error) {
	if start < 0 || start > 255 || len(data) == 0 || len(data) > 4096 {
		return out, fmt.Errorf("invalid memory start or length")
	}
	data = append([]byte(nil), data...)
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		n, tag, units, width, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if expectedFamily == "classic" && width != 16 || expectedFamily == "type2" && width != 4 {
			return fmt.Errorf("memory family mismatch; no data written")
		}
		if expectedTag != 0 && (tag != expectedTag || start != 0 || len(data) != units*width) {
			return fmt.Errorf("JSON image must exactly match slot type and geometry; no data written")
		}
		if len(data)%width != 0 || len(data)/width > units-start {
			return fmt.Errorf("input alignment/range does not fit active memory; no data written")
		}
		out = MemoryLoadResult{Slot: n, Start: start, Units: len(data) / width}
		for pos := 0; pos < out.Units; {
			count := min(32, out.Units-pos)
			cmd := CmdMf1WriteEmuBlockData
			p := []byte{byte(start + pos)}
			if width == 4 {
				cmd = CmdMf0NtagWriteEmuPageData
				p = append(p, byte(count))
			}
			p = append(p, data[pos*width:(pos+count)*width]...)
			if _, e = s.expect(ctx, cmd, p, 0x68); e != nil {
				return fmt.Errorf("partial memory load at unit %d: %w", start+pos, e)
			}
			pos += count
		}
		got, e := c.readImageUnits(ctx, start, out.Units, width)
		if e != nil {
			return e
		}
		if !bytes.Equal(got, data) {
			return fmt.Errorf("memory load readback mismatch")
		}
		out.Verified = true
		return nil
	})
	out.Saved = err == nil && persist
	return
}
