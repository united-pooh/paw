package bubble

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	configv2 "paw/internal/config"
	modelcfg "paw/internal/model"
)

type fakeConfigCenterDiscoverer struct {
	models []configv2.DiscoveredModel
}

func (d *fakeConfigCenterDiscoverer) Discover(_ context.Context, _ string, _ configv2.Provider, _ string) ([]configv2.DiscoveredModel, error) {
	return append([]configv2.DiscoveredModel(nil), d.models...), nil
}

func newConfigCenterHarness(t *testing.T) (*configv2.Controller, *modelcfg.Client) {
	t.Helper()
	return newConfigCenterHarnessWithOptions(t, nil, false)
}

func newConfigCenterHarnessWithDiscovery(t *testing.T, discovered []configv2.DiscoveredModel) (*configv2.Controller, *modelcfg.Client) {
	t.Helper()
	return newConfigCenterHarnessWithOptions(t, discovered, true)
}

func newConfigCenterHarnessWithOptions(t *testing.T, discovered []configv2.DiscoveredModel, discoveryEnabled bool) (*configv2.Controller, *modelcfg.Client) {
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
	provider := `{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1"}`
	if discoveryEnabled {
		provider = `{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1","discovery":{"enabled":true,"path":"models","format":"openai-list"}}`
	}
	raw := []byte(`{
  "schemaVersion": 2,
  "activeModel": "local/one",
  "providers": {"local":` + provider + `},
  "models": {
    "local/one":{"provider":"local","name":"one"},
    "local/two":{"provider":"local","name":"two"}
  }
}`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	options := configv2.Options{
		Paths:                 paths,
		Credentials:           &configv2.FakeCredentialStore{Values: map[string]string{}},
		DisableWatch:          true,
		DisableModelDiscovery: !discoveryEnabled,
	}
	if discoveryEnabled {
		options.Discoverer = &fakeConfigCenterDiscoverer{models: discovered}
	}
	manager, err := configv2.Open(context.Background(), options)
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
		Paths:                 paths,
		Credentials:           &configv2.FakeCredentialStore{Unavailable: true},
		DisableWatch:          true,
		DisableModelDiscovery: true,
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
	manager, err := configv2.Open(context.Background(), configv2.Options{Paths: paths, Credentials: &configv2.FakeCredentialStore{Values: map[string]string{}}, DisableWatch: true, DisableModelDiscovery: true})
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

func TestConfigCenterCtrlSSavesAndExpiresNotice(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.openConfigCenter()
	model.configCenter.page = configCenterProviderActions
	model.configCenter.targetID = "local"
	model.openConfigEdit(configEditProviderEndpoint, "http://saved.invalid/v1", "Endpoint")

	next, cmd := model.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	saved := next.(appModel)
	if cmd == nil {
		t.Fatal("Ctrl+S did not schedule Saved notice expiry")
	}
	if got := controller.Snapshot().Document.Providers["local"].Endpoint; got != "http://saved.invalid/v1" {
		t.Fatalf("endpoint=%q", got)
	}
	if saved.configCenter.page != configCenterProviderActions || saved.configCenter.notice != "Saved" {
		t.Fatalf("saved state=%#v", saved.configCenter)
	}
	if rendered := ansi.Strip(saved.renderConfigCenterBox()); !strings.Contains(rendered, "Saved") {
		t.Fatalf("Saved notice missing from render: %q", rendered)
	}
	if configCenterSavedNoticeDuration != 3*time.Second {
		t.Fatalf("notice duration=%s", configCenterSavedNoticeDuration)
	}

	sequence := saved.configCenter.noticeSequence
	expired, _ := saved.Update(configCenterSavedExpiredMsg{state: saved.configCenter, sequence: sequence})
	if got := expired.(appModel).configCenter.notice; got != "" {
		t.Fatalf("notice remained after expiry: %q", got)
	}
}

func TestConfigCenterSaveFailureDoesNotShowSavedNotice(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterProviderActions
	model.configCenter.targetID = "local"
	model.openConfigEdit(configEditProviderTimeout, "invalid", "Timeout seconds")

	next, cmd := model.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	failed := next.(appModel)
	if cmd != nil {
		t.Fatal("failed save scheduled a success notice")
	}
	if failed.configCenter.notice != "" || !strings.Contains(failed.configCenter.err, "timeout must be an integer") {
		t.Fatalf("failed save state=%#v", failed.configCenter)
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

type recordingCatalogController struct {
	*configv2.Controller
	setActiveCalls []string
	selections     []configv2.CatalogSelection
}

func (c *recordingCatalogController) SetActiveModelID(id string) error {
	c.setActiveCalls = append(c.setActiveCalls, id)
	return c.Controller.SetActiveModelID(id)
}

func (c *recordingCatalogController) ActivateCatalogSelection(selection configv2.CatalogSelection) error {
	c.selections = append(c.selections, selection)
	return c.Controller.ActivateCatalogSelection(selection)
}

func TestConfigCenterModelsAndActiveShowEffectiveCatalogSourcesAndCounts(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.ready = true
	app.width = 100
	app.height = 30
	app.relayout()
	app.openConfigCenter()

	home := ansi.Strip(app.renderConfigCenterBox())
	if !strings.Contains(home, "registered=2 effective=3") {
		t.Fatalf("home=%q", home)
	}

	app.configCenter.page = configCenterModels
	models := ansi.Strip(app.renderConfigCenterBox())
	for _, want := range []string{"local/one", "configured", "local/live", "discovered"} {
		if !strings.Contains(models, want) {
			t.Fatalf("models page missing %q: %q", want, models)
		}
	}

	app.configCenter.page = configCenterActive
	active := ansi.Strip(app.renderConfigCenterBox())
	for _, want := range []string{"local/one", "configured", "local/live", "discovered"} {
		if !strings.Contains(active, want) {
			t.Fatalf("active page missing %q: %q", want, active)
		}
	}
}

func TestConfigCenterActiveDiscoveredSelectionUsesObservedCatalogIdentity(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterActive
	app.configCenter.selected = sortedIndex(sortedCatalogModelIDs(controller.Snapshot().EffectiveModels), "local/live")
	_ = app.configCenterOptions()
	observed := controller.Snapshot()

	app = app.advanceConfigCenter()

	if len(recorder.setActiveCalls) != 0 {
		t.Fatalf("Active page used SetActiveModelID: %#v", recorder.setActiveCalls)
	}
	if len(recorder.selections) != 1 {
		t.Fatalf("catalog selections=%#v", recorder.selections)
	}
	selection := recorder.selections[0]
	if selection.Revision != observed.Revision || selection.ID != "local/live" || selection.ProviderKey != "local" || selection.ModelName != "live" || selection.Source != configv2.ModelSourceDiscovered {
		t.Fatalf("selection=%#v observed revision=%d", selection, observed.Revision)
	}
	updated := controller.Snapshot()
	if updated.ActiveModelID != "local/live" || updated.Document.Models["local/live"].Name != "live" {
		t.Fatalf("discovered selection was not activated and registered: %#v", updated)
	}
}

func TestConfigCenterActiveRejectsCatalogSelectionThatChangedAfterDisplay(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.openConfigCenter()
	app.configCenter.page = configCenterActive
	app.configCenter.selected = sortedIndex(sortedCatalogModelIDs(controller.Snapshot().EffectiveModels), "local/live")
	_ = app.configCenterOptions()

	before := controller.Snapshot()
	occupant := configv2.Model{Provider: "local", Name: "replacement"}
	if _, err := controller.UpdateConfig(context.Background(), before.Revision, []configv2.Operation{configv2.UpsertModel("local/live", occupant)}); err != nil {
		t.Fatal(err)
	}

	app = app.advanceConfigCenter()

	if !strings.Contains(app.configCenter.err, "revision conflict") {
		t.Fatalf("stale selection error=%q", app.configCenter.err)
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/one" {
		t.Fatalf("stale selection activated %q", got)
	}
}

func TestConfigCenterDiscoveredModelActionsOnlyActivateAndRegister(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = sortedIndex(sortedCatalogModelIDs(controller.Snapshot().EffectiveModels), "local/live")

	app = app.advanceConfigCenter()
	options := app.configCenterOptions()
	if len(options) != 1 || options[0].label != "Activate and register" {
		t.Fatalf("discovered actions=%#v", options)
	}

	app = app.advanceConfigCenter()
	if len(recorder.selections) != 1 || len(recorder.setActiveCalls) != 0 {
		t.Fatalf("activation calls: selections=%#v setActive=%#v", recorder.selections, recorder.setActiveCalls)
	}
	if app.configCenter.page != configCenterModels || controller.Snapshot().Document.Models["local/live"].Name != "live" {
		t.Fatalf("post-activation state=%#v snapshot=%#v", app.configCenter, controller.Snapshot())
	}

	app.configCenter.targetID = "local/one"
	app.configCenter.page = configCenterModelActions
	if got := len(app.configCenterOptions()); got != 11 {
		t.Fatalf("configured model actions=%d, want 11", got)
	}
}

func TestModelCommandAcceptsDiscoveredCatalogIDAndShowsDiscoveryStatus(t *testing.T) {
	controller, client := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller

	handled, cmd := app.handleCommand("/model local/live")
	if !handled || cmd != nil {
		t.Fatal("discovered /model command was not handled")
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/live" {
		t.Fatalf("active ID=%q", got)
	}
	if got := client.CurrentModelConfig().Model; got != "live" {
		t.Fatalf("runtime model=%q", got)
	}
	if got := controller.Snapshot().EffectiveModels["local/live"].Source; got != configv2.ModelSourceConfigured {
		t.Fatalf("registered source=%q", got)
	}

	app.handleCommand("/model status")
	status := app.transcript[len(app.transcript)-1].body
	for _, want := range []string{"registered=3 effective=3", "discovery source=live", "provider=local", "discovered=1", "filtered=0", "cache=updated", "discoveredAt=", "age="} {
		if !strings.Contains(status, want) {
			t.Fatalf("model status missing %q: %q", want, status)
		}
	}
}

func TestConfigStatusAndDiagnosticsShowSanitizedDiscoveryStatus(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.ready = true
	app.width = 120
	app.height = 40
	app.relayout()

	app.handleCommand("/config status")
	status := app.transcript[len(app.transcript)-1].body
	for _, want := range []string{"registered=2 effective=3", "discovery source=live", "provider=local", "discovered=1", "filtered=0", "cache=updated", "attemptedAt=", "succeededAt=", "discoveredAt=", "age=", "skip=(none)", "error=(none)"} {
		if !strings.Contains(status, want) {
			t.Fatalf("config status missing %q: %q", want, status)
		}
	}

	app.openConfigCenter()
	app.configCenter.page = configCenterDiagnostics
	diagnostics := ansi.Strip(app.renderConfigCenterBox())
	compactDiagnostics := strings.NewReplacer(" ", "", "\n", "", "│", "").Replace(diagnostics)
	for _, want := range []string{"discoverysource=live", "provider=local", "discovered=1", "effective=3", "cache=updated", "age="} {
		if !strings.Contains(compactDiagnostics, want) {
			t.Fatalf("diagnostics missing %q: %q", want, diagnostics)
		}
	}
	for _, forbidden := range []string{"http://127.0.0.1:1234/v1", "Authorization", "credential", "headers", "response body"} {
		if strings.Contains(diagnostics, forbidden) || strings.Contains(status, forbidden) {
			t.Fatalf("discovery status leaked %q: status=%q diagnostics=%q", forbidden, status, diagnostics)
		}
	}
}

func TestDiscoveryStatusSummaryIncludesSafeSkipAndError(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	status := configv2.DiscoveryStatus{
		Attempted:       true,
		ProviderID:      "local",
		Source:          "cache",
		AttemptedAt:     now.Add(-3 * time.Minute),
		SucceededAt:     now.Add(-2 * time.Minute),
		DiscoveredAt:    now.Add(-90 * time.Second),
		DiscoveredCount: 12,
		FilteredCount:   3,
		EffectiveCount:  14,
		CacheProviders:  2,
		CacheState:      "matched",
		SkippedReason:   "disabled",
		LastError:       "model discovery failed: kind=auth_failed status=401: provider authentication failed",
	}

	summary := discoveryStatusSummary(status, now)
	for _, want := range []string{"attempted=true", "source=cache", "provider=local", "discovered=12", "filtered=3", "effective=14", "cache=matched", "cacheProviders=2", "attemptedAt=2026-08-11T11:57:00Z", "succeededAt=2026-08-11T11:58:00Z", "discoveredAt=2026-08-11T11:58:30Z", "age=1m30s", "skip=disabled", "error=model discovery failed: kind=auth_failed status=401: provider authentication failed"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %q", want, summary)
		}
	}
}
