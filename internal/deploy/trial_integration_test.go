package deploy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RinoPaw/Rino-Proxy/internal/core"
)

// Explicit opt-in integration test. It only accepts the dedicated disposable
// instance ports; it cannot accidentally target Clash Verge's active core.
func TestLiveTrialAgainstIsolatedMihomo(t *testing.T) {
	if os.Getenv("RINO_TEST_ISOLATED") != "1" {
		t.Skip("requires explicit disposable instance")
	}
	const controller = "http://127.0.0.1:29794"
	state := os.Getenv("RINO_TEST_ISOLATED_STATE")
	if state == "" {
		t.Fatal("missing isolated archive path")
	}
	client, err := core.NewClient(controller, "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Config.MixedPort != 29793 || before.Config.Mode != "rule" || before.Config.TUNEnabled || before.Config.AllowLAN {
		t.Fatalf("unsafe starting state: %+v", before.Config)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	// API-only integration: internet reachability is separately tested by
	// the real CLI probe. This verifies a real PUT -> GET -> PUT round-trip.
	result, err := Trial(ctx, state, client, time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.Healthy || !result.Restored {
		t.Fatalf("unexpected live result: %+v", result)
	}
	after, err := client.Snapshot(context.Background())
	if err != nil || after.Config.Mode != "rule" {
		t.Fatalf("baseline not restored: %+v, %v", after.Config, err)
	}
}
