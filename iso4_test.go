package chameleon

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"testing"
)

func rawISO(frame []byte) []byte {
	bits := len(frame) * 8
	return append([]byte{0x6c, 3, 232, byte(bits >> 8), byte(bits)}, frame...)
}
func isoCard() []byte { return []byte{4, 1, 2, 3, 4, 4, 0, 0x20, 2, 2, 5} }
func TestISO4WTXChainAndSequence(t *testing.T) {
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2016, data: isoCard()}, {command: 2010, request: rawISO([]byte{2, 0, 0xa4, 4, 0}), data: []byte{0xf2, 2}}, {command: 2010, request: rawISO([]byte{0xf2, 2}), data: []byte{0x12, 0xaa}}, {command: 2010, request: rawISO([]byte{0xa3}), data: []byte{3, 0x90, 0}}, {command: 2010, request: rawISO([]byte{3, 0, 0xa4, 4, 0}), data: []byte{2, 0x90, 0}}, {command: 2101, status: 0x68}}
	c := readerSimulator(t, steps)
	var retained *ISO4Session
	e := c.ISO4(context.Background(), func(s *ISO4Session) error {
		retained = s
		v, e := s.Exchange(context.Background(), []byte{0, 0xa4, 4, 0})
		if e != nil || !bytes.Equal(v, []byte{0xaa, 0x90, 0}) {
			t.Fatal(v, e)
		}
		_, e = s.Exchange(context.Background(), []byte{0, 0xa4, 4, 0})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = retained.Exchange(context.Background(), []byte{0, 0xa4, 4, 0}); e != ErrClosed {
		t.Fatal(e)
	}
}
func TestDESFireAESMutualAuthentication(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		key := make([]byte, 16)
		block, _ := aes.NewCipher(key)
		encB, _ := hex.DecodeString("66e94bd4ef8a2c3b884cfa59ca342b2e")
		rndA := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
		plain := append(append([]byte(nil), rndA...), make([]byte, 16)...)
		sent := make([]byte, 32)
		cipher.NewCBCEncrypter(block, encB).CryptBlocks(sent, plain)
		expected := rotateNonce(rndA)
		if wrong {
			expected[0] ^= 1
		}
		reply := make([]byte, 16)
		cipher.NewCBCEncrypter(block, sent[16:]).CryptBlocks(reply, expected)
		steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2016, data: isoCard()}, {command: 2010, request: rawISO(append([]byte{2}, desfireAPDU(0x5a, []byte{0, 0, 0})...)), data: []byte{2, 0x91, 0}}, {command: 2010, request: rawISO(append([]byte{3}, desfireAPDU(0xaa, []byte{0})...)), data: append(append([]byte{3}, encB...), 0x91, 0xaf)}, {command: 2010, request: rawISO(append([]byte{2}, desfireAPDU(0xaf, sent)...)), data: append(append([]byte{2}, reply...), 0x91, 0)}, {command: 2101, status: 0x68}}
		c := readerSimulator(t, steps)
		var ok bool
		e := c.ISO4(context.Background(), func(s *ISO4Session) error {
			var e error
			ok, e = desfireAuthenticate(context.Background(), s, []byte{0, 0, 0}, 0, "aes", key, bytes.NewReader(rndA))
			return e
		})
		if e != nil || ok == wrong {
			t.Fatal(ok, wrong, e)
		}
	}
}
