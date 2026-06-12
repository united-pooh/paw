package settings

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileReturnsDefaultConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "settings.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("Load(%q) = %#v, want default %#v", path, cfg, DefaultConfig())
	}
}

func TestNormalizeRestoresSupportedDefaults(t *testing.T) {
	cfg := Normalize(Config{
		Subagent: SubagentConfig{
			DefaultContextMode: ContextMode("bad-context"),
			DefaultRunMode:     RunMode("bad-run"),
		},
		UI: UIConfig{
			ContextLimitTokens:   -1,
			ContextMeterLocation: MeterLocation("bad-location"),
		},
	})
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("Normalize() = %#v, want %#v", cfg, DefaultConfig())
	}
}

func TestSaveLoadAndControllerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ccagent", "settings.json")
	want := Config{
		Subagent: SubagentConfig{
			DefaultContextMode: ContextModeFork,
			DefaultRunMode:     RunModeBackground,
		},
		UI: UIConfig{
			ContextLimitTokens:   200000,
			ContextMeterLocation: MeterLocationHeader,
		},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save(%q) error = %v", path, err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("Load(%q) = %#v, want %#v", path, loaded, want)
	}

	controller, err := NewController(path)
	if err != nil {
		t.Fatalf("NewController(%q) error = %v", path, err)
	}
	if !reflect.DeepEqual(controller.CurrentSettings(), want) {
		t.Fatalf("CurrentSettings() = %#v, want %#v", controller.CurrentSettings(), want)
	}

	next := Config{
		Subagent: SubagentConfig{
			DefaultContextMode: ContextModeEmpty,
			DefaultRunMode:     RunModeSync,
		},
		UI: UIConfig{
			ContextLimitTokens:   DefaultContextLimitTokens,
			ContextMeterLocation: MeterLocationInputAbove,
		},
	}
	if err := controller.SaveSettings(next); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	if !reflect.DeepEqual(controller.CurrentSettings(), next) {
		t.Fatalf("CurrentSettings() = %#v, want %#v", controller.CurrentSettings(), next)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) after SaveSettings error = %v", path, err)
	}
	if !reflect.DeepEqual(reloaded, next) {
		t.Fatalf("Load(%q) after SaveSettings = %#v, want %#v", path, reloaded, next)
	}
}
