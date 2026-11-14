package chameleon

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// Explicitly opt in after placing a card: CHAMELEON_TEST_PORT=/dev/cu... go test
// -run TestHardwareScan -v . . Ordinary go test never opens real hardware.
func TestHardwareScan(t *testing.T) {
	port := os.Getenv("CHAMELEON_TEST_PORT")
	if port == "" || os.Getenv("CHAMELEON_TEST_NO_CARD") == "1" {
		t.Skip("set CHAMELEON_TEST_PORT and place a card to enable hardware test")
	}
	c, err := Open(port, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	original, err := c.Mode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first []Card
	for i := 0; i < 20; i++ {
		cards, scanErr := c.ScanISO14443A(ctx)
		mode, modeErr := c.Mode(context.Background())
		if modeErr != nil || mode != original {
			t.Fatalf("round %d mode not restored: %q %v", i+1, mode, modeErr)
		}
		if scanErr != nil {
			t.Fatalf("round %d scan: %v", i+1, scanErr)
		}
		if first == nil {
			first = cards
		} else if !reflect.DeepEqual(first, cards) {
			t.Fatalf("round %d card identification changed", i+1)
		}
	}
	data, _ := json.Marshal(first)
	t.Logf("20/20 identical card scans; mode restored to %s after every scan; cards=%s; stats=%+v", original, data, c.Stats())
}
