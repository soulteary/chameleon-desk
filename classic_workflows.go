package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

func ClassicSectorGeometry(sector int) (first, count int, err error) {
	if sector < 0 || sector > 39 {
		return 0, 0, fmt.Errorf("sector must be 0..39")
	}
	if sector < 32 {
		return sector * 4, 4, nil
	}
	return 128 + (sector-32)*16, 16, nil
}

type ClassicKeyMap struct {
	Schema   int          `json:"schema"`
	UID      HexBytes     `json:"uid"`
	Sectors  []SectorKeys `json:"sectors"`
	Complete bool         `json:"complete"`
}

func (k ClassicKeyMap) Validate() error {
	if k.Schema != 1 || (len(k.UID) != 4 && len(k.UID) != 7 && len(k.UID) != 10) || len(k.Sectors) < 1 || len(k.Sectors) > 40 {
		return fmt.Errorf("invalid key map")
	}
	complete := true
	for i, s := range k.Sectors {
		if s.BReadable && (len(s.A) != 6 || len(s.B) != 6) {
			return fmt.Errorf("readable Key B evidence requires both keys in the map")
		}
		if s.Sector != i {
			return fmt.Errorf("key map must contain ordered contiguous sectors from zero")
		}
		for _, key := range []HexBytes{s.A, s.B} {
			if len(key) != 0 && len(key) != 6 {
				return fmt.Errorf("sector keys must be six bytes or absent")
			}
			complete = complete && len(key) == 6
		}
	}
	if k.Complete != complete {
		return fmt.Errorf("key map completeness flag mismatch")
	}
	return nil
}
func (k *ClassicKeyMap) updateComplete() {
	k.Complete = true
	for _, s := range k.Sectors {
		k.Complete = k.Complete && len(s.A) == 6 && len(s.B) == 6
	}
}
func ensureUID(ctx context.Context, s *ReaderSession, uid []byte) error {
	current, e := scanSession(ctx, s)
	if e != nil {
		return e
	}
	if !bytes.Equal(current, uid) {
		return fmt.Errorf("card changed during operation")
	}
	return nil
}

// CheckClassicDictionary uses the firmware's 80-bit sector mask, batching to fit
// the caller's command timeout. Keys are authenticated or proved by an ACL-readable trailer.
func (c *Client) CheckClassicDictionary(ctx context.Context, sectors int, keys []HexBytes) (out ClassicKeyMap, err error) {
	if sectors < 1 || sectors > 40 || len(keys) < 1 || len(keys) > 100000 {
		return out, fmt.Errorf("1..40 sectors and 1..100000 keys required")
	}
	for _, key := range keys {
		if len(key) != 6 {
			return out, fmt.Errorf("dictionary keys require six bytes")
		}
	}
	out.Schema = 1
	out.Sectors = make([]SectorKeys, sectors)
	for i := range out.Sectors {
		out.Sectors[i].Sector = i
	}
	minimum := time.Second + time.Duration(2*sectors+1)*100*time.Millisecond
	if c.timeout < minimum {
		return out, fmt.Errorf("sector dictionary check needs per-command timeout at least %s", minimum)
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		var e error
		out.UID, e = scanSession(ctx, s)
		if e != nil {
			return e
		}
		for pos := 0; pos < len(keys); {
			mask := bytes.Repeat([]byte{255}, 10)
			missing := 0
			for i, v := range out.Sectors {
				for j, key := range []HexBytes{v.A, v.B} {
					if len(key) == 0 {
						bit := i*2 + j
						mask[bit/8] &= ^(byte(0x80) >> uint(bit%8))
						missing++
					}
				}
			}
			if missing == 0 {
				break
			}
			n := min(83, len(keys)-pos, int((c.timeout-time.Second)/(time.Duration(missing+1)*100*time.Millisecond)))
			n = max(1, n)
			if e = ensureUID(ctx, s, out.UID); e != nil {
				return e
			}
			p := append([]byte(nil), mask...)
			for _, key := range keys[pos : pos+n] {
				p = append(p, key...)
			}
			r, e := s.Do(ctx, CmdMf1CheckKeysOfSectors, p)
			if e != nil {
				return e
			}
			if r.Status == 1 {
				return ErrNoCard
			}
			if e = r.Check(0); e != nil {
				return e
			}
			if len(r.Data) != 490 {
				return fmt.Errorf("invalid sector-check response length")
			}
			for bit := 0; bit < 80; bit++ {
				if r.Data[bit/8]&(0x80>>uint(bit%8)) == 0 {
					continue
				}
				if bit >= sectors*2 {
					return fmt.Errorf("firmware returned a masked sector key")
				}
				if mask[bit/8]&(0x80>>uint(bit%8)) != 0 {
					continue
				}
				if e = ensureUID(ctx, s, out.UID); e != nil {
					return e
				}
				key := append(HexBytes(nil), r.Data[10+6*bit:16+6*bit]...)
				first, _, _ := ClassicSectorGeometry(bit / 2)
				kind := KeyA
				if bit%2 != 0 {
					kind = KeyB
				}
				p, _ := keyPayload(first, kind, key)
				_, e := s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0)
				var status *StatusError
				if errors.As(e, &status) && status.Status == 6 {
					continue
				}
				if e != nil {
					return e
				}
				if kind == KeyA {
					out.Sectors[bit/2].A = key
				} else {
					out.Sectors[bit/2].B = key
				}
			}
			// Key B may be readable data and unusable for authentication.
			// In that case record it only after reading a valid trailer with A.
			for i := range out.Sectors {
				entry := &out.Sectors[i]
				if len(entry.A) != 6 || len(entry.B) != 0 {
					continue
				}
				if e = ensureUID(ctx, s, out.UID); e != nil {
					return e
				}
				first, count, _ := ClassicSectorGeometry(i)
				trailer, readErr := s.ReadClassicBlock(ctx, first+count-1, KeyA, entry.A)
				if readErr != nil {
					var status *StatusError
					if errors.As(readErr, &status) && (status.Status == 2 || status.Status == 6) {
						continue
					}
					return readErr
				}
				if classicBReadable(trailer) {
					entry.B = append(HexBytes(nil), trailer[10:16]...)
					entry.BReadable = true
				}
			}
			pos += n
		}
		return nil
	})
	out.updateComplete()
	return
}

type AutopwnOptions struct {
	Recovery   AdvancedRecoveryOptions
	Dictionary []HexBytes
	Backdoor   HexBytes
	Sectors    int
}

// Autopwn is bounded and deterministic: dictionary, recovered-key reuse, then
// one recovery per missing sector/key. Backdoor recovery is opt-in, never guessed.
func (c *Client) Autopwn(ctx context.Context, o AutopwnOptions) (out ClassicKeyMap, err error) {
	if o.Recovery.Limit <= 0 {
		return out, fmt.Errorf("positive total recovery limit required")
	}
	if len(o.Backdoor) != 0 && len(o.Backdoor) != 6 {
		return out, fmt.Errorf("backdoor must be six bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, o.Recovery.Limit)
	defer cancel()
	out, err = c.CheckClassicDictionary(ctx, o.Sectors, o.Dictionary)
	if err != nil || out.Complete {
		return
	}
	if len(o.Backdoor) > 0 {
		v, e := c.RecoverStaticEncrypted(ctx, o.Backdoor, o.Sectors, o.Recovery.ToolsDir, o.Recovery.Limit)
		if e != nil {
			return out, e
		}
		if !bytes.Equal(out.UID, v.UID) {
			return out, fmt.Errorf("card changed during backdoor recovery")
		}
		for _, s := range v.Sectors {
			if len(s.A) > 0 {
				out.Sectors[s.Sector].A = s.A
			}
			if len(s.B) > 0 {
				out.Sectors[s.Sector].B = s.B
			}
		}
		out.updateComplete()
		if out.Complete {
			return out, nil
		}
	}
	for sector := range out.Sectors {
		for side := 0; side < 2; side++ {
			entry := &out.Sectors[sector]
			if side == 0 && len(entry.A) > 0 || side == 1 && len(entry.B) > 0 {
				continue
			}
			if side == 1 && len(entry.A) == 6 {
				var readable HexBytes
				err = c.Reader(ctx, func(s *ReaderSession) error {
					if e := ensureUID(ctx, s, out.UID); e != nil {
						return e
					}
					first, count, _ := ClassicSectorGeometry(sector)
					trailer, e := s.ReadClassicBlock(ctx, first+count-1, KeyA, entry.A)
					if e != nil {
						var status *StatusError
						if errors.As(e, &status) && (status.Status == 2 || status.Status == 6) {
							return nil
						}
						return e
					}
					if classicBReadable(trailer) {
						readable = append(HexBytes(nil), trailer[10:16]...)
					}
					return nil
				})
				if err != nil {
					return out, err
				}
				if len(readable) != 0 {
					entry.B, entry.BReadable = readable, true
					out.updateComplete()
					continue
				}
			}
			knownBlock, knownKind, knownKey := -1, KeyA, HexBytes(nil)
			for _, s := range out.Sectors {
				first, _, _ := ClassicSectorGeometry(s.Sector)
				if len(s.A) > 0 {
					knownBlock, knownKind, knownKey = first, KeyA, s.A
					break
				}
				if len(s.B) > 0 {
					knownBlock, knownKind, knownKey = first, KeyB, s.B
					break
				}
			}
			first, _, _ := ClassicSectorGeometry(sector)
			kind := KeyA
			if side == 1 {
				kind = KeyB
			}
			r := o.Recovery
			if knownBlock < 0 {
				r.Strategy = "darkside"
				knownBlock = 0
			}
			var candidate HexBytes
			// Try already known keys against the target before another CPU recovery.
			reuse := []HexBytes{}
			for _, s := range out.Sectors {
				if len(s.A) > 0 {
					reuse = append(reuse, s.A)
				}
				if len(s.B) > 0 {
					reuse = append(reuse, s.B)
				}
			}
			candidate, err = c.verifyRecovery(ctx, out.UID, first, kind, reuse)
			if err != nil {
				return
			}
			if len(candidate) == 0 {
				v, e := c.RecoverClassic(ctx, knownBlock, knownKind, knownKey, first, kind, r)
				if e != nil {
					return out, e
				}
				if !bytes.Equal(v.UID, out.UID) {
					return out, fmt.Errorf("card changed during recovery")
				}
				candidate = v.Key
			}
			if side == 0 {
				entry.A = candidate
			} else {
				entry.B = candidate
			}
			out.updateComplete()
		}
	}
	if !out.Complete {
		return out, fmt.Errorf("not all sector keys recovered")
	}
	return out, nil
}

// ClassicUnknownKey identifies a key whose trailer bytes are not known.
type ClassicUnknownKey struct {
	Sector int     `json:"sector"`
	Type   KeyType `json:"key_type"`
}

type ClassicCardDump struct {
	UnknownKeys []ClassicUnknownKey `json:"unknown_keys,omitempty"`
	Schema      int                 `json:"schema"`
	UID         HexBytes            `json:"uid"`
	TagType     uint16              `json:"tag_type"`
	Data        HexBytes            `json:"data"`
	SHA256      string              `json:"sha256"`
}

func (d ClassicCardDump) Validate() error {
	i := EmulatorImage{Schema: 1, TagType: d.TagType, UnitBytes: 16, Data: d.Data, SHA256: d.SHA256}
	if (d.Schema != 1 && d.Schema != 2) || (len(d.UID) != 4 && len(d.UID) != 7 && len(d.UID) != 10) {
		return fmt.Errorf("invalid card dump metadata")
	}
	if d.TagType < 1000 || d.TagType > 1003 {
		return fmt.Errorf("dump must be Classic")
	}
	if e := i.Validate(); e != nil {
		return e
	}
	sectors := map[uint16]int{1000: 5, 1001: 16, 1002: 32, 1003: 40}[d.TagType]
	seen := map[ClassicUnknownKey]bool{}
	for _, k := range d.UnknownKeys {
		if d.Schema != 2 || k.Sector < 0 || k.Sector >= sectors || (k.Type != KeyA && k.Type != KeyB) || seen[k] {
			return fmt.Errorf("invalid unknown-key metadata")
		}
		seen[k] = true
	}
	return nil
}

// DumpClassicWithKeys verifies supplied keys against authentication or readable
// trailer data, then reads each sector. Key A
// and hidden Key B bytes in trailers are filled from supplied keys, not guessed.
// Schema 2 explicitly records unknown keys; such dumps cannot write trailers.
func (c *Client) DumpClassicWithKeys(ctx context.Context, k ClassicKeyMap) (ClassicCardDump, error) {
	return c.dumpClassicWithKeys(ctx, k, nil)
}

func (c *Client) dumpClassicWithKeys(ctx context.Context, k ClassicKeyMap, identity *ClassicIdentity) (out ClassicCardDump, err error) {
	tag, e := classicReadGeometry(k)
	if e != nil {
		return out, e
	}
	out.Schema = 2
	out.UID = append(HexBytes(nil), k.UID...)
	out.TagType = tag
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for _, sector := range k.Sectors {
			var e error
			if identity == nil {
				e = ensureUID(ctx, s, k.UID)
			} else {
				e = observeClassicIdentity(ctx, s, k.UID, identity)
			}
			if e != nil {
				return e
			}
			verified := [2]bool{}
			first, count, _ := ClassicSectorGeometry(sector.Sector)
			for block := first; block < first+count; block++ {
				var data []byte
				var last error
				for side, key := range []HexBytes{sector.A, sector.B} {
					if len(key) == 0 {
						continue
					}
					kind := KeyA
					if side == 1 {
						kind = KeyB
					}
					data, last = s.ReadClassicBlock(ctx, block, kind, key)
					if last == nil {
						verified[side] = true
						break
					}
					var status *StatusError
					if !errors.As(last, &status) || (status.Status != 2 && status.Status != 6) {
						return last
					}
				}
				if last != nil {
					return last
				}
				if block == first+count-1 {
					if e := ValidateClassicAccess(data); e != nil {
						return fmt.Errorf("sector %d trailer: %w", sector.Sector, e)
					}
					readableB := classicBReadable(data)
					for side, key := range []HexBytes{sector.A, sector.B} {
						kind := KeyA
						if side == 1 {
							kind = KeyB
						}
						if side == 1 && readableB {
							if len(key) == 6 && !bytes.Equal(key, data[10:16]) {
								return fmt.Errorf("sector %d supplied Key B differs from readable trailer", sector.Sector)
							}
							continue
						}
						if len(key) == 0 {
							out.UnknownKeys = append(out.UnknownKeys, ClassicUnknownKey{sector.Sector, kind})
							continue
						}
						if !verified[side] {
							p, _ := keyPayload(block, kind, key)
							if _, e := s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0); e != nil {
								return fmt.Errorf("sector %d supplied key %02x not verified: %w", sector.Sector, byte(kind), e)
							}
						}
						if side == 0 {
							copy(data[:6], key)
						} else {
							copy(data[10:16], key)
						}
					}
				}
				out.Data = append(out.Data, data...)
			}
		}
		if identity != nil {
			return observeClassicIdentity(ctx, s, k.UID, identity)
		}
		return nil
	})
	if err == nil {
		out.SHA256 = imageDigest(out.Data)
		err = out.Validate()
	}
	return
}
func EncodeClassicValue(value int32, address byte) HexBytes {
	b := make(HexBytes, 16)
	binary.LittleEndian.PutUint32(b, uint32(value))
	for i := 0; i < 4; i++ {
		b[4+i] = ^b[i]
		b[8+i] = b[i]
	}
	b[12], b[13], b[14], b[15] = address, ^address, address, ^address
	return b
}
func DecodeClassicValue(b []byte) (int32, byte, error) {
	if len(b) != 16 {
		return 0, 0, fmt.Errorf("value block requires 16 bytes")
	}
	for i := 0; i < 4; i++ {
		if b[4+i] != ^b[i] || b[8+i] != b[i] {
			return 0, 0, fmt.Errorf("value redundancy mismatch")
		}
	}
	if b[12] != b[14] || b[13] != ^b[12] || b[15] != ^b[12] {
		return 0, 0, fmt.Errorf("address redundancy mismatch")
	}
	return int32(binary.LittleEndian.Uint32(b)), b[12], nil
}
func (c *Client) ReadClassicValue(ctx context.Context, block int, kind KeyType, key []byte) (int32, byte, error) {
	b, e := c.ReadClassicBlock(ctx, block, kind, key)
	if e != nil {
		return 0, 0, e
	}
	return DecodeClassicValue(b)
}
func (c *Client) WriteClassicValue(ctx context.Context, block int, kind KeyType, key []byte, value int32) error {
	return c.WriteClassicBlock(ctx, block, kind, key, EncodeClassicValue(value, byte(block)))
}
func (c *Client) ManipulateClassicValue(ctx context.Context, src int, srcKind KeyType, srcKey []byte, op byte, amount int32, dst int, dstKind KeyType, dstKey []byte) error {
	p, e := keyPayload(src, srcKind, srcKey)
	if e != nil {
		return e
	}
	q, e := keyPayload(dst, dstKind, dstKey)
	if e != nil {
		return e
	}
	if op != 0xc0 && op != 0xc1 && op != 0xc2 || amount < 0 {
		return fmt.Errorf("operator must be C0/C1/C2 with nonnegative operand")
	}
	if op == 0xc2 && amount != 0 {
		return fmt.Errorf("restore operator requires zero operand")
	}
	p = append(p, op)
	operand := make([]byte, 4)
	binary.BigEndian.PutUint32(operand, uint32(amount))
	p = append(p, operand...)
	p = append(p, q...)
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, CmdMf1ManipulateValueBlock, p, 0); return e })
}

func classicBReadable(trailer []byte) bool {
	if ValidateClassicAccess(trailer) != nil {
		return false
	}
	code := ((trailer[7]>>7)&1)<<2 | ((trailer[8]>>3)&1)<<1 | (trailer[8]>>7)&1
	return code <= 2
}
