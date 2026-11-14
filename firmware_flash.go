package chameleon

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/soulteary/chameleon-desk/dfu"
	"go.bug.st/serial"
)

// ReadFirmwareApplication loads one immutable application/init pair. Multi-image
// system/bootloader packages are rejected before opening a serial device.
func ReadFirmwareApplication(path string) (dfu.Image, error) {
	z, e := zip.OpenReader(path)
	if e != nil {
		return dfu.Image{}, e
	}
	defer z.Close()
	if len(z.File) > 32 {
		return dfu.Image{}, fmt.Errorf("too many ZIP entries")
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !fs.ValidPath(f.Name) || strings.Contains(f.Name, "\\") || files[f.Name] != nil {
			return dfu.Image{}, fmt.Errorf("invalid/duplicate DFU ZIP path")
		}
		files[f.Name] = f
	}
	read := func(name string, limit int) ([]byte, error) {
		f := files[name]
		if f == nil || f.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("missing or oversized DFU ZIP entry %q", name)
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		b, e := io.ReadAll(io.LimitReader(r, int64(limit)+1))
		if e != nil {
			return nil, e
		}
		if len(b) == 0 || len(b) > limit {
			return nil, fmt.Errorf("invalid DFU ZIP entry length")
		}
		return b, nil
	}
	b, e := read("manifest.json", 65536)
	if e != nil {
		return dfu.Image{}, e
	}
	var m struct {
		Manifest map[string]struct {
			Binary string `json:"bin_file"`
			Init   string `json:"dat_file"`
		} `json:"manifest"`
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return dfu.Image{}, e
	}
	part, ok := m.Manifest["application"]
	if !ok || len(m.Manifest) != 1 {
		return dfu.Image{}, fmt.Errorf("native flash requires an application-only Nordic DFU package; system components are not accepted")
	}
	data, e := read(part.Binary, 2<<20)
	if e != nil {
		return dfu.Image{}, e
	}
	init, e := read(part.Init, 65536)
	if e != nil {
		return dfu.Image{}, e
	}
	return dfu.Image{Init: init, Firmware: data}, nil
}

// FlashFirmware requires an already enumerated USB DFU port. It never guesses
// a port, enters bootloader, downloads firmware or changes firmware protection.
func FlashFirmware(ctx context.Context, dfuPort, zipFile string, options dfu.Options) (dfu.Result, error) {
	image, e := ReadFirmwareApplication(zipFile)
	if e != nil {
		return dfu.Result{}, e
	}
	if strings.HasPrefix(dfuPort, "ble:") {
		setup, cancel := context.WithTimeout(ctx, 30*time.Second)
		p, e := nativeBLEDFUOpen(setup, strings.TrimPrefix(dfuPort, "ble:"), options.Timeout)
		cancel()
		if e != nil {
			return dfu.Result{}, e
		}
		return dfu.UploadBLE(ctx, p, image, options)
	}
	if strings.TrimSpace(dfuPort) == "" || strings.HasPrefix(dfuPort, "ble:") || strings.HasPrefix(dfuPort, "tcp:") {
		return dfu.Result{}, fmt.Errorf("an explicit USB DFU serial port is required")
	}
	p, e := serial.Open(dfuPort, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if e != nil {
		return dfu.Result{}, e
	}
	fail := func(err error) (dfu.Result, error) { _ = p.Close(); return dfu.Result{}, err }
	if e = p.SetDTR(true); e != nil {
		return fail(e)
	}
	_ = p.SetRTS(true)
	if e = p.SetReadTimeout(100 * time.Millisecond); e != nil {
		return fail(e)
	}
	if e = p.ResetInputBuffer(); e != nil {
		return fail(e)
	}
	return dfu.Upload(ctx, p, image, options)
}
