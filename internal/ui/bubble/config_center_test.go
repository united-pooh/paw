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

func TestConfigCenterBackFromTopLevelClosesWithoutPanic(t *testing.T) {
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
	// 配置中心已全屏化并使用小页面 gutter，整页行宽等于终端宽 100；诊断
	// 正文按页面内容宽换行，长迁移错误应被完整显示、不越界、不泄露凭证。
	for index, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > 100 {
			t.Fatalf("line %d width=%d, want <=100 (full screen): %q", index+1, got, line)
		}
	}
}

func TestConfigCenterWithoutActiveModelOpensMergedModelsPage(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	snapshot := controller.Snapshot()
	snapshot.Document.ActiveModel = ""
	snapshot.ActiveModelID = ""
	snapshot.Ready = false
	wrapped := &fixedSnapshotConfigCenterController{ConfigCenterController: controller, snapshot: snapshot}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = wrapped

	app.openConfigCenter()

	if app.configCenter.page != configCenterModels {
		t.Fatalf("first-run page=%v, want merged Models page", app.configCenter.page)
	}
	options := app.configCenterOptions()
	if len(options) != 3 || options[0].label != "local/one" || options[1].label != "local/two" || options[2].label != "+ 添加模型" {
		t.Fatalf("model options=%#v", options)
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
	if saved.configCenter.page != configCenterProviderActions || saved.configCenter.notice != "已保存" {
		t.Fatalf("saved state=%#v", saved.configCenter)
	}
	if rendered := ansi.Strip(saved.renderConfigCenterBox()); !strings.Contains(rendered, "已保存") {
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

func TestConfigCenterTransportSwitchAlignsCanonicalAPIPath(t *testing.T) {
	tests := []struct {
		name     string
		apiPath  string
		wantPath string
	}{
		{name: "previous canonical path", apiPath: "/chat/completions", wantPath: "/messages"},
		{name: "empty path", wantPath: "/messages"},
		{name: "custom path", apiPath: "/gateway/generate", wantPath: "/gateway/generate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller, _ := newConfigCenterHarness(t)
			snapshot := controller.Snapshot()
			provider := snapshot.Document.Providers["local"]
			provider.APIPath = tt.apiPath
			updated, err := controller.UpdateConfig(context.Background(), snapshot.Revision, []configv2.Operation{configv2.UpsertProvider("local", provider)})
			if err != nil {
				t.Fatal(err)
			}

			model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
			model.configCenterController = controller
			model.openConfigCenter()
			model.configCenter.page = configCenterProviderActions
			model.configCenter.targetID = "local"
			model.configCenter.revision = updated.Revision
			model.configCenter.selected = 1

			model.advanceProviderAction(updated)

			if model.configCenter.err != "" {
				t.Fatalf("transport switch failed: %s", model.configCenter.err)
			}
			got := controller.Snapshot().Document.Providers["local"]
			if got.Transport != configv2.TransportAnthropicCompatible || got.APIPath != tt.wantPath {
				t.Fatalf("provider transport/path=%q %q want=%q %q", got.Transport, got.APIPath, configv2.TransportAnthropicCompatible, tt.wantPath)
			}
		})
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
	model.configCenter.selected = 9
	model.advanceModelAction(controller.Snapshot())
	if model.configCenter.editKind != configEditModelParameters {
		t.Fatalf("model parameters action remained at index 9: %#v", model.configCenter)
	}
	model.configCenter.page = configCenterModelActions
	model.configCenter.editKind = configEditNone
	model.configCenter.selected = 10

	configured := controller.Snapshot().Document.Models["local/one"]
	if configured.ContextWindow != 131072 || configured.Stream == nil || !*configured.Stream {
		t.Fatalf("model fields=%#v", configured)
	}
	if configured.Capabilities.Tools == nil || !*configured.Capabilities.Tools {
		t.Fatalf("tools capability=%v", configured.Capabilities.Tools)
	}
}

func TestConfigCenterSpaceMatchesEnter(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.openConfigCenter()
	model.configCenter.page = configCenterModelActions
	model.configCenter.targetID = "local/one"
	model.configCenter.selected = 1

	next, cmd := model.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if cmd != nil {
		t.Fatal("space key returned an unexpected command")
	}
	if next.(appModel).configCenter.page != configCenterEdit {
		t.Fatalf("space key did not activate the selected action: %#v", next.(appModel).configCenter)
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
	if !strings.Contains(model.configCenter.err, "再次按 Enter") {
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

func configCenterOptionIndex(t *testing.T, app appModel, label string) int {
	t.Helper()
	for index, option := range app.configCenterOptions() {
		if option.label == label {
			return index
		}
	}
	t.Fatalf("config center option %q not found: %#v", label, app.configCenterOptions())
	return -1
}

func TestConfigCenterModelsShowEffectiveCatalogSourcesAndCurrentState(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	if err := controller.SetActiveModelID("local/two"); err != nil {
		t.Fatal(err)
	}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.ready = true
	app.width = 100
	app.height = 30
	app.relayout()
	app.openConfigCenter()
	if app.configCenter.page != configCenterGeneral {
		t.Fatalf("config center should open directly on General, got page=%v", app.configCenter.page)
	}

	general := ansi.Strip(app.renderConfigCenterBox())
	for _, want := range []string{"推理开关", "推理强度"} {
		if !strings.Contains(general, want) {
			t.Fatalf("general page missing %q: %q", want, general)
		}
	}

	app.configCenter.page = configCenterModels
	options := app.configCenterOptions()
	if len(options) != 4 {
		t.Fatalf("model options=%#v, want three catalog models plus add", options)
	}
	if options[0].label != "local/two" || !strings.Contains(options[0].description, "当前") {
		t.Fatalf("current model is not first and marked: %#v", options)
	}
	models := ansi.Strip(app.renderConfigCenterBox())
	for _, want := range []string{"local/two", "当前", "configured", "local/live", "discovered"} {
		if !strings.Contains(models, want) {
			t.Fatalf("models page missing %q: %q", want, models)
		}
	}
}

func TestConfigCenterEnterActivatesConfiguredModelFromMergedList(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/two")

	next, cmd := app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyEnter})
	app = next.(appModel)

	if cmd != nil {
		t.Fatal("model activation returned an unexpected command")
	}
	if app.configCenter.page != configCenterModels {
		t.Fatalf("model activation left merged list: %#v", app.configCenter)
	}
	if len(recorder.selections) != 1 || recorder.selections[0].ID != "local/two" {
		t.Fatalf("catalog selections=%#v", recorder.selections)
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/two" {
		t.Fatalf("active model=%q", got)
	}
}

func TestConfigCenterSpaceOpensModelManagement(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/two")

	next, cmd := app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	app = next.(appModel)

	if cmd != nil {
		t.Fatal("model management returned an unexpected command")
	}
	if app.configCenter.page != configCenterModelActions || app.configCenter.targetID != "local/two" {
		t.Fatalf("space did not open model management: %#v", app.configCenter)
	}
}

func TestConfigCenterEnterActivatesDiscoveredModelFromMergedList(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/live")
	observed := controller.Snapshot()

	next, cmd := app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyEnter})
	app = next.(appModel)

	if cmd != nil {
		t.Fatal("discovered model activation returned an unexpected command")
	}
	if app.configCenter.page != configCenterModels {
		t.Fatalf("discovered model activation left merged list: %#v", app.configCenter)
	}
	if len(recorder.setActiveCalls) != 0 || len(recorder.selections) != 1 {
		t.Fatalf("activation calls: selections=%#v setActive=%#v", recorder.selections, recorder.setActiveCalls)
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

func TestConfigCenterEnterRejectsStaleMergedModelSelection(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/live")

	before := controller.Snapshot()
	occupant := configv2.Model{Provider: "local", Name: "replacement"}
	if _, err := controller.UpdateConfig(context.Background(), before.Revision, []configv2.Operation{configv2.UpsertModel("local/live", occupant)}); err != nil {
		t.Fatal(err)
	}

	next, _ := app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyEnter})
	app = next.(appModel)

	if !strings.Contains(app.configCenter.err, "revision conflict") {
		t.Fatalf("stale selection error=%q", app.configCenter.err)
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/one" {
		t.Fatalf("stale selection activated %q", got)
	}
}

func TestConfigCenterFilteredModelKeysKeepOriginalSelection(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.search = "local/two"
	app.resetConfigCenterSelectionForSearch()

	next, _ := app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyEnter})
	app = next.(appModel)
	if len(recorder.selections) != 1 || recorder.selections[0].ID != "local/two" {
		t.Fatalf("filtered Enter activated wrong model: %#v", recorder.selections)
	}

	app.configCenter.search = "local/one"
	app.resetConfigCenterSelectionForSearch()
	next, _ = app.handleConfigCenterKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	app = next.(appModel)
	if app.configCenter.page != configCenterModelActions || app.configCenter.targetID != "local/one" {
		t.Fatalf("filtered Space opened wrong model management page: %#v", app.configCenter)
	}
}

func TestConfigCenterAddModelWorksWithEnterAndSpace(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{' '}},
	} {
		controller, _ := newConfigCenterHarness(t)
		app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
		app.configCenterController = controller
		app.openConfigCenter()
		app.configCenter.page = configCenterModels
		app.configCenter.search = "添加模型"
		app.resetConfigCenterSelectionForSearch()

		next, _ := app.handleConfigCenterKey(key)
		app = next.(appModel)
		if app.configCenter.page != configCenterAddModelProvider {
			t.Fatalf("key %q opened page %v, want AddModelProvider", key.String(), app.configCenter.page)
		}
		if app.configCenter.search != "" {
			t.Fatalf("key %q kept stale model search %q on AddModelProvider", key.String(), app.configCenter.search)
		}
	}
}

func TestConfigCenterConfiguredModelCanBeActivatedFromModelActions(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/two")

	app = app.advanceConfigCenter()
	if app.configCenter.page != configCenterModelActions {
		t.Fatalf("model selection did not open actions: %#v", app.configCenter)
	}
	app.configCenter.selected = 0
	app = app.advanceConfigCenter()

	if len(recorder.setActiveCalls) != 0 {
		t.Fatalf("model actions used SetActiveModelID: %#v", recorder.setActiveCalls)
	}
	if len(recorder.selections) != 1 || recorder.selections[0].ID != "local/two" {
		t.Fatalf("catalog selections=%#v", recorder.selections)
	}
	if got := controller.Snapshot().ActiveModelID; got != "local/two" {
		t.Fatalf("active model=%q", got)
	}
}

func TestConfigCenterModelActionRejectsCatalogSelectionThatChangedAfterDisplay(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/live")
	app = app.advanceConfigCenter()

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

func TestConfigCenterRecoversFromStaleModelActionAfterBackRefresh(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = controller
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/live")
	_ = app.configCenterOptions()
	app = app.advanceConfigCenter()
	if app.configCenter.page != configCenterModelActions || app.configCenter.targetSelection.Source != configv2.ModelSourceDiscovered {
		t.Fatalf("initial discovered action state=%#v", app.configCenter)
	}

	before := controller.Snapshot()
	if _, err := controller.UpdateConfig(context.Background(), before.Revision, []configv2.Operation{
		configv2.UpsertModel("local/live", configv2.Model{Provider: "local", Name: "replacement"}),
	}); err != nil {
		t.Fatal(err)
	}
	current := controller.Snapshot()

	app = app.advanceConfigCenter()
	if !strings.Contains(app.configCenter.err, "revision conflict") {
		t.Fatalf("stale action error=%q", app.configCenter.err)
	}

	app.configCenterBack()
	if app.configCenter.page != configCenterModels || app.configCenter.revision != current.Revision {
		t.Fatalf("back refresh state=%#v current revision=%d", app.configCenter, current.Revision)
	}
	options := app.configCenterOptions()
	freshIndex := configCenterOptionIndex(t, app, "local/live")
	if freshIndex >= len(options) || !strings.Contains(options[freshIndex].description, "configured") || !strings.Contains(options[freshIndex].description, "replacement") {
		t.Fatalf("refreshed catalog options=%#v", options)
	}

	app.configCenter.selected = freshIndex
	app = app.advanceConfigCenter()
	app.configCenter.selected = 1
	app = app.advanceConfigCenter()
	if app.configCenter.page != configCenterEdit {
		t.Fatalf("fresh edit did not open: %#v", app.configCenter)
	}
	app.configCenter.editValue = "replacement-edited"
	app.finishConfigEdit(true)
	if app.configCenter.err != "" {
		t.Fatalf("fresh edit failed after recovery: %#v", app.configCenter)
	}
	if got := controller.Snapshot().Document.Models["local/live"].Name; got != "replacement-edited" {
		t.Fatalf("edited model name=%q", got)
	}

	app.configCenter.selected = 0
	app = app.advanceConfigCenter()
	if app.configCenter.err != "" || controller.Snapshot().ActiveModelID != "local/live" {
		t.Fatalf("fresh activation failed: state=%#v active=%q", app.configCenter, controller.Snapshot().ActiveModelID)
	}
}

func TestConfigCenterDiscoveredModelActionsOnlyActivateAndRegister(t *testing.T) {
	controller, _ := newConfigCenterHarnessWithDiscovery(t, []configv2.DiscoveredModel{{ProviderID: "local", Name: "live"}})
	recorder := &recordingCatalogController{Controller: controller}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = recorder
	app.openConfigCenter()
	app.configCenter.page = configCenterModels
	app.configCenter.selected = configCenterOptionIndex(t, app, "local/live")

	app = app.advanceConfigCenter()
	options := app.configCenterOptions()
	if len(options) != 1 || options[0].label != "设为当前并注册" {
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

type fixedSnapshotConfigCenterController struct {
	ConfigCenterController
	snapshot configv2.Snapshot
}

func (c *fixedSnapshotConfigCenterController) Snapshot() configv2.Snapshot {
	return c.snapshot.Clone()
}

func TestDiscoveryStatusSanitizesAllTextFieldsAndTruncatesByCells(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	snapshot := controller.Snapshot()
	snapshot.Discovery = configv2.DiscoveryStatus{
		Source:        "li\x1b[2Jve\nSOURCE_LINE",
		ProviderID:    "loc\x1b]0;owned\x07al\nPROVIDER_LINE",
		CacheState:    "mat\u009b2Jched\r\nCACHE_LINE",
		SkippedReason: "dis\x00abled\tSKIP_LINE",
		LastError:     "bad\x1bPignored\x1b\\\nERROR_LINE\x07",
	}
	wrapped := &fixedSnapshotConfigCenterController{ConfigCenterController: controller, snapshot: snapshot}
	app := newModel(context.Background(), &fakeRunner{}, "session", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.configCenterController = wrapped
	app.ready = true
	app.width = 160
	app.height = 40
	app.relayout()

	app.handleCommand("/config status")
	status := app.transcript[len(app.transcript)-1].body
	if got := strings.Count(status, "\n"); got != 5 {
		t.Fatalf("status contains injected lines (%d newlines): %q", got, status)
	}
	for _, want := range []string{
		"source=live SOURCE_LINE",
		"provider=local PROVIDER_LINE",
		"cache=matched CACHE_LINE",
		"skip=disabled SKIP_LINE",
		"error=bad ERROR_LINE",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("sanitized status missing %q: %q", want, status)
		}
	}
	for _, r := range status {
		if r == '\n' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("status retained control %U: %q", r, status)
		}
	}
	for _, forbidden := range []string{"\x1b[2J", "\x1b]", "\x1bP", "\x07", "\u009b", "\x00"} {
		if strings.Contains(status, forbidden) {
			t.Fatalf("status retained control sequence %q: %q", forbidden, status)
		}
	}

	app.openConfigCenter()
	app.configCenter.page = configCenterDiagnostics
	rendered := app.renderConfigCenterBox()
	for _, forbidden := range []string{"\x1b[2J", "\x1b]", "\x1bP", "\x07", "\u009b", "\x00"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered diagnostics retained injected sequence %q: %q", forbidden, rendered)
		}
	}
	plain := ansi.Strip(rendered)
	for _, r := range plain {
		if r == '\n' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("rendered diagnostics retained control %U: %q", r, plain)
		}
	}

	wide := safeDiscoveryStatusLabel(strings.Repeat("界", 130))
	if width := terminalCellWidth(wide); width > 240 || !strings.HasSuffix(wide, "…") {
		t.Fatalf("cell-truncated label width=%d value=%q", width, wide)
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
