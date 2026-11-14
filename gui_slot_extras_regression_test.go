package chameleon

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLoadGUICardVerifiesType2ExtrasBeforeSave(t *testing.T) {
	for _, bad := range []string{"", "version", "signature", "missing-version-get", "missing-signature-get"} {
		t.Run(bad, func(t *testing.T) {
			card := guiTestCard(t, 1100, "04112233445566", guiRows(1, 4, 0), map[string]any{"extra": map[string]any{"ultralightVersion": []int{0, 4, 4, 2, 1, 0, 17, 3}, "ultralightSignature": make([]int, 32)}})
			p, e := PlanGUICardLoad(card, 2)
			if e != nil {
				t.Fatal(e)
			}
			gets := []Command{CmdHf14aGetAntiCollData, CmdMf0NtagReadEmuPageData}
			if bad != "missing-version-get" {
				gets = append(gets, CmdMf0NtagGetVersionData)
			}
			if bad != "missing-signature-get" {
				gets = append(gets, CmdMf0NtagGetSignatureData)
			}
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1035, status: 0x68, data: guiCaps(p, gets...)}}
			if !strings.HasPrefix(bad, "missing-") {
				steps = append(steps, readerStep{command: 1018, status: 0x68, data: []byte{0}})
				for _, s := range p.Steps {
					steps = append(steps, readerStep{command: uint16(s.Command), request: s.Payload, status: 0x68})
				}
				steps = append(steps, readerStep{command: 4018, status: 0x68, data: p.identity}, readerStep{command: 4021, request: []byte{0, 1}, status: 0x68, data: p.runs[0].data})
				v := append([]byte(nil), p.version...)
				if bad == "version" {
					v[0] ^= 1
				}
				steps = append(steps, readerStep{command: 4023, status: 0x68, data: v})
				if bad != "version" {
					sig := append([]byte(nil), p.signature...)
					if bad == "signature" {
						sig[0] ^= 1
					}
					steps = append(steps, readerStep{command: 4025, status: 0x68, data: sig})
				}
				if bad == "" {
					steps = append(steps, readerStep{command: 1008, request: []byte{1, 2}, status: 0x68, data: []byte(p.Nickname)})
				}
				steps = append(steps, readerStep{command: 1003, request: []byte{0}, status: 0x68})
				if bad == "" {
					steps = append(steps, readerStep{command: 1009, status: 0x68})
				}
			}
			c := readerSimulator(t, steps)
			out, e := c.LoadGUICard(context.Background(), 2, card, GUICardLoadOptions{Persist: true})
			if bad == "" {
				if e != nil || !out.Verified || !out.Persisted {
					t.Fatal(out, e)
				}
			} else {
				if e == nil || out.Verified || out.Persisted {
					t.Fatal("bad extra accepted", out, e)
				}
			}
			if len(p.version) != 8 || !bytes.Equal(p.signature, make([]byte, 32)) {
				t.Fatal("plan lost extra data")
			}
		})
	}
}
