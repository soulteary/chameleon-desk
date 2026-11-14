package chameleon

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func packageZIP(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range entries {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "fw.zip")
	if e := os.WriteFile(path, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestFirmwareInspect(t *testing.T) {
	manifest := []byte(`{"manifest":{"application":{"bin_file":"app.bin","dat_file":"app.dat"}}}`)
	path := packageZIP(t, map[string][]byte{"manifest.json": manifest, "app.bin": {1, 2, 3}, "app.dat": {4}})
	p, e := InspectFirmware(path)
	if e != nil || len(p.Parts) != 1 || p.Parts[0].Bytes != 3 || p.SignatureVerified {
		t.Fatalf("%+v %v", p, e)
	}
}
func TestFirmwareMissingOrUnsafe(t *testing.T) {
	for _, m := range []map[string][]byte{{"manifest.json": []byte(`{"manifest":{"application":{"bin_file":"missing","dat_file":"x"}}}`)}, {"../escape": {1}, "manifest.json": []byte(`{"manifest":{}}`)}, {"manifest.json": []byte("not-json")}} {
		if _, e := InspectFirmware(packageZIP(t, m)); e == nil {
			t.Fatal("invalid ZIP accepted")
		}
	}
}
func TestFirmwareUploadSnapshot(t *testing.T) {
	m := []byte(`{"manifest":{"application":{"bin_file":"app.bin","dat_file":"app.dat"}}}`)
	p := packageZIP(t, map[string][]byte{"manifest.json": m, "app.bin": {1, 2}, "app.dat": {3}})
	v, e := ReadFirmwareApplication(p)
	if e != nil || len(v.Firmware) != 2 || v.Init[0] != 3 {
		t.Fatal(v, e)
	}
	for _, manifest := range []string{`{"manifest":{}}`, `{"manifest":{"softdevice":{"bin_file":"app.bin","dat_file":"app.dat"}}}`, `{"manifest":{"application":{"bin_file":"missing","dat_file":"app.dat"}}}`} {
		p = packageZIP(t, map[string][]byte{"manifest.json": []byte(manifest), "app.bin": {1}, "app.dat": {2}})
		if _, e = ReadFirmwareApplication(p); e == nil {
			t.Fatal(manifest)
		}
	}
}
