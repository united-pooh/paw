package bubble

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	configv2 "paw/internal/config"
	modelcfg "paw/internal/model"
)

func newConfigCenterHarness(t *testing.T) (*configv2.Controller, *modelcfg.Client) {
	t.Helper()
	for _, name := range []string{"PAW_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	paths, err := configv2.ResolvePaths(configv2.PathOptions{ConfigHome: filepath.Join(root, "Paw"), UserHomeDir: filepath.Join(root, "home"), WorkspaceRoot: filepath.Join(root, "work")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
  "schemaVersion": 2,
  "activeModel": "local/one",
  "providers": {"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1"}},
  "models": {
    "local/one":{"provider":"local","name":"one"},
    "local/two":{"provider":"local","name":"two"}
  }
}`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := configv2.Open(context.Background(), configv2.Options{Paths: paths, Credentials: &configv2.FakeCredentialStore{Values: map[string]string{}}, DisableWatch: true})
	if err != nil {
		t.Fatal(err)
	}
	client := modelcfg.NewClient(manager.Snapshot().Active)
	controller := configv2.NewController(manager, client)
	t.Cleanup(func() { _ = controller.Close() })
	return controller, client
}

func TestSettingAndConfigShareUnifiedCenter(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, &fakeSettingsController{}, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	handled, cmd := model.handleCommand("/setting")
	if !handled || cmd != nil || model.configCenter == nil || model.settingWizard != nil {
		t.Fatalf("/setting did not open config center: handled=%v center=%#v settings=%#v", handled, model.configCenter, model.settingWizard)
	}
	model.configCenter = nil
	handled, cmd = model.handleCommand("/config")
	if !handled || cmd != nil || model.configCenter == nil {
		t.Fatalf("/config did not open the same center")
	}
	model.configCenter = nil
	handled, cmd = model.handleCommand("/config status")
	if !handled || cmd != nil {
		t.Fatal("/config status was not handled")
	}
	last := model.transcript[len(model.transcript)-1]
	if !strings.Contains(last.body, "revision=") || !strings.Contains(last.body, "activeModel=local/one") {
		t.Fatalf("status=%q", last.body)
	}
	model.handleCommand("/config path")
	last = model.transcript[len(model.transcript)-1]
	if last.body != controller.ConfigPath() {
		t.Fatalf("path=%q want=%q", last.body, controller.ConfigPath())
	}
}

func TestConfigCenterBackFromHomeClosesWithoutPanic(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()

	model.configCenterBack()

	if model.configCenter != nil {
		t.Fatalf("config center remained open: %#v", model.configCenter)
	}
}

func TestConfigCenterDiagnosticsWrapLongMigrationError(t *testing.T) {
	for _, name := range []string{"PAW_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	paths, err := configv2.ResolvePaths(configv2.PathOptions{
		ConfigHome:    filepath.Join(root, "Paw"),
		UserHomeDir:   filepath.Join(root, "home"),
		WorkspaceRoot: filepath.Join(root, "work"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":1,"modelProfiles":[{"id":"deepseek","provider":"deepseek","apiKey":"fixture-secret","models":["deepseek-chat"]}]}`
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := configv2.Open(context.Background(), configv2.Options{
		Paths:        paths,
		Credentials:  &configv2.FakeCredentialStore{Unavailable: true},
		DisableWatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := modelcfg.NewClient(manager.Snapshot().Active)
	controller := configv2.NewController(manager, client)
	t.Cleanup(func() { _ = controller.Close() })

	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.ready = true
	model.width = 100
	model.height = 30
	model.relayout()
	model.openConfigCenter()
	if model.configCenter.page != configCenterDiagnostics {
		t.Fatalf("page=%v, want diagnostics", model.configCenter.page)
	}

	rendered := ansi.Strip(model.renderConfigCenterBox())
	if strings.Contains(rendered, "fixture-secret") {
		t.Fatalf("diagnostics leaked credential: %q", rendered)
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "│", "").Replace(rendered)
	if !strings.Contains(compact, "configureanenvironmentvariableandretry") {
		t.Fatalf("diagnostic tail was clipped:\n%s", rendered)
	}
	for index, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("line %d width=%d, want <=80: %q", index+1, got, line)
		}
	}
}

func TestIncompleteFirstRunOpensProviderThenCredentialSetup(t *testing.T) {
	for _, name := range []string{"PAW_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	paths, err := configv2.ResolvePaths(configv2.PathOptions{ConfigHome: filepath.Join(root, "Paw"), UserHomeDir: filepath.Join(root, "home"), WorkspaceRoot: filepath.Join(root, "work")})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := configv2.Open(context.Background(), configv2.Options{Paths: paths, Credentials: &configv2.FakeCredentialStore{Values: map[string]string{}}, DisableWatch: true})
	if err != nil {
		t.Fatal(err)
	}
	client := modelcfg.NewClient(manager.Snapshot().Active)
	controller := configv2.NewController(manager, client)
	t.Cleanup(func() { _ = controller.Close() })
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller

	model.openConfigCenter()
	if model.configCenter.page != configCenterAddProvider {
		t.Fatalf("first-run page=%v, want provider selection", model.configCenter.page)
	}
	model.addPresetProvider("openai")
	if model.configCenter.page != configCenterCredentials {
		t.Fatalf("post-provider page=%v, want credential setup; err=%q", model.configCenter.page, model.configCenter.err)
	}
	if _, exists := controller.Snapshot().Document.Providers["openai"]; !exists {
		t.Fatal("selected provider was not persisted")
	}
}

func TestConfigCenterKeepsDraftOnExternalRevisionConflict(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterProviderActions
	model.configCenter.targetID = "local"
	model.openConfigEdit(configEditProviderEndpoint, "http://draft.invalid/v1", "Endpoint")
	snapshot := controller.Snapshot()
	configured := snapshot.Document.Models["local/two"]
	configured.Parameters = map[string]any{"temperature": 0.2}
	if _, err := controller.UpdateConfig(context.Background(), snapshot.Revision, []configv2.Operation{configv2.UpsertModel("local/two", configured)}); err != nil {
		t.Fatal(err)
	}
	model.finishConfigEdit(true)
	if model.configCenter.page != configCenterEdit || model.configCenter.editValue != "http://draft.invalid/v1" || !strings.Contains(model.configCenter.err, "revision conflict") {
		t.Fatalf("draft/conflict state=%#v", model.configCenter)
	}
}

func TestCredentialEditorNeverRendersSecret(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterEdit
	model.configCenter.editKind = configEditCredential
	model.configCenter.editValue = "super-secret"
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	rendered := model.renderConfigCenterBox()
	if strings.Contains(rendered, "super-secret") {
		t.Fatalf("credential leaked in render: %q", rendered)
	}
	if !strings.Contains(rendered, "••••") {
		t.Fatalf("masked credential missing: %q", rendered)
	}
}

func TestModelCommandAcceptsStableRegistryID(t *testing.T) {
	controller, client := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	handled, cmd := model.handleCommand("/model local/two")
	if !handled || cmd != nil {
		t.Fatal("stable /model command was not handled")
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/two" {
		t.Fatalf("active ID=%q", got)
	}
	if got := client.CurrentModelConfig().Model; got != "two" {
		t.Fatalf("runtime model=%q snapshot runtime=%q", got, controller.Snapshot().Active.Model)
	}
}

func TestConfigCenterEditsProviderRuntimeFields(t *testing.T) {
	controller, client := newConfigCenterHarness(t)
	t.Setenv("FIRST_KEY", "test-secret")
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterProviderActions
	model.configCenter.targetID = "local"

	model.configCenter.selected = 2
	model.advanceProviderAction(controller.Snapshot())
	if model.configCenter.editKind != configEditProviderTimeout {
		t.Fatalf("edit kind=%v", model.configCenter.editKind)
	}
	model.configCenter.editValue = "45"
	model.finishConfigEdit(true)

	model.configCenter.selected = 4
	model.advanceProviderAction(controller.Snapshot())
	model.configCenter.selected = 5
	model.advanceProviderAction(controller.Snapshot())
	model.configCenter.editValue = "FIRST_KEY, SECOND_KEY FIRST_KEY"
	model.finishConfigEdit(true)

	provider := controller.Snapshot().Document.Providers["local"]
	if provider.TimeoutSeconds != 45 || provider.Stream == nil || !*provider.Stream {
		t.Fatalf("provider fields=%#v", provider)
	}
	if got := strings.Join(provider.Auth.Env, ","); got != "FIRST_KEY,SECOND_KEY" {
		t.Fatalf("env candidates=%q", got)
	}
	runtime := client.CurrentModelConfig()
	if runtime.Timeout != 45*time.Second || !runtime.Stream {
		t.Fatalf("runtime was not synchronously updated: timeout=%s stream=%v", runtime.Timeout, runtime.Stream)
	}
}

func TestConfigCenterEditsModelRuntimeFields(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterModelActions
	model.configCenter.targetID = "local/one"

	model.configCenter.selected = 3
	model.advanceModelAction(controller.Snapshot())
	model.configCenter.editValue = "131072"
	model.finishConfigEdit(true)
	model.configCenter.selected = 4
	model.advanceModelAction(controller.Snapshot())
	model.configCenter.selected = 5
	model.advanceModelAction(controller.Snapshot())

	configured := controller.Snapshot().Document.Models["local/one"]
	if configured.ContextWindow != 131072 || configured.Stream == nil || !*configured.Stream {
		t.Fatalf("model fields=%#v", configured)
	}
	if configured.Capabilities.Tools == nil || !*configured.Capabilities.Tools {
		t.Fatalf("tools capability=%v", configured.Capabilities.Tools)
	}
}

func TestConfigCenterRequiresDeleteConfirmation(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterModelActions
	model.configCenter.targetID = "local/two"
	model.configCenter.selected = 10

	model.advanceModelAction(controller.Snapshot())
	if _, exists := controller.Snapshot().Document.Models["local/two"]; !exists {
		t.Fatal("first delete activation removed the model without confirmation")
	}
	if !strings.Contains(model.configCenter.err, "Press Enter again") {
		t.Fatalf("confirmation message=%q", model.configCenter.err)
	}
	model.advanceModelAction(controller.Snapshot())
	if _, exists := controller.Snapshot().Document.Models["local/two"]; exists {
		t.Fatal("confirmed delete did not remove the model")
	}
}
