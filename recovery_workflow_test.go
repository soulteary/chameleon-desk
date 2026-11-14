package chameleon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNestedSampleDecoding(t *testing.T) {
	s, e := nestedSample(0, nil, []byte{1, 2, 3, 4, 0, 0, 0, 2, 0, 0, 0, 3}, 4, KeyA)
	if e != nil || s.Arguments[0] != "16909060" || s.Arguments[1] != "96" || s.Arguments[3] != "3" {
		t.Fatal(s, e)
	}
	s, e = nestedSample(1, []byte{1, 2, 3, 4, 0, 0, 0, 32}, []byte{0, 0, 0, 1, 0, 0, 0, 2, 3}, 4, KeyB)
	if e != nil || s.Tool != "nested" || len(s.Arguments) != 5 {
		t.Fatal(s, e)
	}
	for _, v := range []struct {
		k    byte
		d, n []byte
	}{{0, nil, nil}, {1, make([]byte, 8), make([]byte, 8)}, {2, nil, nil}} {
		if _, e = nestedSample(v.k, v.d, v.n, 4, KeyA); e == nil {
			t.Fatal("invalid sample accepted")
		}
	}
}
func TestRecoveryCandidateParser(t *testing.T) {
	v := recoveryCandidates("untrusted abcdefabcdef\nKey 1... FFFFFFFFFFFF \r\nKey 2... ffffffffffff \nKey 3... 010203040506\n")
	if len(v) != 2 || v[1].String() != "010203040506" {
		t.Fatal(v)
	}
}
func TestCollectStaticNested(t *testing.T) {
	key := []byte{1, 2, 3, 4, 5, 6}
	p := []byte{0x60, 0, 1, 2, 3, 4, 5, 6, 0x61, 4}
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 2002, data: []byte{0}}, {command: 2003, request: p, data: []byte{1, 2, 3, 4, 0, 0, 0, 1, 0, 0, 0, 2}}, {command: 1001, request: []byte{0}, status: 0x68}})
	v, e := c.CollectNested(context.Background(), 0, KeyA, key, 4, KeyB)
	if e != nil || v.Tool != "staticnested" {
		t.Fatal(v, e)
	}
}
func TestCollectHardnestedRestoresMode(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 2002, data: []byte{2}}, {command: 1001, request: []byte{0}, status: 0x68}})
	_, e := c.CollectNested(context.Background(), 0, KeyA, make([]byte, 6), 4, KeyA)
	if !errors.Is(e, ErrHardnestedRequired) {
		t.Fatal(e)
	}
}
func TestRecoveryInputRejectedBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.CollectNested(context.Background(), 0, KeyA, []byte{1}, 4, KeyB); e == nil {
		t.Fatal("accepted short key")
	}
	if _, e := c.RecoverNested(context.Background(), "tools/bin", 0, 0, KeyA, make([]byte, 6), 4, KeyB); e == nil {
		t.Fatal("accepted unbounded recovery")
	}
}

func TestRecoverNestedVerifiesCandidate(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "staticnested"), []byte("#!/bin/sh\nprintf 'Key 1... FFFFFFFFFFFF \nKey 2... 010203040506 \n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2002, data: []byte{0}}, {command: 2003, request: []byte{0x60, 0, 0, 0, 0, 0, 0, 0, 0x61, 4}, data: []byte{1, 2, 3, 4, 0, 0, 0, 1, 0, 0, 0, 2}}}
	for i, key := range [][]byte{{255, 255, 255, 255, 255, 255}, {1, 2, 3, 4, 5, 6}} {
		status := uint16(6)
		if i == 1 {
			status = 0
		}
		p, _ := keyPayload(4, KeyB, key)
		steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}, readerStep{command: 2007, request: p, status: status})
	}
	c := readerSimulator(t, steps)
	v, e := c.RecoverNested(context.Background(), dir, 5*time.Second, 0, KeyA, make([]byte, 6), 4, KeyB)
	if e != nil || v.Key.String() != "010203040506" || v.Candidates != 2 {
		t.Fatal(v, e)
	}
}
