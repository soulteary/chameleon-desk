package chameleon

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

// Explicit opt-in; never executed by ordinary unit tests.
func TestHardwareNoCard(t *testing.T) {
	if os.Getenv("CHAMELEON_TEST_NO_CARD") != "1" {
		t.Skip("set CHAMELEON_TEST_NO_CARD=1 and CHAMELEON_TEST_PORT, with antennas empty")
	}
	port := os.Getenv("CHAMELEON_TEST_PORT")
	if port == "" {
		t.Fatal("CHAMELEON_TEST_PORT is required")
	}
	c, e := Open(port, 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	before, e := c.Info(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings, e := c.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	image, e := c.ExportEmulator(ctx)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := c.AntiCollision(ctx)
	if e != nil {
		t.Fatal(e)
	}
	lfBefore, e := c.EmulatorLFID(ctx, "em410x")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100; i++ {
		_, e = c.ScanISO14443A(ctx)
		var restore *ModeRestoreError
		if !errors.Is(e, ErrNoCard) || errors.As(e, &restore) {
			t.Fatalf("HF round %d: %v", i+1, e)
		}
		if (i+1)%20 == 0 {
			t.Logf("HF no-card %d/100", i+1)
		}
	}
	for i := 0; i < 10; i++ {
		_, e = c.ScanLF(ctx, "em410x", 1)
		var restore *ModeRestoreError
		if !errors.Is(e, ErrNoCard) || errors.As(e, &restore) {
			t.Fatalf("LF round %d: %v", i+1, e)
		}
	}
	after, e := c.Info(ctx)
	if e != nil {
		t.Fatal(e)
	}
	afterSettings, e := c.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	afterImage, e := c.ExportEmulator(ctx)
	if e != nil {
		t.Fatal(e)
	}
	afterIdentity, e := c.AntiCollision(ctx)
	if e != nil {
		t.Fatal(e)
	}
	lfAfter, e := c.EmulatorLFID(ctx, "em410x")
	if e != nil {
		t.Fatal(e)
	}
	if before.Mode != after.Mode || !reflect.DeepEqual(before.Slots, after.Slots) || !reflect.DeepEqual(settings, afterSettings) || image.SHA256 != afterImage.SHA256 || !reflect.DeepEqual(identity, afterIdentity) || !reflect.DeepEqual(lfBefore, lfAfter) {
		t.Fatal("device mode/settings/slots/active HF memory or LF ID changed")
	}
	stats := c.Stats()
	if stats.Requests != stats.Responses || stats.RejectedFrames != 0 || stats.UnexpectedResponses != 0 {
		t.Fatalf("transport anomaly: %+v", stats)
	}
	t.Logf("100 HF + 10 LF no-card probes passed; mode=%s; slots/settings/active HF memory/identity/LF ID unchanged; active-memory SHA256=%s; stats=%+v", after.Mode, image.SHA256, stats)
}
