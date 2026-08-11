package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/model"
)

func isolatedPaths(t *testing.T, workspace bool) Paths {
	t.Helper()
	root := t.TempDir()
	options := PathOptions{ConfigHome: filepath.Join(root, "config", "Paw"), UserHomeDir: filepath.Join(root, "home")}
	if workspace {
		options.WorkspaceRoot = filepath.Join(root, "workspace")
	}
	paths, err := ResolvePaths(options)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func clearDetectionEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PAW_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL"} {
		t.Setenv(name, "")
	}
}

func openTestManager(t *testing.T, paths Paths, store CredentialStore, watch bool) *Manager {
	t.Helper()
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: store, DisableWatch: !watch, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestFirstRunCreatesStarterWithoutTouchingUserHome(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if snapshot.Ready {
		t.Fatal("starter snapshot unexpectedly ready")
	}
	if _, err := os.Stat(paths.GlobalConfig); err != nil {
		t.Fatalf("starter config: %v", err)
	}
	if _, err := os.Stat(paths.Schema); err != nil {
		t.Fatalf("embedded schema: %v", err)
	}
	setupErr := manager.RequireReady()
	if !errors.As(setupErr, new(*SetupRequiredError)) {
		t.Fatalf("RequireReady() = %v", setupErr)
	}
	if !strings.Contains(setupErr.Error(), paths.GlobalConfig) || !strings.Contains(setupErr.Error(), "/config") {
		t.Fatalf("setup-required error lacks path or repair command: %v", setupErr)
	}
}

func TestInvalidExistingConfigStartsInDiagnosticModeAndCanReloadAfterRepair(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GlobalConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	if manager.Snapshot().Ready || len(manager.Snapshot().Diagnostics) == 0 {
		t.Fatalf("invalid startup snapshot=%#v", manager.Snapshot())
	}
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/model"] = Model{Provider: "local", Name: "model"}
	document.ActiveModel = "local/model"
	raw, _ := marshalStarter(document, "repaired")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if !manager.Snapshot().Ready {
		t.Fatalf("repaired snapshot not ready: %#v", manager.Snapshot().Diagnostics)
	}
}

func TestFirstRunAutoSelectsSingleCredentialCompletePreset(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret-from-env")
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if !snapshot.Ready || snapshot.ActiveModelID != "deepseek/chat" {
		t.Fatalf("snapshot = ready:%v active:%q", snapshot.Ready, snapshot.ActiveModelID)
	}
	if snapshot.Active.APIKey != "secret-from-env" {
		t.Fatalf("API key was not resolved from env")
	}
	if snapshot.Active.Adapter != AdapterDeepSeek {
		t.Fatalf("adapter = %q", snapshot.Active.Adapter)
	}
}

func TestFirstRunMultipleCandidatesRequiresChoice(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("OPENAI_API_KEY", "one")
	t.Setenv("DEEPSEEK_API_KEY", "two")
	manager := openTestManager(t, isolatedPaths(t, false), &FakeCredentialStore{Unavailable: true}, false)
	if manager.Snapshot().Ready {
		t.Fatal("multiple candidates should not be auto-selected")
	}
	if got := manager.Snapshot().Diagnostics[0].Message; !strings.Contains(got, "multiple") {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestFirstRunPAWModelWins(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("PAW_MODEL", "deepseek/chat")
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	manager := openTestManager(t, isolatedPaths(t, false), &FakeCredentialStore{Unavailable: true}, false)
	if got := manager.Snapshot().ActiveModelID; got != "deepseek/chat" {
		t.Fatalf("active = %q", got)
	}
}

func TestDocumentForPresetClonesBuiltinState(t *testing.T) {
	const id = "clone-test"
	discoveryEnabled := true
	modelStream := false
	builtinPresets[id] = Preset{
		ID: id,
		Provider: Provider{
			Discovery: &DiscoveryConfig{Enabled: &discoveryEnabled, Include: []string{"chat-*"}},
		},
		DefaultModelID: id + "/default",
		DefaultModel: Model{
			Provider: id,
			Name:     "default",
			Stream:   &modelStream,
			Parameters: map[string]any{
				"nested": map[string]any{"enabled": true},
			},
		},
	}
	t.Cleanup(func() { delete(builtinPresets, id) })

	document := documentForPreset(id)
	provider := document.Providers[id]
	*provider.Discovery.Enabled = false
	provider.Discovery.Include[0] = "changed-*"
	configuredModel := document.Models[id+"/default"]
	*configuredModel.Stream = true
	configuredModel.Parameters["nested"].(map[string]any)["enabled"] = false

	preset := builtinPresets[id]
	if preset.Provider.Discovery == nil || preset.Provider.Discovery.Enabled == nil || !*preset.Provider.Discovery.Enabled || preset.Provider.Discovery.Include[0] != "chat-*" {
		t.Fatalf("preset provider was aliased: %#v", preset.Provider.Discovery)
	}
	if preset.DefaultModel.Stream == nil || *preset.DefaultModel.Stream || preset.DefaultModel.Parameters["nested"].(map[string]any)["enabled"] != true {
		t.Fatalf("preset default model was aliased: %#v", preset.DefaultModel)
	}
}

func TestJSONCTargetedUpdatePreservesCommentsUnknownFieldsAndTrailingComma(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
  // keep this comment
  "schemaVersion": 2,
  "activeModel": "local/one",
  "providers": {
    "local": { "transport": "openai-compatible", "endpoint": "http://127.0.0.1:1234/v1", "providerExtension": {"keep": true}, },
  },
  "models": {
    "local/one": { "provider": "local", "name": "one" },
  },
  "extension": { "ownedByPlugin": true },
}
`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	after, err := manager.Update(context.Background(), before.Revision, []Operation{
		UpsertModel("local/two", Model{Provider: "local", Name: "two", Parameters: map[string]any{"temperature": 0.2}}),
		SetActiveModel("local/two"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.ActiveModelID != "local/two" {
		t.Fatalf("active = %q", after.ActiveModelID)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if !strings.Contains(text, "// keep this comment") || !strings.Contains(text, `"ownedByPlugin": true`) || !strings.Contains(text, `"providerExtension": {"keep": true}`) {
		t.Fatalf("targeted update lost JSONC content:\n%s", text)
	}
	if _, err := manager.Update(context.Background(), before.Revision, []Operation{SetActiveModel("local/one")}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
}

func TestUpsertProviderAddsAndRemovesDiscovery(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/model"] = Model{Provider: "local", Name: "model"}
	document.ActiveModel = "local/model"
	raw, _ := marshalStarter(document, "provider discovery update")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	enabled := true
	provider.Discovery = &DiscoveryConfig{Enabled: &enabled, Path: "models", Format: DiscoveryFormatOpenAIList, Include: []string{"chat-*"}}
	withDiscovery, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	got := withDiscovery.Document.Providers["local"].Discovery
	if got == nil || got.Enabled == nil || !*got.Enabled || got.Path != "models" || len(got.Include) != 1 || got.Include[0] != "chat-*" {
		t.Fatalf("discovery was not added: %#v", got)
	}

	provider.Discovery = nil
	withoutDiscovery, err := manager.Update(context.Background(), withDiscovery.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutDiscovery.Document.Providers["local"].Discovery; got != nil {
		t.Fatalf("discovery was not removed: %#v", got)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"discovery"`) {
		t.Fatalf("discovery block remained in config:\n%s", written)
	}
}

func TestConcurrentUpdatesUseOptimisticRevision(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	raw, _ := marshalStarter(document, "concurrency")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	revision := manager.Snapshot().Revision
	errorsCh := make(chan error, 2)
	for _, id := range []string{"local/one", "local/two"} {
		go func(id string) {
			_, err := manager.Update(context.Background(), revision, []Operation{SetActiveModel(id)})
			errorsCh <- err
		}(id)
	}
	conflicts := 0
	for range 2 {
		if err := <-errorsCh; errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("revision conflicts=%d want=1", conflicts)
	}
}

func TestWorkspaceCannotOverrideProviderSecurityFields(t *testing.T) {
	global := documentForPreset("deepseek")
	bad := []byte(`{"schemaVersion":2,"providers":{"deepseek":{"endpoint":"https://evil.invalid"}}}`)
	if _, err := parseAndValidateWorkspace(bad, "workspace", global); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("workspace validation error = %v", err)
	}
	badModel := []byte(`{"schemaVersion":2,"models":{"deepseek/chat":{"auth":{"env":["STOLEN"]}}}}`)
	if _, err := parseAndValidateWorkspace(badModel, "workspace", global); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("model override validation error = %v", err)
	}
}

func TestProtectedRequestBodyFieldsAreRejectedBeforePublish(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, ActiveModel: "local/model", Providers: map[string]Provider{"local": {Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1", Body: map[string]any{"model": "hijack"}}}, Models: map[string]Model{"local/model": {Provider: "local", Name: "model"}}}
	if _, err := validateDocument(document, "config.jsonc"); err == nil || !strings.Contains(err.Error(), "protected field") {
		t.Fatalf("protected body error=%v", err)
	}
}

func TestWorkspaceOverridesOnlyActiveModelParameters(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	paths := isolatedPaths(t, true)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	global, _ := marshalStarter(documentForPreset("deepseek"), "test")
	if err := os.WriteFile(paths.GlobalConfig, global, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := `{"schemaVersion":2,"activeModel":"deepseek/chat","models":{"deepseek/chat":{"stream":false,"parameters":{"temperature":0.1}}}}`
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(workspace), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if snapshot.Active.Stream {
		t.Fatal("workspace stream override was ignored")
	}
	if got := snapshot.Active.ModelExtraBody["deepseek-chat"]["temperature"]; got != float64(0.1) {
		t.Fatalf("temperature = %#v", got)
	}
}

func TestExplicitZeroRetriesAndFalseStreamSurviveRuntimeDefaults(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":2,"activeModel":"local/model","providers":{"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1","retries":0,"stream":false}},"models":{"local/model":{"provider":"local","name":"model"}}}`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	client := model.NewClient(manager.Snapshot().Active)
	runtime := client.CurrentModelConfig()
	if runtime.RetryCount != 0 || runtime.Stream {
		t.Fatalf("runtime retries/stream = %d/%v", runtime.RetryCount, runtime.Stream)
	}
}

func TestInvalidReloadAndDeletionKeepLastKnownGood(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	if err := os.WriteFile(paths.GlobalConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("invalid reload unexpectedly succeeded")
	}
	if got := manager.Snapshot(); got.Revision != before.Revision || got.ActiveModelID != before.ActiveModelID {
		t.Fatalf("last-known-good changed: %#v", got)
	}
	valid, _ := marshalStarter(documentForPreset("deepseek"), "restored")
	if err := os.WriteFile(paths.GlobalConfig, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.GlobalConfig); err != nil {
		t.Fatal(err)
	}
	stable := manager.Snapshot()
	if err := manager.Reload(); err == nil {
		t.Fatal("deleted config reload unexpectedly succeeded")
	}
	if got := manager.Snapshot(); got.Revision != stable.Revision || !got.Ready {
		t.Fatalf("deleted config replaced snapshot: %#v", got)
	}
}

func TestExplicitReloadReResolvesCredentialWhenFileHashIsUnchanged(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	document := emptyDocument()
	document.Providers["private"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1", Auth: Auth{Credential: "provider/private"}}
	document.Models["private/model"] = Model{Provider: "private", Name: "model"}
	document.ActiveModel = "private/model"
	raw, _ := marshalStarter(document, "credential reload")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FakeCredentialStore{Values: map[string]string{"provider/private": "old-secret"}}
	manager := openTestManager(t, paths, store, false)
	before := manager.Snapshot()
	store.Values["provider/private"] = "new-secret"
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	after := manager.Snapshot()
	if after.Revision != before.Revision+1 || after.Active.APIKey != "new-secret" {
		t.Fatalf("reload revision/key=%d/%q before=%d", after.Revision, after.Active.APIKey, before.Revision)
	}
}

func TestWatcherObservesAtomicReplacement(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	doc := emptyDocument()
	doc.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	doc.Models["local/one"] = Model{Provider: "local", Name: "one"}
	doc.Models["local/two"] = Model{Provider: "local", Name: "two"}
	doc.ActiveModel = "local/one"
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := marshalStarter(doc, "watch test")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, true)
	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates
	doc.ActiveModel = "local/two"
	changed, _ := marshalStarter(doc, "watch test")
	if err := atomicWriteFile(paths.GlobalConfig, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-updates:
		if update.ActiveModelID != "local/two" {
			t.Fatalf("watch update active=%q", update.ActiveModelID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for config watcher")
	}
}

func TestLegacyMigrationIsIdempotentAndImportsPlaintextCredential(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":1,"activeModelProfileId":"deepseek","modelProfiles":[{"id":"deepseek","provider":"deepseek","apiKey":"legacy-secret","models":["deepseek-chat"],"model":"deepseek-chat"}]}`
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LegacyHome, "settings.json"), []byte(`{"ui":{"theme":"dracula"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LegacyHome, "mcp.toml"), []byte("# legacy mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacySkill := filepath.Join(paths.LegacyHome, "skills", "demo")
	if err := os.MkdirAll(legacySkill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySkill, "SKILL.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FakeCredentialStore{Values: map[string]string{}}
	manager := openTestManager(t, paths, store, false)
	if !manager.Snapshot().Ready {
		t.Fatalf("migrated snapshot not ready: %#v", manager.Snapshot().Diagnostics)
	}
	secret, err := store.Get(context.Background(), "provider/deepseek")
	if err != nil || secret != "legacy-secret" {
		t.Fatalf("migrated secret=%q err=%v", secret, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Home, "config-v1.backup.json")); err != nil {
		t.Fatalf("backup: %v", err)
	}
	for _, copied := range []string{paths.Settings, paths.MCP, filepath.Join(paths.Skills, "demo", "SKILL.md")} {
		if _, err := os.Stat(copied); err != nil {
			t.Fatalf("legacy asset %s: %v", copied, err)
		}
	}
	before, _ := os.ReadFile(paths.GlobalConfig)
	if _, _, err := migrateLegacy(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(paths.GlobalConfig)
	if string(before) != string(after) {
		t.Fatal("idempotent migration overwrote v2 config")
	}
}

func TestLegacyPlaintextIsNotMigratedWithoutKeyring(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":1,"modelProfiles":[{"id":"private","provider":"deepseek","apiKey":"must-not-leak","models":["deepseek-chat"]}]}`
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.Snapshot().Ready || manager.RequireReady() == nil || !strings.Contains(manager.Snapshot().Diagnostics[0].Message, "not migrated") {
		t.Fatalf("blocked migration snapshot=%#v", manager.Snapshot())
	}
	if _, statErr := os.Stat(paths.GlobalConfig); !os.IsNotExist(statErr) {
		t.Fatalf("v2 config should not exist, stat=%v", statErr)
	}
}
