package chameleon

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

// Only reads the active slot. Never selects another slot or writes settings.
func TestHardwareV7NoCard(t *testing.T) {
	if os.Getenv("CHAMELEON_TEST_NO_CARD") != "1" || os.Getenv("CHAMELEON_TEST_PORT") == "" {
		t.Skip("explicit empty-antenna hardware opt-in required")
	}
	c, err := Open(os.Getenv("CHAMELEON_TEST_PORT"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	before, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	image, err := c.ExportEmulator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		details, e := c.SlotDetails(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(details) != 8 {
			t.Fatal("expected eight slots")
		}
		view, e := c.SettingsView(ctx, false)
		if e != nil {
			t.Fatal(e)
		}
		if view.BLEKey != "" || len(view.Raw) != 0 {
			t.Fatal("settings secret exposed")
		}
		config, e := c.SlotEmulatorConfig(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		if config.Classic != nil {
			if _, e = c.SlotPRNG(ctx, 0, nil, false); e != nil {
				t.Fatal(e)
			}
		}
		memory, e := c.ExportSlotMemory(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		if memory.SHA256 != image.SHA256 {
			t.Fatal("memory mismatch")
		}
	}
	after, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterSettings, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterImage, err := c.ExportEmulator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode != after.Mode || !reflect.DeepEqual(before.Slots, after.Slots) || !reflect.DeepEqual(settings, afterSettings) || image.SHA256 != afterImage.SHA256 {
		t.Fatal("device state changed")
	}
	t.Logf("3 read cycles passed; active memory/settings/slots/mode unchanged; stats=%+v", c.Stats())
}
