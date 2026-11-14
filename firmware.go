package chameleon

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

type FirmwarePart struct {
	Kind       string `json:"kind"`
	Binary     string `json:"binary"`
	InitPacket string `json:"init_packet"`
	Bytes      int    `json:"bytes"`
	SHA256     string `json:"sha256"`
}
type FirmwarePackage struct {
	Parts             []FirmwarePart `json:"parts"`
	SignatureVerified bool           `json:"signature_verified"`
}

// InspectFirmware validates a Nordic DFU ZIP offline. It does not authenticate
// the signature or flash the device. Firmware flashing remains an external DFU
// tool operation after explicit EnterBootloader.
func InspectFirmware(filename string) (FirmwarePackage, error) {
	z, e := zip.OpenReader(filename)
	if e != nil {
		return FirmwarePackage{}, e
	}
	defer z.Close()
	if len(z.File) > 32 {
		return FirmwarePackage{}, fmt.Errorf("too many ZIP entries")
	}
	entries := map[string]*zip.File{}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !fs.ValidPath(f.Name) || strings.Contains(f.Name, "\\") || entries[f.Name] != nil {
			return FirmwarePackage{}, fmt.Errorf("invalid/duplicate ZIP path %q", f.Name)
		}
		entries[f.Name] = f
	}
	read := func(name string, max int) ([]byte, error) {
		f := entries[name]
		if f == nil {
			return nil, fmt.Errorf("missing ZIP entry %q", name)
		}
		if f.UncompressedSize64 > uint64(max) {
			return nil, fmt.Errorf("ZIP entry %q exceeds size limit", name)
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		b, e := io.ReadAll(io.LimitReader(r, int64(max)+1))
		if e != nil {
			return nil, e
		}
		if len(b) > max {
			return nil, fmt.Errorf("ZIP entry too large")
		}
		return b, nil
	}
	b, e := read("manifest.json", 65536)
	if e != nil {
		return FirmwarePackage{}, e
	}
	var manifest struct {
		Manifest map[string]struct {
			Binary string `json:"bin_file"`
			Init   string `json:"dat_file"`
		} `json:"manifest"`
	}
	if e = json.Unmarshal(b, &manifest); e != nil {
		return FirmwarePackage{}, e
	}
	if len(manifest.Manifest) == 0 {
		return FirmwarePackage{}, fmt.Errorf("empty DFU manifest")
	}
	kinds := []string{}
	for kind := range manifest.Manifest {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := FirmwarePackage{Parts: []FirmwarePart{}}
	for _, kind := range kinds {
		switch kind {
		case "application", "bootloader", "softdevice", "softdevice_bootloader":
		default:
			return FirmwarePackage{}, fmt.Errorf("unsupported DFU component %q", kind)
		}
		part := manifest.Manifest[kind]
		data, e := read(part.Binary, 2<<20)
		if e != nil {
			return FirmwarePackage{}, e
		}
		init, e := read(part.Init, 65536)
		if e != nil {
			return FirmwarePackage{}, e
		}
		if len(data) == 0 || len(init) == 0 {
			return FirmwarePackage{}, fmt.Errorf("empty DFU component")
		}
		sum := sha256.Sum256(data)
		out.Parts = append(out.Parts, FirmwarePart{kind, part.Binary, part.Init, len(data), hex.EncodeToString(sum[:])})
	}
	return out, nil
}
