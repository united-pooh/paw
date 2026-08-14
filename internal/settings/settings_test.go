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

func TestDefaultConfigEnablesReasonixContextMaintenance(t *testing.T) {
	cfg := DefaultConfig()
	got := cfg.ContextMaintenance
	if got.SoftCompactRatio != 0.50 || got.ToolResultSnipRatio != 0.60 ||
		got.CompactRatio != 0.80 || got.CompactForceRatio != 0.90 ||
		got.CompactTargetRatio != 0.50 {
		t.Fatalf("unexpected ratios: %+v", got)
	}
	if got.TailTokens != 16384 || got.MinToolResultBytes != 1024 {
		t.Fatalf("unexpected budgets: %+v", got)
	}
	if !got.KeepErrors || !got.KeepUserMarked || !got.ArchiveEnabled {
		t.Fatalf("maintenance must default on: %+v", got)
	}
}

func TestLoadRejectsInvalidContextMaintenanceOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{
      "context_maintenance": {
        "soft_compact_ratio": 0.7,
        "tool_result_snip_ratio": 0.6,
        "compact_ratio": 0.8,
        "compact_force_ratio": 0.9,
        "compact_target_ratio": 0.5,
        "tail_tokens": 16384,
        "min_tool_result_bytes": 1024,
        "keep_errors": true,
        "keep_user_marked": true,
        "archive_enabled": true
      }
    }`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "soft_compact_ratio") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestContextMaintenanceRoundTripAndMissingDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := DefaultConfig()
	want.ContextMaintenance = ContextMaintenanceConfig{
		SoftCompactRatio: 0.45, ToolResultSnipRatio: 0.55,
		CompactRatio: 0.75, CompactForceRatio: 0.88,
		CompactTargetRatio: 0.40, TailTokens: 8192,
		MinToolResultBytes: 2048, KeepErrors: false,
		KeepUserMarked: false, ArchiveEnabled: false,
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextMaintenance != want.ContextMaintenance {
		t.Fatalf("context maintenance = %+v, want %+v", got.ContextMaintenance, want.ContextMaintenance)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacyPath, []byte(`{"ui":{"theme":"default"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := Load(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ContextMaintenance != DefaultContextMaintenanceConfig() {
		t.Fatalf("legacy context maintenance = %+v", legacy.ContextMaintenance)
	}
}

func TestLoadRejectsExplicitZeroContextMaintenanceBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"context_maintenance":{"soft_compact_ratio":0.5,"tool_result_snip_ratio":0.6,"compact_ratio":0.8,"compact_force_ratio":0.9,"compact_target_ratio":0.5,"tail_tokens":0,"min_tool_result_bytes":1024,"keep_errors":true,"keep_user_marked":true,"archive_enabled":true}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "budgets") {
		t.Fatalf("Load() error = %v", err)
	}
}

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

func TestTranslateOnDoubleClickDefaultsOffAndRoundTrips(t *testing.T) {
	if DefaultConfig().UI.TranslateOnDoubleClick {
		t.Fatal("TranslateOnDoubleClick must default to false")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	want := DefaultConfig()
	want.UI.TranslateOnDoubleClick = true
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UI.TranslateOnDoubleClick {
		t.Fatal("TranslateOnDoubleClick did not survive round trip")
	}

	// 旧 settings.json 缺该字段 → 零值 false，天然兼容。
	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacyPath, []byte(`{"ui":{"theme":"default"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := Load(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.UI.TranslateOnDoubleClick {
		t.Fatal("legacy settings must default TranslateOnDoubleClick to false")
	}
}

func TestSaveLoadAndControllerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paw", "settings.json")
	want := DefaultConfig()
	want.Subagent = SubagentConfig{
		DefaultContextMode: ContextModeFork,
		DefaultRunMode:     RunModeBackground,
		WaitTimeoutMs:      DefaultSubagentWaitTimeoutMs,
	}
	want.UI = UIConfig{
		Theme:                theme.Default,
		ContextLimitTokens:   200000,
		ContextMeterLocation: MeterLocationInputAbove,
		TranscriptOutputMode: TranscriptOutputModeLine,
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

	next := DefaultConfig()
	next.Subagent = SubagentConfig{
		DefaultContextMode: ContextModeEmpty,
		DefaultRunMode:     RunModeSync,
		WaitTimeoutMs:      DefaultSubagentWaitTimeoutMs,
	}
	next.UI = UIConfig{
		Theme:                theme.Default,
		ContextLimitTokens:   DefaultContextLimitTokens,
		ContextMeterLocation: MeterLocationInputAbove,
		TranscriptOutputMode: TranscriptOutputModeLine,
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

func TestTranscriptAnimationSettingsDefaultsNormalizeAndRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.UI.TranscriptOutputMode != TranscriptOutputModeLine {
		t.Fatalf("default output mode = %q, want %q", cfg.UI.TranscriptOutputMode, TranscriptOutputModeLine)
	}
	cfg.UI.TranscriptOutputMode = " CHAR "
	cfg = Normalize(cfg)
	if cfg.UI.TranscriptOutputMode != TranscriptOutputModeChar {
		t.Fatalf("normalized transcript settings = %+v", cfg.UI)
	}
	cfg.UI.TranscriptOutputMode = "invalid"
	cfg = Normalize(cfg)
	if cfg.UI.TranscriptOutputMode != TranscriptOutputModeLine {
		t.Fatalf("invalid transcript settings = %+v", cfg.UI)
	}

	path := filepath.Join(t.TempDir(), "settings.json")
	want := DefaultConfig()
	want.UI.TranscriptOutputMode = TranscriptOutputModeChar
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UI.TranscriptOutputMode != want.UI.TranscriptOutputMode {
		t.Fatalf("round trip transcript settings = %+v, want %+v", got.UI, want.UI)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacyPath, []byte(`{"ui":{"theme":"default"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := Load(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.UI.TranscriptOutputMode != TranscriptOutputModeLine {
		t.Fatalf("legacy transcript settings = %+v", legacy.UI)
	}
}
func TestDefaultCompressionConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ContextCompression.Mode != CompressionModeState {
		t.Fatalf("default mode must be state, got %q", cfg.ContextCompression.Mode)
	}
	if cfg.ContextCompression.ResumeRecentTurns != 3 {
		t.Fatalf("default resume turns: %d", cfg.ContextCompression.ResumeRecentTurns)
	}
	if cfg.ContextCompression.StateCompactionRatio != 0.9 {
		t.Fatalf("default ratio: %v", cfg.ContextCompression.StateCompactionRatio)
	}
}

func TestCompressionNormalizeAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := Save(path, Config{ContextCompression: ContextCompressionConfig{
		Mode:              "bogus",
		ResumeRecentTurns: 0,
	}}); err != nil {
		t.Fatalf("save must normalize: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ContextCompression.Mode != CompressionModeState {
		t.Fatalf("bogus mode must normalize to state: %q", cfg.ContextCompression.Mode)
	}
	if cfg.ContextCompression.ResumeRecentTurns != 3 {
		t.Fatalf("zero turns must normalize to 3: %d", cfg.ContextCompression.ResumeRecentTurns)
	}

	// summary 合法保留。
	if err := Save(path, Config{ContextCompression: ContextCompressionConfig{
		Mode: "summary", ResumeRecentTurns: 5, StateCompactionRatio: 0.8,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load(path)
	if cfg.ContextCompression.Mode != CompressionModeSummary || cfg.ContextCompression.ResumeRecentTurns != 5 {
		t.Fatalf("summary mode must survive: %+v", cfg.ContextCompression)
	}

	// 非法 ratio 拒绝。
	bad := Config{ContextCompression: ContextCompressionConfig{
		Mode: CompressionModeState, ResumeRecentTurns: 3, StateCompactionRatio: 1.5,
	}}
	if err := Validate(bad); err == nil {
		t.Fatal("ratio >= 1 must be rejected")
	}
}
