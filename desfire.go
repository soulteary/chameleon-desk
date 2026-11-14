package chameleon

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"
)

func desfireAPDU(op byte, data []byte) []byte {
	if len(data) == 0 {
		return []byte{0x90, op, 0, 0, 0}
	}
	p := append([]byte{0x90, op, 0, 0, byte(len(data))}, data...)
	return append(p, 0)
}
func desfireExchange(ctx context.Context, s *ISO4Session, op byte, data []byte) (HexBytes, byte, error) {
	b, e := s.Exchange(ctx, desfireAPDU(op, data))
	if e != nil {
		return nil, 0, e
	}
	if len(b) < 2 || b[len(b)-2] != 0x91 {
		return nil, 0, fmt.Errorf("DESFire response lacks native 91xx status")
	}
	return b[:len(b)-2], b[len(b)-1], nil
}
func desfireCollect(ctx context.Context, s *ISO4Session, op byte) (HexBytes, error) {
	out := HexBytes{}
	for n := 0; n < 32; n++ {
		b, status, e := desfireExchange(ctx, s, op, nil)
		if e != nil {
			return nil, e
		}
		if len(out)+len(b) > 4096 {
			return nil, fmt.Errorf("DESFire response exceeds 4096 bytes")
		}
		out = append(out, b...)
		if status == 0 {
			return out, nil
		}
		if status != 0xaf {
			return nil, fmt.Errorf("DESFire status %02x", status)
		}
		op = 0xaf
	}
	return nil, fmt.Errorf("DESFire additional-frame limit exceeded")
}

type DESFireInfo struct {
	Card          Card       `json:"card"`
	Version       HexBytes   `json:"version"`
	Hardware      HexBytes   `json:"hardware"`
	Software      HexBytes   `json:"software"`
	Manufacturing HexBytes   `json:"manufacturing"`
	Applications  []HexBytes `json:"applications"`
}

func (c *Client) DESFireInfo(ctx context.Context) (out DESFireInfo, err error) {
	err = c.ISO4(ctx, func(s *ISO4Session) error {
		out.Card = s.Card
		var e error
		out.Version, e = desfireCollect(ctx, s, 0x60)
		if e != nil {
			return e
		}
		if len(out.Version) != 28 {
			return fmt.Errorf("DESFire version requires 28 bytes, got %d", len(out.Version))
		}
		out.Hardware = out.Version[:7]
		out.Software = out.Version[7:14]
		out.Manufacturing = out.Version[14:]
		aids, e := desfireCollect(ctx, s, 0x6a)
		if e != nil {
			return e
		}
		if len(aids)%3 != 0 {
			return fmt.Errorf("truncated DESFire AID")
		}
		for len(aids) > 0 {
			out.Applications = append(out.Applications, append(HexBytes(nil), aids[:3]...))
			aids = aids[3:]
		}
		return nil
	})
	return
}
func desfireCipher(algorithm string, key []byte) (cipher.Block, byte, error) {
	switch algorithm {
	case "aes":
		if len(key) != 16 {
			return nil, 0, fmt.Errorf("AES key must be 16 bytes")
		}
		b, e := aes.NewCipher(key)
		return b, 0xaa, e
	case "des":
		if len(key) == 8 {
			key = bytes.Repeat(key, 3)
		} else if len(key) == 16 {
			key = append(append([]byte(nil), key...), key[:8]...)
		} else {
			return nil, 0, fmt.Errorf("DES/2TDEA key must be 8/16 bytes")
		}
		b, e := des.NewTripleDESCipher(key)
		return b, 0x0a, e
	case "3des":
		if len(key) != 24 {
			return nil, 0, fmt.Errorf("3TDEA key must be 24 bytes")
		}
		b, e := des.NewTripleDESCipher(key)
		return b, 0x1a, e
	}
	return nil, 0, fmt.Errorf("algorithm must be aes,des,3des")
}
func rotateNonce(b []byte) []byte { return append(append([]byte(nil), b[1:]...), b[0]) }
func desfireAuthenticate(ctx context.Context, s *ISO4Session, aid []byte, keyNo byte, algorithm string, key []byte, random io.Reader) (bool, error) {
	block, op, e := desfireCipher(algorithm, key)
	if e != nil {
		return false, e
	}
	if len(aid) != 3 || keyNo > 13 {
		return false, fmt.Errorf("AID must have three bytes; key number 0..13")
	}
	_, status, e := desfireExchange(ctx, s, 0x5a, aid)
	if e != nil {
		return false, e
	}
	if status != 0 {
		return false, fmt.Errorf("SelectApplication rejected: %02x", status)
	}
	encB, status, e := desfireExchange(ctx, s, op, []byte{keyNo})
	if e != nil {
		return false, e
	}
	if status == 0xae || status == 0x9d {
		return false, nil
	}
	if status != 0xaf {
		return false, fmt.Errorf("Authenticate rejected: %02x", status)
	}
	size := len(encB)
	bs := block.BlockSize()
	if size != bs && !(algorithm == "3des" && size == 16) {
		return false, fmt.Errorf("invalid encrypted nonce size %d", size)
	}
	rndB := make([]byte, size)
	cipher.NewCBCDecrypter(block, make([]byte, bs)).CryptBlocks(rndB, encB)
	rndA := make([]byte, size)
	if _, e = io.ReadFull(random, rndA); e != nil {
		return false, e
	}
	plain := append(append([]byte(nil), rndA...), rotateNonce(rndB)...)
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, encB[len(encB)-bs:]).CryptBlocks(encrypted, plain)
	reply, status, e := desfireExchange(ctx, s, 0xaf, encrypted)
	if e != nil {
		return false, e
	}
	if status == 0xae || status == 0x9d {
		return false, nil
	}
	if status != 0 {
		return false, fmt.Errorf("authentication continuation status %02x", status)
	}
	if len(reply) != size {
		return false, fmt.Errorf("invalid final nonce length")
	}
	answer := make([]byte, size)
	cipher.NewCBCDecrypter(block, encrypted[len(encrypted)-bs:]).CryptBlocks(answer, reply)
	return subtle.ConstantTimeCompare(answer, rotateNonce(rndA)) == 1, nil
}

// AuthenticateDESFire retains RF/PCB state across both challenge messages.
// It verifies the card's rotated nonce, rather than treating status 9100 as proof.
func (c *Client) AuthenticateDESFire(ctx context.Context, aid []byte, keyNo byte, algorithm string, key []byte) (ok bool, err error) {
	if _, _, e := desfireCipher(algorithm, key); e != nil {
		return false, e
	}
	if len(aid) != 3 || keyNo > 13 {
		return false, fmt.Errorf("AID/key number invalid")
	}
	err = c.ISO4(ctx, func(s *ISO4Session) error {
		var e error
		ok, e = desfireAuthenticate(ctx, s, aid, keyNo, algorithm, key, rand.Reader)
		return e
	})
	return
}

type DESFireKeyMatch struct {
	UID       HexBytes `json:"uid"`
	AID       HexBytes `json:"aid"`
	KeyNumber byte     `json:"key_number"`
	Algorithm string   `json:"algorithm"`
	Key       HexBytes `json:"verified_key"`
}

func (c *Client) CheckDESFireDictionary(ctx context.Context, aid []byte, keyNo byte, keys []HexBytes) (out []DESFireKeyMatch, err error) {
	if len(aid) != 3 || keyNo > 13 || len(keys) < 1 || len(keys) > 10000 {
		return nil, fmt.Errorf("three-byte AID, key number 0..13 and 1..10000 explicit keys required")
	}
	for _, key := range keys {
		if len(key) != 8 && len(key) != 16 && len(key) != 24 {
			return nil, fmt.Errorf("DESFire keys must have 8,16,24 bytes")
		}
	}
	var uid HexBytes
	for _, key := range keys {
		algorithms := []string{"des"}
		if len(key) == 16 {
			algorithms = []string{"aes", "des"}
		}
		if len(key) == 24 {
			algorithms = []string{"3des"}
		}
		for _, algorithm := range algorithms {
			var authenticated bool
			err = c.ISO4(ctx, func(s *ISO4Session) error {
				if uid == nil {
					uid = append(HexBytes(nil), s.Card.UID...)
				} else if !bytes.Equal(uid, s.Card.UID) {
					return fmt.Errorf("card changed during DESFire dictionary check")
				}
				var e error
				authenticated, e = desfireAuthenticate(ctx, s, aid, keyNo, algorithm, key, rand.Reader)
				return e
			})
			if err != nil {
				return
			}
			if authenticated {
				out = append(out, DESFireKeyMatch{uid, append(HexBytes(nil), aid...), keyNo, algorithm, append(HexBytes(nil), key...)})
				return out, nil
			}
		}
	}
	return out, nil
}
