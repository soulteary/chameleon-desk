package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func interfaceRequest(t *testing.T, ws *webServer, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8088"+path, bytes.NewReader(raw))
	r.Header.Set("X-Chameleon-Token", ws.token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNdefCLIWebAndSDKRoundTrip(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			dir := t.TempDir()
			input, output, readOutput := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json"), filepath.Join(dir, "records.json")
			card := ntagCard()
			card["custom"] = "preserved"
			writeJSONFile(t, input, card)
			if err := runCardTool([]string{"ndef", "write", "--input", input, "--output", output, "--text", "你好", "--language", language, "--uri", "https://example.com"}); err != nil {
				t.Fatal(err)
			}
			ws := &webServer{}
			h := offlineWeb(t, ws)
			written := interfaceRequest(t, ws, h, "/api/ndef/write", map[string]any{"card": card, "text": "你好", "language": language, "uri": "https://example.com"})
			if written.Code != 200 {
				t.Fatal(written.Code, written.Body.String())
			}
			cli, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var cliCard, webCard map[string]any
			if err := json.Unmarshal(cli, &cliCard); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(written.Body.Bytes(), &webCard); err != nil || !reflect.DeepEqual(cliCard, webCard) {
				t.Fatalf("CLI and Web card output differs: %v", err)
			}
			if err := runCardTool([]string{"ndef", "read", "--input", output, "--output", readOutput}); err != nil {
				t.Fatal(err)
			}
			read := interfaceRequest(t, ws, h, "/api/ndef/read", webCard)
			var webRead, cliRead map[string]any
			rawRead, _ := os.ReadFile(readOutput)
			if err := json.Unmarshal(rawRead, &cliRead); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(read.Body.Bytes(), &webRead); err != nil || read.Code != 200 || !reflect.DeepEqual(cliRead, webRead) {
				t.Fatalf("read results differ: CLI=%s Web=%s (%v)", rawRead, read.Body.String(), err)
			}
			sdkCard, err := chameleon.ParseGUICard(cli)
			if err != nil {
				t.Fatal(err)
			}
			container, err := chameleon.DetectNdef(sdkCard.Tag, sdkCard.Data)
			if err != nil {
				t.Fatal(err)
			}
			records, err := chameleon.DecodeNdefMessage(container.Message())
			if err != nil || len(records) != 2 || records[0].Lang != language || records[0].Value != "你好" || records[1].Value != "https://example.com" {
				t.Fatalf("SDK round trip: %+v %v", records, err)
			}
		})
	}
}

func TestNdefInputModesRejectConflictsAcrossInterfaces(t *testing.T) {
	record, _ := chameleon.NewNdefText("text", "en")
	message, _ := chameleon.EncodeNdefMessage([]chameleon.NdefRecord{record})
	for _, mode := range []string{"raw-text", "records-text", "records-raw", "empty-records", "null-records", "invalid-language"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input, output, records := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json"), filepath.Join(dir, "records.json")
			card := ntagCard()
			writeJSONFile(t, input, card)
			writeJSONFile(t, records, []chameleon.NdefRecord{record})
			body := map[string]any{"card": card}
			args := []string{"ndef", "write", "--input", input, "--output", output}
			if strings.HasPrefix(mode, "records-") || mode == "empty-records" || mode == "null-records" {
				body["records"] = []chameleon.NdefRecord{record}
				args = append(args, "--records", records)
			}
			if mode == "raw-text" || mode == "records-raw" {
				body["message_hex"] = hex.EncodeToString(message)
				args = append(args, "--message-hex", hex.EncodeToString(message))
			}
			if mode == "raw-text" || mode == "records-text" || mode == "invalid-language" {
				body["text"] = "text"
				args = append(args, "--text", "text")
			}
			if mode == "empty-records" {
				body["records"] = []chameleon.NdefRecord{}
				writeJSONFile(t, records, []chameleon.NdefRecord{})
			}
			if mode == "null-records" {
				body["records"] = nil
				writeJSONFile(t, records, nil)
			}
			if mode == "invalid-language" {
				body["language"] = "中文"
				args = append(args, "--language", "中文")
			}
			if err := runCardTool(args); err == nil {
				t.Fatal("CLI accepted invalid NDEF input")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("failed write published an output", err)
			}
			ws := &webServer{}
			w := interfaceRequest(t, ws, offlineWeb(t, ws), "/api/ndef/write", body)
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestNdefExplicitInputModesMatchSDKEncoding(t *testing.T) {
	text, _ := chameleon.NewNdefText("text", "en")
	mime, _ := chameleon.NewNdefMime("text/plain", "value")
	external, _ := chameleon.NewNdefExternal("example.com:type", "value")
	for _, mode := range []string{"records", "raw", "media"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
			card := ntagCard()
			writeJSONFile(t, input, card)
			records := []chameleon.NdefRecord{text}
			if mode == "media" {
				records = []chameleon.NdefRecord{mime, external}
			}
			want, err := chameleon.EncodeNdefMessage(records)
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"card": card}
			args := []string{"ndef", "write", "--input", input, "--output", output}
			switch mode {
			case "records":
				path := filepath.Join(dir, "records.json")
				writeJSONFile(t, path, records)
				args = append(args, "--records", path)
				body["records"] = records
			case "raw":
				args = append(args, "--message-hex", hex.EncodeToString(want))
				body["message_hex"] = hex.EncodeToString(want)
			case "media":
				args = append(args, "--mime", "text/plain", "--external", "example.com:type", "--value", "value")
				body["mime"], body["external"], body["value"] = "text/plain", "example.com:type", "value"
			}
			if err := runCardTool(args); err != nil {
				t.Fatal(err)
			}
			cli, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			ws := &webServer{}
			w := interfaceRequest(t, ws, offlineWeb(t, ws), "/api/ndef/write", body)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, raw := range [][]byte{cli, w.Body.Bytes()} {
				out, err := chameleon.ParseGUICard(raw)
				if err != nil {
					t.Fatal(err)
				}
				container, err := chameleon.DetectNdef(out.Tag, out.Data)
				if err != nil || !bytes.Equal(container.Message(), want) {
					t.Fatalf("%s wire message differs: %v", mode, err)
				}
			}
		})
	}
}

func TestEmptyNdefReadShapeMatchesAcrossInterfaces(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "card.json"), filepath.Join(dir, "read.json")
	card := ntagCard()
	writeJSONFile(t, input, card)
	if err := runCardTool([]string{"ndef", "read", "--input", input, "--output", output}); err != nil {
		t.Fatal(err)
	}
	cli, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{}
	w := interfaceRequest(t, ws, offlineWeb(t, ws), "/api/ndef/read", card)
	var cliRead, webRead map[string]any
	if err := json.Unmarshal(cli, &cliRead); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &webRead); err != nil || w.Code != 200 || !reflect.DeepEqual(cliRead, webRead) {
		t.Fatalf("empty read differs: %s / %s: %v", cli, w.Body.String(), err)
	}
	if records, ok := cliRead["records"].([]any); !ok || len(records) != 0 || cliRead["message_hex"] != "" {
		t.Fatal(cliRead)
	}
}

func TestVanityValidationAcrossInterfaces(t *testing.T) {
	for _, uid := range []string{"GGGGGGGG", "0411223Z", "0411223344556Z", "0411", " 04\t11\n22\r33 "} {
		t.Run(fmt.Sprintf("%q", uid), func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "uid.json")
			err := runCardTool([]string{"vanity", "sak", "--uid", uid, "--output", output})
			ws := &webServer{}
			w := interfaceRequest(t, ws, offlineWeb(t, ws), "/api/vanity", map[string]string{"uid": uid})
			if chameleon.VanitySAKFromUIDHex(uid) == nil {
				if err == nil || w.Code != 422 {
					t.Fatalf("invalid UID accepted: CLI=%v HTTP=%d", err, w.Code)
				}
			} else if err != nil || w.Code != 200 || !strings.Contains(w.Body.String(), "04112233") {
				t.Fatalf("valid UID rejected: CLI=%v HTTP=%d %s", err, w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidWebDeviceInputsDoNotOpenTransport(t *testing.T) {
	opened := 0
	ws := &webServer{allowWrites: true, port: "simulated", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		opened++
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	for _, slot := range []int{-1, 0, 9} {
		for _, path := range []string{"/api/slot/activate", "/api/slot/load-card"} {
			body := map[string]any{"slot": slot}
			if path == "/api/slot/load-card" {
				body["card"] = ntagCard()
			}
			if w := interfaceRequest(t, ws, h, path, body); w.Code != 400 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		if err := runExtendedWith([]string{"slot", "select", "--slot", fmt.Sprint(slot), "--yes", "--port", "simulated"}, noOpen(t)); err == nil || !strings.Contains(err.Error(), "1..8") {
			t.Fatalf("CLI slot validation reached transport: %v", err)
		}
	}
	badCard := ntagCard()
	badCard["tag"] = 3001
	if w := interfaceRequest(t, ws, h, "/api/slot/load-card", map[string]any{"slot": 2, "card": badCard}); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := interfaceRequest(t, ws, h, "/api/firmware/check", map[string]string{"model": "unknown"}); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := runFirmwareUpdate([]string{"firmware", "check", "--model", "unknown", "--port", "tcp:127.0.0.1:0"}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("invalid firmware model reached transport: %v", err)
	}
	if opened != 0 {
		t.Fatalf("invalid requests opened transport %d times", opened)
	}
}
