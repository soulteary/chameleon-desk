package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Only a loopback TCP simulator is exposed. No USB/BLE enumeration or access.
func workflowSimulator(t *testing.T, handle func(uint16, []byte) (uint16, []byte)) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var conn net.Conn
	accepted := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			close(accepted)
			return
		}
		conn = c
		close(accepted)
		defer c.Close()
		for {
			h := make([]byte, 9)
			if _, e := io.ReadFull(c, h); e != nil {
				return
			}
			p := make([]byte, int(binary.BigEndian.Uint16(h[6:8]))+1)
			if _, e := io.ReadFull(c, p); e != nil {
				return
			}
			cmd := binary.BigEndian.Uint16(h[2:])
			status, data := handle(cmd, p[:len(p)-1])
			b := make([]byte, 10+len(data))
			b[0], b[1] = 0x11, 0xef
			binary.BigEndian.PutUint16(b[2:], cmd)
			binary.BigEndian.PutUint16(b[4:], status)
			binary.BigEndian.PutUint16(b[6:], uint16(len(data)))
			for _, v := range b[:8] {
				b[8] -= v
			}
			copy(b[9:], data)
			for _, v := range data {
				b[len(b)-1] -= v
			}
			if _, e := c.Write(b); e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-accepted
		if conn != nil {
			conn.Close()
		}
		<-done
	})
	return "tcp:" + ln.Addr().String()
}
func TestULCGCLIMultiStageJournalAndFailures(t *testing.T) {
	for _, name := range []string{"complete", "stdout-failure", "replacement", "write-failure", "before-first-checkpoint"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
			var writes, records atomic.Int32
			port := workflowSimulator(t, func(cmd uint16, p []byte) (uint16, []byte) {
				switch cmd {
				case 1002:
					return 0x68, []byte{1}
				case 2000:
					return 0, []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}
				case 2101:
					return 0x68, nil
				case 2010:
					if len(p) < 7 {
						t.Error("short raw command")
						return 2, nil
					}
					switch p[5] {
					case 0x30:
						b := make([]byte, 16)
						b[8] = 48
						return 0, b
					case 0x1a:
						if name == "before-first-checkpoint" {
							return 2, nil
						}
						return 0, []byte{0xaf, 1, 1, 1, 1, 1, 1, 1, 1}
					case 0xa2:
						n := writes.Add(1)
						raw, e := os.ReadFile(path)
						if e != nil {
							t.Error(e)
						}
						var checkpoints []chameleon.ULCGChallenges
						d := json.NewDecoder(bytes.NewReader(raw))
						for {
							var v chameleon.ULCGChallenges
							e := d.Decode(&v)
							if e == io.EOF {
								break
							}
							if e != nil {
								t.Error(e)
								break
							}
							checkpoints = append(checkpoints, v)
						}
						records.Store(int32(len(checkpoints)))
						if len(checkpoints) != int(n)*2 || len(checkpoints[len(checkpoints)-1].PossiblyErasedPages) != int(n) {
							t.Error("write before durable checkpoint", n, len(checkpoints))
						}
						if name == "replacement" {
							if e := os.Rename(path, path+".saved"); e != nil {
								t.Error(e)
							}
							if e := os.WriteFile(path, []byte("external"), 0600); e != nil {
								t.Error(e)
							}
						}
						if name == "write-failure" {
							return 2, nil
						}
						return 0, []byte{10}
					}
				}
				t.Errorf("unexpected simulator command %d/%X", cmd, p)
				return 0x67, nil
			})
			stdout := os.Stdout
			if name == "stdout-failure" {
				f, e := os.CreateTemp(t.TempDir(), "closed-stdout")
				if e != nil {
					t.Fatal(e)
				}
				f.Close()
				os.Stdout = f
			}
			e := runWorkflow([]string{"ulc", "ulcg-collect", "--yes", "--erase-key", "--challenges", "2", "--port", port, "--output", path, "--limit", "15s"})
			os.Stdout = stdout
			switch name {
			case "complete":
				if e != nil || writes.Load() != 4 {
					t.Fatal(writes.Load(), e)
				}
				raw, _ := os.ReadFile(path)
				if bytes.Count(raw, []byte{'\n'}) != 9 {
					t.Fatalf("wrong checkpoint count %s", raw)
				}
				v, e := readULCGCheckpoint(path)
				if e != nil || len(v.PossiblyErasedPages) != 4 {
					t.Fatal(v, e)
				}
				tools := t.TempDir()
				helper := filepath.Join(tools, "mfulc_des_brute")
				if e := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'Full key (hex): 00112233445566778899aabbccddeeff\\n'\n"), 0700); e != nil {
					t.Fatal(e)
				}
				if e := runWorkflow([]string{"ulc", "ulcg-recover", "--input", path, "--tools-dir", tools, "--threads", "1", "--limit", "1m", "--output", path + ".recovered"}); e != nil {
					t.Fatal("CLI could not consume its own journal", e)
				}

			case "stdout-failure":
				if writes.Load() != 4 || !errors.Is(e, os.ErrClosed) || !strings.Contains(e.Error(), "device operation already ran") {
					t.Fatal(writes.Load(), e)
				}
				if _, e := readULCGCheckpoint(path); e != nil {
					t.Fatal("summary error lost durable journal", e)
				}
			case "replacement":
				if e == nil || writes.Load() != 1 || !strings.Contains(e.Error(), "replaced") {
					t.Fatal(writes.Load(), e)
				}
				raw, _ := os.ReadFile(path)
				if string(raw) != "external" {
					t.Fatal("replaced journal overwritten")
				}
			case "write-failure":
				var status *chameleon.StatusError
				if e == nil || !errors.As(e, &status) || writes.Load() != 1 {
					t.Fatal(writes.Load(), e)
				}
				if records.Load() != 2 {
					t.Fatal(records.Load())
				}
				raw, _ := os.ReadFile(path)
				if bytes.Count(raw, []byte{'\n'}) != 2 {
					t.Fatal("failed write lost pre-write journal")
				}
			case "before-first-checkpoint":
				if e == nil || writes.Load() != 0 {
					t.Fatal(writes.Load(), e)
				}
				if _, e = os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("unused empty journal retained", e)
				}
			}
		})
	}
}
func TestULCGJournalReaderCompatibility(t *testing.T) {
	full := chameleon.ULCGChallenges{Schema: 1, UID: chameleon.HexBytes{1, 2, 3, 4}, Challenge100: make(chameleon.HexBytes, 8), Challenge75: make(chameleon.HexBytes, 8), Challenge50: make(chameleon.HexBytes, 8), Challenge25: make(chameleon.HexBytes, 8), Challenge0: make(chameleon.HexBytes, 8)}
	other := full
	other.UID = chameleon.HexBytes{9, 8, 7, 6}
	otherJSON, _ := json.Marshal(other)
	one, _ := json.MarshalIndent(full, "", "  ")
	compact, _ := json.Marshal(full)
	partial := full
	partial.Challenge0 = nil
	pr, _ := json.Marshal(partial)
	for _, tt := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"legacy", one, true}, {"journal", append(append(pr, '\n'), compact...), true}, {"torn-tail", append(append(append([]byte(nil), compact...), '\n'), []byte(`{"schema":`)...), true},
		{"incomplete", pr, false}, {"invalid-tail", append(append(append([]byte(nil), compact...), '\n'), []byte("garbage")...), false}, {"empty", nil, false}, {"mixed-UID", bytes.Join([][]byte{compact, otherJSON}, []byte{'\n'}), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "journal")
			os.WriteFile(p, tt.data, 0600)
			_, e := readULCGCheckpoint(p)
			if (e == nil) != tt.ok {
				t.Fatal(e)
			}
		})
	}
	// A closed journal must never grant permission for the next destructive step.
	p := filepath.Join(t.TempDir(), "closed")
	j, e := newULCGJournal(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = j.Write(full); e != nil {
		t.Fatal(e)
	}
	j.Close()
	if e = j.Write(full); e == nil {
		t.Fatal("closed journal accepted write")
	}
}
func TestWorkflowPreservesOperationAndOutputErrors(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "result.json")
	input := filepath.Join(dir, "dump.json")
	keys := filepath.Join(dir, "keys.json")
	// Schema 1 remains safe for data-only cloning; the first block write fails.
	data := make([]byte, 320)
	sum := sha256.Sum256(data)
	dump := chameleon.ClassicCardDump{Schema: 1, UID: chameleon.HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: hex.EncodeToString(sum[:])}
	km := chameleon.ClassicKeyMap{Schema: 1, UID: chameleon.HexBytes{1, 2, 3, 4}, Sectors: make([]chameleon.SectorKeys, 5)}
	for i := range km.Sectors {
		km.Sectors[i] = chameleon.SectorKeys{Sector: i, A: make(chameleon.HexBytes, 6)}
	}
	for p, v := range map[string]any{input: dump, keys: km} {
		b, _ := json.Marshal(v)
		os.WriteFile(p, b, 0600)
	}
	port := workflowSimulator(t, func(cmd uint16, p []byte) (uint16, []byte) {
		switch cmd {
		case 1002:
			return 0x68, []byte{1}
		case 2000:
			return 0, []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
		case 2007:
			return 0, nil
		case 2009:
			if e := os.WriteFile(output, []byte("external"), 0600); e != nil {
				t.Error(e)
			}
			return 2, nil
		}
		t.Error("unexpected command", cmd)
		return 0x67, nil
	})
	e := runWorkflow([]string{"mfc", "clone", "--yes", "--port", port, "--input", input, "--keys-file", keys, "--output", output, "--limit", "15s"})
	var status *chameleon.StatusError
	if !errors.As(e, &status) || !errors.Is(e, os.ErrExist) || !strings.Contains(e.Error(), "device operation already ran") {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(output)
	if string(raw) != "external" {
		t.Fatal("late result conflict overwritten")
	}
}

func TestULCGCheckpointWriteFailureStopsBeforeErase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, e := newULCGJournal(path)
	if e != nil {
		t.Fatal(e)
	}
	// Keep the same regular inode, but make its descriptor unwritable. This
	// exercises a real checkpoint Write error, rather than a mocked callback.
	j.file.Close()
	j.file, e = os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	raw := func(command []byte) []byte {
		bits := len(command) * 8
		return append([]byte{0xf4, 1, 244, byte(bits >> 8), byte(bits)}, command...)
	}
	config := make([]byte, 16)
	config[8] = 48
	nonce := []byte{0xaf, 1, 1, 1, 1, 1, 1, 1, 1}
	open := mockDevice(t, []step{
		{cmd: 1002, status: 0x68, data: []byte{1}},
		{cmd: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}},
		{cmd: 2010, payload: raw([]byte{0x30, 40}), data: config},
		{cmd: 2010, payload: raw([]byte{0x1a, 0}), data: nonce},
		{cmd: 2010, payload: raw([]byte{0x1a, 0}), data: nonce},
		{cmd: 2101, status: 0x68},
	})
	c, e := open("mock", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.CollectULCG(context.Background(), 2, true, j.Write)
	if e == nil || !strings.Contains(e.Error(), "write ULCG checkpoint") || len(out.PossiblyErasedPages) != 0 {
		t.Fatal(out, e)
	}
}
