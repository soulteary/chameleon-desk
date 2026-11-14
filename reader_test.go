package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type readerStep struct {
	command uint16
	request []byte
	status  uint16
	data    []byte
	before  func()
}

func readerSimulator(t *testing.T, steps []readerStep) *Client {
	t.Helper()
	local, peer := net.Pipe()
	c := newClient(local, time.Second)
	done := make(chan struct{})
	consumed := 0
	go func() {
		defer close(done)
		for _, step := range steps {
			header := make([]byte, 9)
			if _, e := io.ReadFull(peer, header); e != nil {
				return
			}
			payload := make([]byte, int(binary.BigEndian.Uint16(header[6:8]))+1)
			if _, e := io.ReadFull(peer, payload); e != nil {
				return
			}
			if lrc(header) != 0 || lrc(payload) != 0 {
				t.Error("invalid request checksum")
			}
			cmd := binary.BigEndian.Uint16(header[2:4])
			if cmd != step.command || !bytes.Equal(payload[:len(payload)-1], step.request) {
				t.Errorf("request %d/%x; want %d/%x", cmd, payload[:len(payload)-1], step.command, step.request)
			}
			consumed++
			if step.before != nil {
				step.before()
			}
			if _, e := peer.Write(response(cmd, step.status, step.data)); e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = c.Close()
		_ = peer.Close()
		<-done
		if consumed != len(steps) {
			t.Errorf("consumed %d of %d requests", consumed, len(steps))
		}
	})
	return c
}

func capabilities(commands ...uint16) []byte {
	b := make([]byte, len(commands)*2)
	for i, c := range commands {
		binary.BigEndian.PutUint16(b[2*i:], c)
	}
	return b
}
func sampleCard() []byte { return []byte{4, 0xde, 0xad, 0xbe, 0xef, 0x04, 0x00, 0x08, 0} }
func scanSteps(mode byte, status uint16, payload []byte) []readerStep {
	steps := []readerStep{{command: 1035, status: 0x68, data: capabilities(1001, 1002, 2000)}, {command: 1002, status: 0x68, data: []byte{mode}}}
	if mode == 0 {
		steps = append(steps, readerStep{command: 1001, request: []byte{1}, status: 0x68})
	}
	steps = append(steps, readerStep{command: 2000, status: status, data: payload})
	if mode == 0 {
		steps = append(steps, readerStep{command: 1001, request: []byte{0}, status: 0x68})
	}
	return steps
}

func TestScanSwitchAndRestore(t *testing.T) {
	c := readerSimulator(t, scanSteps(0, 0, sampleCard()))
	cards, e := c.ScanISO14443A(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(cards) != 1 || cards[0].UID.String() != "DEADBEEF" || cards[0].ATQA != 4 || cards[0].ATQAWire.String() != "0400" || cards[0].SAK != 8 {
		t.Fatalf("%+v", cards)
	}
	if c.Stats().Responses != 5 {
		t.Fatalf("%+v", c.Stats())
	}
}
func TestScanAlreadyReader(t *testing.T) {
	c := readerSimulator(t, scanSteps(1, 0, sampleCard()))
	if _, e := c.ScanISO14443A(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestScanNoCardRestores(t *testing.T) {
	c := readerSimulator(t, scanSteps(0, 1, nil))
	if _, e := c.ScanISO14443A(context.Background()); !errors.Is(e, ErrNoCard) {
		t.Fatal(e)
	}
}
func TestScanStatusErrorRestores(t *testing.T) {
	c := readerSimulator(t, scanSteps(0, 3, nil))
	var status *StatusError
	if _, e := c.ScanISO14443A(context.Background()); !errors.As(e, &status) || status.Command != 2000 || status.Status != 3 {
		t.Fatalf("%v", e)
	}
}
func TestScanMalformedRestores(t *testing.T) {
	c := readerSimulator(t, scanSteps(0, 0, []byte{7, 1}))
	if _, e := c.ScanISO14443A(context.Background()); e == nil {
		t.Fatal("accepted truncated payload")
	}
}
func TestScanRestoreErrorNotHidden(t *testing.T) {
	steps := scanSteps(0, 1, nil)
	steps[len(steps)-1].status = 0x66
	c := readerSimulator(t, steps)
	_, e := c.ScanISO14443A(context.Background())
	var restore *ModeRestoreError
	if !errors.Is(e, ErrNoCard) || !errors.As(e, &restore) {
		t.Fatalf("%v", e)
	}
}
func TestScanUnsupported(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1035, status: 0x68, data: capabilities(1001, 1002)}})
	if _, e := c.ScanISO14443A(context.Background()); !errors.Is(e, ErrReaderUnsupported) {
		t.Fatal(e)
	}
}
func TestScanSequenceExclusive(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	steps := scanSteps(0, 0, sampleCard())
	steps[3].before = func() { close(entered); <-release }
	steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{0}})
	c := readerSimulator(t, steps)
	scanned := make(chan error, 1)
	go func() { _, e := c.ScanISO14443A(context.Background()); scanned <- e }()
	<-entered
	queried := make(chan error, 1)
	go func() { _, e := c.Mode(context.Background()); queried <- e }()
	close(release)
	if e := <-scanned; e != nil {
		t.Fatal(e)
	}
	if e := <-queried; e != nil {
		t.Fatal(e)
	}
}
func TestParseCardsMultipleAndATS(t *testing.T) {
	second := []byte{7, 4, 1, 2, 3, 4, 5, 6, 0x44, 0, 0, 2, 0x75, 0x77}
	cards, e := parseCards(append(sampleCard(), second...))
	if e != nil {
		t.Fatal(e)
	}
	if len(cards) != 2 || cards[1].UID.String() != "04010203040506" || cards[1].ATQA != 0x44 || cards[1].ATS.String() != "7577" {
		t.Fatalf("%+v", cards)
	}
	b, e := json.Marshal(cards)
	if e != nil || !strings.Contains(string(b), `"uid":"DEADBEEF"`) {
		t.Fatalf("%s %v", b, e)
	}
}
func TestParseCardsTruncated(t *testing.T) {
	b := sampleCard()
	for n := 0; n < len(b); n++ {
		if _, e := parseCards(b[:n]); e == nil {
			t.Fatalf("accepted length %d", n)
		}
	}
	for _, b := range [][]byte{{5, 1, 2, 3, 4, 5, 4, 0, 8, 0}, {4, 1, 2, 3, 4, 4, 0, 8, 2, 0x75}} {
		if _, e := parseCards(b); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
func TestCardFamilyHints(t *testing.T) {
	for _, v := range []struct {
		sak      byte
		contains string
	}{{8, "1K"}, {0x88, "1K"}, {0x18, "4K"}, {9, "Mini"}, {0, "Ultralight / NTAG"}, {0x20, "ISO14443-4"}, {0x28, "1K"}, {0x28, "ISO14443-4"}, {0x40, "peer-to-peer"}, {0x04, "incomplete"}} {
		if got := cardType(v.sak); !strings.Contains(got, v.contains) {
			t.Fatalf("%02x: %s", v.sak, got)
		}
	}
}
func TestEncodePayload(t *testing.T) {
	b := encode(1001, 1)
	if len(b) != 11 || b[7] != 1 || b[9] != 1 || b[10] != 0xff || lrc(b) != 0 {
		t.Fatalf("%x", b)
	}
}
func FuzzParseCards(f *testing.F) {
	f.Add(sampleCard())
	f.Add([]byte{7, 4})
	f.Fuzz(func(t *testing.T, b []byte) {
		cards, e := parseCards(b)
		if e == nil {
			for _, c := range cards {
				if n := len(c.UID); n != 4 && n != 7 && n != 10 {
					t.Fatal(n)
				}
			}
		}
	})
}

func TestCardJSONRoundTrip(t *testing.T) {
	cards, err := parseCards(sampleCard())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []Card
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !bytes.Equal(decoded[0].UID, cards[0].UID) || !bytes.Equal(decoded[0].ATQAWire, cards[0].ATQAWire) {
		t.Fatalf("invalid round trip: %+v", decoded)
	}
	var raw HexBytes
	if err = json.Unmarshal([]byte(`"not-hex"`), &raw); err == nil {
		t.Fatal("accepted non-hex input")
	}
}
