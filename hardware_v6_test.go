package chameleon

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestHardwareV6NoCard(t *testing.T) {
	if os.Getenv("CHAMELEON_TEST_NO_CARD") != "1" || os.Getenv("CHAMELEON_TEST_PORT") == "" {
		t.Skip("explicit empty-antenna hardware opt-in required")
	}
	c, e := Open(os.Getenv("CHAMELEON_TEST_PORT"), 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
	config, e := c.ReaderConfig(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.CheckClassicDictionary(ctx, 16, []HexBytes{{255, 255, 255, 255, 255, 255}})
	if !errors.Is(e, ErrNoCard) {
		t.Fatalf("dictionary without card: %v", e)
	}
	_, e = c.DESFireInfo(ctx)
	var status *StatusError
	if e == nil || !errors.Is(e, ErrNoCard) && !(errors.As(e, &status) && status.Status == 1) {
		t.Fatalf("ISO4 without card: %v", e)
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
	if before.Mode != after.Mode || !reflect.DeepEqual(before.Slots, after.Slots) || !reflect.DeepEqual(settings, afterSettings) || image.SHA256 != afterImage.SHA256 {
		t.Fatal("device state changed")
	}
	t.Logf("dictionary/ISO4 no-card cleanup passed; reader config=%+v; settings/slots/active-memory unchanged; stats=%+v", config, c.Stats())
}
