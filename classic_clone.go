package chameleon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

type ClassicCloneOptions struct {
	WriteTrailers  bool
	PreserveAccess bool
	// ExpectedIdentity optionally binds each physical write to captured anticollision metadata.
	ExpectedIdentity *ClassicIdentity
	// BeforeWrite runs once at the first write-command boundary.
	BeforeWrite func()
	// NoWriteRetry pre-authenticates candidates and sends at most one write per block.
	NoWriteRetry bool
}
type ClassicCloneResult struct {
	WrittenBlocks       int  `json:"written_blocks"`
	VerifiedBlocks      int  `json:"verified_blocks"`
	ManufacturerSkipped bool `json:"manufacturer_skipped"`
	TrailersWritten     bool `json:"trailers_written"`
}

func ValidateClassicAccess(trailer []byte) error {
	if len(trailer) != 16 {
		return fmt.Errorf("trailer must have 16 bytes")
	}
	a, b, c := trailer[6], trailer[7], trailer[8]
	if (a&15)^(b>>4) != 15 || (a>>4)^(c&15) != 15 || (b&15)^(c>>4) != 15 {
		return fmt.Errorf("invalid Classic access-bit redundancy")
	}
	return nil
}

// CloneClassic copies data to an explicitly authenticated target. Manufacturer
// block 0 is always skipped. Trailer writes are opt-in and happen after each
// sector's data. Failure can leave partial data/new keys; no automatic retries.
func (c *Client) CloneClassic(ctx context.Context, d ClassicCardDump, target ClassicKeyMap, o ClassicCloneOptions) (out ClassicCloneResult, err error) {
	if e := d.Validate(); e != nil {
		return out, e
	}
	if o.WriteTrailers && (d.Schema != 2 || len(d.UnknownKeys) != 0) {
		return out, fmt.Errorf("trailer writes require a schema-2 dump with all keys known; re-export with complete keys")
	}
	if e := target.Validate(); e != nil {
		return out, e
	}
	if o.ExpectedIdentity != nil {
		if err := o.ExpectedIdentity.Validate(); err != nil {
			return out, err
		}
		if !bytes.Equal(o.ExpectedIdentity.UID, target.UID) {
			return out, fmt.Errorf("target UID differs from expected Classic identity")
		}
	}
	geometry := map[uint16]int{1000: 5, 1001: 16, 1002: 32, 1003: 40}
	if geometry[d.TagType] != len(target.Sectors) {
		return out, fmt.Errorf("target key map and source geometry differ")
	}
	for _, s := range target.Sectors {
		if len(s.A) == 0 && len(s.B) == 0 {
			return out, fmt.Errorf("missing target sector key")
		}
		if o.WriteTrailers {
			first, count, _ := ClassicSectorGeometry(s.Sector)
			b := append([]byte(nil), d.Data[(first+count-1)*16:(first+count)*16]...)
			if !o.PreserveAccess {
				copy(b[6:9], []byte{255, 7, 128})
			}
			if e := ValidateClassicAccess(b); e != nil {
				return out, e
			}
		}
	}
	out.ManufacturerSkipped = true
	writeStarted := false
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for _, sector := range target.Sectors {
			if o.ExpectedIdentity == nil {
				if e := ensureUID(ctx, s, target.UID); e != nil {
					return e
				}
			}
			first, count, _ := ClassicSectorGeometry(sector.Sector)
			for block := first; block < first+count; block++ {
				if block == 0 {
					continue
				}
				trailer := block == first+count-1
				if trailer && !o.WriteTrailers {
					continue
				}
				if o.ExpectedIdentity != nil {
					expected := *o.ExpectedIdentity
					if e := observeClassicIdentity(ctx, s, target.UID, &expected); e != nil {
						return e
					}
				}
				data := append([]byte(nil), d.Data[block*16:(block+1)*16]...)
				if trailer && !o.PreserveAccess {
					copy(data[6:9], []byte{255, 7, 128})
				}
				var writeErr error
				usedKind := KeyA
				var usedKey []byte
				for side, key := range []HexBytes{sector.A, sector.B} {
					if len(key) == 0 {
						continue
					}
					kind := KeyA
					if side == 1 {
						kind = KeyB
					}
					if o.NoWriteRetry {
						p, _ := keyPayload(block, kind, key)
						if _, authErr := s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0); authErr != nil {
							var status *StatusError
							if errors.As(authErr, &status) && (status.Status == 2 || status.Status == 6) {
								writeErr = authErr
								continue
							}
							return authErr
						}
					}
					if !writeStarted {
						if err := ctx.Err(); err != nil {
							return err
						}
						if o.BeforeWrite != nil {
							o.BeforeWrite()
						}
						writeStarted = true
					}
					writeErr = s.WriteClassicBlock(ctx, block, kind, key, data)
					if writeErr == nil {
						usedKind, usedKey = kind, key
						break
					}
					if o.NoWriteRetry {
						return writeErr
					}
					var status *StatusError
					if !errors.As(writeErr, &status) || (status.Status != 2 && status.Status != 6) {
						return writeErr
					}
				}
				if writeErr != nil {
					return writeErr
				}
				out.WrittenBlocks++
				if trailer {
					out.TrailersWritten = true
					usedKind, usedKey = KeyA, data[:6]
				}
				read, e := s.ReadClassicBlock(ctx, block, usedKind, usedKey)
				if e != nil {
					return e
				}
				if trailer {
					if !bytes.Equal(read[6:10], data[6:10]) {
						return fmt.Errorf("trailer access/GPB readback mismatch at block %d", block)
					}
					p, _ := keyPayload(block, KeyB, data[10:16])
					_, e = s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0)
					if e != nil {
						var status *StatusError
						if !errors.As(e, &status) || status.Status != 6 || !classicBReadable(read) || !bytes.Equal(read[10:16], data[10:16]) {
							return fmt.Errorf("new Key B not verified at block %d: %w", block, e)
						}
					}
				} else if !bytes.Equal(read, data) {
					return fmt.Errorf("clone readback mismatch at block %d", block)
				}
				out.VerifiedBlocks++
			}
		}
		return nil
	})
	return
}
