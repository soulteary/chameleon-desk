package chameleon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soulteary/chameleon-desk/dfu"
)

func TestNativeBLEEntryPointsRequireBridge(t *testing.T) {
	for name, call := range map[string]func() error{
		"scan": func() error { _, err := ScanBLE(context.Background(), time.Second); return err },
		"open": func() error { _, err := OpenBLE(context.Background(), "old-peripheral", time.Second); return err },
		"address": func() error {
			_, err := OpenContext(context.Background(), "ble:old-peripheral", time.Second)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrBLEUnavailable) || !strings.Contains(err.Error(), "tcp:HOST:PORT") {
				t.Fatalf("missing migration error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenBLE(ctx, "old-peripheral", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ScanBLE(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFirmwareRejectsWirelessBeforeReadingArchive(t *testing.T) {
	for _, port := range []string{"", "ble:old-peripheral", "tcp:127.0.0.1:1"} {
		if _, err := FlashFirmware(context.Background(), port, "missing-archive.zip", dfu.Options{}); err == nil || !strings.Contains(err.Error(), "USB DFU serial") {
			t.Fatalf("%q: %v", port, err)
		}
	}
}
