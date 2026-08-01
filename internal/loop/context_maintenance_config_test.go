package loop

import (
	"testing"

	"paw/internal/settings"
)

func TestContextMaintenanceConfigFromSettings(t *testing.T) {
	in := settings.DefaultContextMaintenanceConfig()
	got, err := contextMaintenanceConfigFromSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.tailTokens != 16384 || !got.keepErrors || !got.archiveEnabled {
		t.Fatalf("mapped config = %+v", got)
	}
}
