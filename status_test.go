package chameleon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogPinnedAndComplete(t *testing.T) {
	b, e := os.ReadFile("internal/catalog/upstream.json")
	if e != nil {
		t.Fatal(e)
	}
	var file struct {
		Commit   string `json:"commit"`
		Commands []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Registered bool   `json:"registered"`
		} `json:"commands"`
	}
	if e = json.Unmarshal(b, &file); e != nil {
		t.Fatal(e)
	}
	if file.Commit != UpstreamCommit || len(file.Commands) != 158 {
		t.Fatal("catalog changed without updating coverage")
	}
	seen := map[int]bool{}
	for _, r := range file.Commands {
		m, ok := LookupCommand(Command(r.ID))
		if !ok || m.Name != r.Name || m.Registered != r.Registered || seen[r.ID] {
			t.Fatalf("bad catalog record %+v", r)
		}
		seen[r.ID] = true
	}
	if _, ok := LookupCommand(6010); !ok {
		t.Fatal("literal firmware registration omitted")
	}
}

// Every generated method is exercised against the serial byte-stream simulator.
// This proves dispatch/status preservation, not successful execution on real cards.
func TestEveryWireCommand(t *testing.T) {
	for _, meta := range Commands() {
		if !meta.Registered || meta.ID == CmdEnterBootloader {
			continue
		}
		meta := meta
		t.Run(meta.Name, func(t *testing.T) {
			payload := []byte{0x12, 0x34}
			want := []byte{0xde, 0xad}
			c := readerSimulator(t, []readerStep{{command: uint16(meta.ID), request: payload, status: 0x67, data: want}})
			name := "Wire"
			for _, part := range strings.Split(meta.Name, "_") {
				name += part[:1] + strings.ToLower(part[1:])
			}
			method := reflect.ValueOf(c).MethodByName(name)
			if !method.IsValid() {
				t.Fatalf("missing generated %s", name)
			}
			vals := method.Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(payload)})
			if !vals[1].IsNil() {
				t.Fatal(vals[1].Interface())
			}
			r := vals[0].Interface().(Response)
			if r.Command != meta.ID || r.Status != 0x67 || !reflect.DeepEqual([]byte(r.Data), want) {
				t.Fatalf("%+v", r)
			}
			var status *StatusError
			if !errors.As(r.Check(0x68), &status) || status.Command != uint16(meta.ID) {
				t.Fatal("firmware failure not preserved")
			}
		})
	}
}
func TestParseCommand(t *testing.T) {
	for _, s := range []string{"get-app-version", "GET_APP_VERSION", "1000", "0x03e8"} {
		c, e := ParseCommand(s)
		if e != nil || c != CmdGetAppVersion {
			t.Fatalf("%s %d %v", s, c, e)
		}
	}
	for _, s := range []string{"-1", "65536", "blah", "1"} {
		if _, e := ParseCommand(s); e == nil {
			t.Fatal(s)
		}
	}
	a := Commands()
	a[0].Name = "changed"
	if Commands()[0].Name == "changed" {
		t.Fatal("catalog alias")
	}
}
func TestSettingsSchema13And14(t *testing.T) {
	for _, size := range []int{13, 14} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := make([]byte, size)
			data[0] = 7
			copy(data[7:], "123456")
			c := readerSimulator(t, []readerStep{{command: 1034, status: 0x68, data: data}})
			s, e := c.Settings(context.Background())
			if e != nil || s.BLEKey != "123456" || (s.SleepSeconds != nil) != (size == 14) {
				t.Fatalf("%+v %v", s, e)
			}
		})
	}
}
func TestExecuteUnknownOrUnregistered(t *testing.T) {
	c := readerSimulator(t, nil)
	for _, id := range []Command{999, 3032, 3007, 3008} {
		if _, e := c.Execute(context.Background(), id, nil); e == nil {
			t.Fatal(id)
		}
	}
	if c.Stats().Requests != 0 {
		t.Fatal("unsupported command sent")
	}
}
