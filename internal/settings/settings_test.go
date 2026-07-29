package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"paw/internal/theme"
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

func TestNormalizeLegacyInputTitleMeterLocation(t *testing.T) {
	cfg := Normalize(Config{
		Subagent: SubagentConfig{
			DefaultContextMode: ContextModeEmpty,
			DefaultRunMode:     RunModeSync,
		},
		UI: UIConfig{
			Theme:                theme.Default,
			ContextLimitTokens:   DefaultContextLimitTokens,
			ContextMeterLocation: MeterLocationInputTitle,
		},
	})
	if cfg.UI.ContextMeterLocation != MeterLocationInputAbove {
		t.Fatalf("ContextMeterLocation = %q, want %q", cfg.UI.ContextMeterLocation, MeterLocationInputAbove)
	}
}

func TestSaveLoadAndControllerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paw", "settings.json")
	want := Config{
		Subagent: SubagentConfig{
			DefaultContextMode: ContextModeFork,
			DefaultRunMode:     RunModeBackground,
		},
		UI: UIConfig{
			Theme:                theme.Default,
			ContextLimitTokens:   200000,
			ContextMeterLocation: MeterLocationInputAbove,
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
			Theme:                theme.Default,
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

func TestDefaultSettingsPathUsesHomeDirectory(t *testing.T) {
	got, err := DefaultPath(func() (string, error) { return "/tmp/paw-home", nil })
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/paw-home", ".paw", "settings.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultSettingsPathPropagatesHomeError(t *testing.T) {
	wantErr := errors.New("no home")
	_, err := DefaultPath(func() (string, error) { return "", wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestNormalizeTheme(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UI.Theme = " TOKYO-NIGHT "
	if got := Normalize(cfg).UI.Theme; got != theme.TokyoNight {
		t.Fatalf("theme = %q", got)
	}
	cfg.UI.Theme = "not-installed"
	if got := Normalize(cfg).UI.Theme; got != theme.Default {
		t.Fatalf("theme = %q", got)
	}
}

func TestNewDefaultControllerIgnoresProjectSettings(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Chdir(project)
	if err := os.MkdirAll(filepath.Join(project, ".paw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".paw", "settings.json"), []byte(`{"ui":{"theme":"dracula"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, err := NewDefaultController(func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := controller.CurrentSettings().UI.Theme; got != theme.Default {
		t.Fatalf("theme = %q", got)
	}
}

func TestLoadLegacyGlobalJSONDefaultsTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"subagent":{"default_context_mode":"fork","default_run_mode":"sync"},"ui":{"context_limit_tokens":128000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != theme.Default {
		t.Fatalf("theme = %q", cfg.UI.Theme)
	}
}

func TestLoadDamagedJSONIncludesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want path", err)
	}
}

func TestSaveCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paw", "settings.json")
	if err := Save(path, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 600", got)
		}
	}
}
