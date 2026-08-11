package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildEffectiveCatalogManualOverridesDiscovered(t *testing.T) {
	stream := false
	document := Document{
		Providers: map[string]Provider{"local": {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}}},
		Models: map[string]Model{
			"custom-id": {
				Provider: "local", Name: "chat-model", Adapter: AdapterDeepSeek,
				ContextWindow: 128000, Stream: &stream,
			},
		},
	}
	discovered := map[string][]DiscoveredModel{
		"local": {{ProviderID: "local", Name: "chat-model"}, {ProviderID: "local", Name: "other"}},
	}

	catalog, stats := buildEffectiveCatalog(document, discovered)

	if len(catalog) != 2 || stats.Merged != 2 {
		t.Fatalf("catalog=%#v stats=%#v", catalog, stats)
	}
	got := catalog["custom-id"]
	if got.Source != ModelSourceConfigured || got.Model.Adapter != AdapterDeepSeek || got.Model.ContextWindow != 128000 {
		t.Fatalf("manual override lost: %#v", got)
	}
	if got.Model.Stream == nil || *got.Model.Stream {
		t.Fatalf("manual stream override lost: %#v", got.Model.Stream)
	}
}

func TestBuildEffectiveCatalogSameNameDifferentProvidersDoNotConflict(t *testing.T) {
	document := Document{
		Providers: map[string]Provider{
			"first":  {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}},
			"second": {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}},
		},
		Models: map[string]Model{},
	}
	discovered := map[string][]DiscoveredModel{
		"first":  {{ProviderID: "first", Name: "shared"}},
		"second": {{ProviderID: "second", Name: "shared"}},
	}

	catalog, stats := buildEffectiveCatalog(document, discovered)

	if stats.Merged != 2 {
		t.Fatalf("catalog=%#v stats=%#v", catalog, stats)
	}
	for _, id := range []string{"first/shared", "second/shared"} {
		if got, ok := catalog[id]; !ok || got.Model.Provider+"/"+got.Model.Name != id {
			t.Fatalf("catalog[%q]=%#v present=%v", id, got, ok)
		}
	}
}

func TestBuildEffectiveCatalogStableIDCollisionAppendsHash(t *testing.T) {
	document := Document{
		Providers: map[string]Provider{
			"local": {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}},
			"other": {},
		},
		Models: map[string]Model{
			"local/chat": {Provider: "other", Name: "manual"},
		},
	}
	discovered := map[string][]DiscoveredModel{
		"local": {{ProviderID: "local", Name: "chat"}},
	}

	catalog, _ := buildEffectiveCatalog(document, discovered)

	hash := sha256.Sum256([]byte("local\x00chat"))
	id := fmt.Sprintf("local/chat~%x", hash[:4])
	got, ok := catalog[id]
	if !ok {
		t.Fatalf("catalog=%#v, missing collision ID %q", catalog, id)
	}
	if got.Source != ModelSourceDiscovered || got.Model.Provider != "local" || got.Model.Name != "chat" {
		t.Fatalf("discovered collision entry=%#v", got)
	}
	if manual := catalog["local/chat"]; manual.Source != ModelSourceConfigured || manual.Model.Provider != "other" {
		t.Fatalf("manual collision entry=%#v", manual)
	}
}

func TestBuildEffectiveCatalogIgnoresDiscoveryUnlessResolvedEnabled(t *testing.T) {
	disabled := false
	document := Document{
		Providers: map[string]Provider{
			"absent":      {},
			"unspecified": {Discovery: &DiscoveryConfig{Path: "models"}},
			"disabled":    {Discovery: &DiscoveryConfig{Enabled: &disabled}},
		},
		Models: map[string]Model{
			"configured-absent":      {Provider: "absent", Name: "configured"},
			"configured-unspecified": {Provider: "unspecified", Name: "configured"},
			"configured-disabled":    {Provider: "disabled", Name: "configured"},
		},
	}
	discovered := map[string][]DiscoveredModel{
		"absent":      {{ProviderID: "absent", Name: "retained"}},
		"unspecified": {{ProviderID: "unspecified", Name: "retained"}},
		"disabled":    {{ProviderID: "disabled", Name: "retained"}},
	}

	catalog, stats := buildEffectiveCatalog(document, discovered)

	if len(catalog) != len(document.Models) || stats.Merged != len(document.Models) {
		t.Fatalf("catalog=%#v stats=%#v", catalog, stats)
	}
	if stats.Discovered != 0 || stats.Filtered != 0 {
		t.Fatalf("disabled discovery affected stats: %#v", stats)
	}
	for id := range document.Models {
		if got, ok := catalog[id]; !ok || got.Source != ModelSourceConfigured {
			t.Fatalf("configured model %q lost: %#v present=%v", id, got, ok)
		}
	}
}

func TestBuildEffectiveCatalogPreservesConfiguredAliases(t *testing.T) {
	document := Document{
		ActiveModel: "alias-z",
		Providers: map[string]Provider{
			"local": {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}},
		},
		Models: map[string]Model{
			"alias-a": {Provider: "local", Name: "chat", Adapter: AdapterGPT},
			"alias-z": {Provider: "local", Name: "chat", Adapter: AdapterDeepSeek, ContextWindow: 128000},
		},
	}
	discovered := map[string][]DiscoveredModel{
		"local": {{ProviderID: "local", Name: "chat"}},
	}

	catalog, stats := buildEffectiveCatalog(document, discovered)

	if len(catalog) != 2 || stats.Merged != 2 {
		t.Fatalf("catalog=%#v stats=%#v", catalog, stats)
	}
	if _, exists := catalog["local/chat"]; exists {
		t.Fatalf("discovered-only duplicate was retained: %#v", catalog["local/chat"])
	}
	for _, id := range []string{"alias-a", document.ActiveModel} {
		if got, ok := catalog[id]; !ok || got.Source != ModelSourceConfigured {
			t.Fatalf("configured alias %q lost: %#v present=%v", id, got, ok)
		}
	}
	active := catalog[document.ActiveModel]
	if active.Model.Adapter != AdapterDeepSeek || active.Model.ContextWindow != 128000 {
		t.Fatalf("active alias metadata changed: %#v", active)
	}
}

func TestFilterDiscoveredModelsIncludeRestoresAndExcludeWins(t *testing.T) {
	cfg := DiscoveryConfig{Include: []string{"text-embedding-*"}, Exclude: []string{"*-private"}}
	got, filtered := filterDiscoveredModels([]string{"text-embedding-3", "text-embedding-private", "chat"}, cfg)
	if !slices.Equal([]string{"text-embedding-3"}, got) {
		t.Fatalf("models=%v", got)
	}
	if filtered != 2 {
		t.Fatalf("filtered=%d", filtered)
	}
}

func TestFilterDiscoveredModelsRejectsUnsafeUniqueNames(t *testing.T) {
	oversized := strings.Repeat("界", 171)
	if len(oversized) <= 512 {
		t.Fatalf("oversized test name is only %d bytes", len(oversized))
	}
	unicodeName := "模型/聊天-β"
	got, filtered := filterDiscoveredModels([]string{
		"bad\x1bmodel",
		"\nchat-model",
		oversized,
		"bad\x1bmodel",
		unicodeName,
	}, DiscoveryConfig{})
	if !slices.Equal([]string{unicodeName}, got) {
		t.Fatalf("models=%q", got)
	}
	if filtered != 3 {
		t.Fatalf("filtered=%d want=3", filtered)
	}
}

func TestBuildEffectiveCatalogTrimsProviderIDsWithoutChangingDocumentIdentity(t *testing.T) {
	const providerKey = " local "
	document := Document{
		Providers: map[string]Provider{
			providerKey: {Discovery: &DiscoveryConfig{Enabled: boolPointer(true)}},
		},
		Models: map[string]Model{
			"manual-alias": {Provider: providerKey, Name: "manual"},
		},
	}
	discovered := map[string][]DiscoveredModel{
		providerKey: {
			{ProviderID: "local", Name: "manual"},
			{ProviderID: " local ", Name: "chat"},
		},
	}

	catalog, stats := buildEffectiveCatalog(document, discovered)

	if len(catalog) != 2 || stats.Discovered != 2 || stats.Filtered != 0 || stats.Merged != 2 {
		t.Fatalf("catalog=%#v stats=%#v", catalog, stats)
	}
	if _, exists := catalog["local/manual"]; exists {
		t.Fatalf("exact configured identity did not suppress discovery: %#v", catalog["local/manual"])
	}
	discoveredChat, ok := catalog["local/chat"]
	if !ok {
		t.Fatalf("catalog=%#v, missing normalized base ID", catalog)
	}
	if discoveredChat.Source != ModelSourceDiscovered || discoveredChat.Model.Provider != providerKey || discoveredChat.Model.Name != "chat" {
		t.Fatalf("discovered model lost exact provider identity: %#v", discoveredChat)
	}

	occupied := map[string]CatalogModel{
		"local/chat": {Model: Model{Provider: "local", Name: "chat"}},
	}
	hash := sha256.Sum256([]byte("local\x00chat"))
	wantCollisionID := fmt.Sprintf("local/chat~%x", hash[:4])
	if got := stableDiscoveredModelID(providerKey, "chat", occupied); got != wantCollisionID {
		t.Fatalf("trim-equivalent identity collision ID=%q want=%q", got, wantCollisionID)
	}
}

func TestMergePresetDiscoveryDefaultsAndExplicitDisable(t *testing.T) {
	resolved := mergePreset("openai", Provider{})
	if resolved.Discovery == nil || resolved.Discovery.Enabled == nil || !*resolved.Discovery.Enabled {
		t.Fatalf("openai discovery=%#v", resolved.Discovery)
	}
	if resolved.Discovery.Path != "models" || resolved.Discovery.Format != DiscoveryFormatOpenAIList {
		t.Fatalf("openai discovery=%#v", resolved.Discovery)
	}

	disabled := false
	resolved = mergePreset("openai", Provider{Discovery: &DiscoveryConfig{Enabled: &disabled, Exclude: []string{"preview-*"}}})
	if resolved.Discovery == nil || resolved.Discovery.Enabled == nil || *resolved.Discovery.Enabled {
		t.Fatalf("explicit disable lost: %#v", resolved.Discovery)
	}
	if resolved.Discovery.Path != "models" || resolved.Discovery.Format != DiscoveryFormatOpenAIList || !slices.Equal(resolved.Discovery.Exclude, []string{"preview-*"}) {
		t.Fatalf("field merge failed: %#v", resolved.Discovery)
	}

	ollama := mergePreset("ollama", Provider{})
	if ollama.Discovery == nil || ollama.Discovery.Path != "/api/tags" || ollama.Discovery.Format != DiscoveryFormatOllamaTags {
		t.Fatalf("ollama discovery=%#v", ollama.Discovery)
	}
}

func TestMergePresetPreservesNilAndExplicitEmptyDiscoveryFilters(t *testing.T) {
	resolved := mergePreset("openai", Provider{Discovery: &DiscoveryConfig{Include: []string{}, Exclude: nil}})
	if resolved.Discovery == nil || resolved.Discovery.Include == nil || len(resolved.Discovery.Include) != 0 {
		t.Fatalf("explicitly empty include was lost: %#v", resolved.Discovery)
	}
	if resolved.Discovery.Exclude != nil {
		t.Fatalf("nil exclude became non-nil: %#v", resolved.Discovery.Exclude)
	}

	resolved = mergePreset("openai", Provider{Discovery: &DiscoveryConfig{Include: nil, Exclude: []string{}}})
	if resolved.Discovery == nil || resolved.Discovery.Exclude == nil || len(resolved.Discovery.Exclude) != 0 {
		t.Fatalf("explicitly empty exclude was lost: %#v", resolved.Discovery)
	}
	if resolved.Discovery.Include != nil {
		t.Fatalf("nil include became non-nil: %#v", resolved.Discovery.Include)
	}
}

func TestValidateProviderDiscovery(t *testing.T) {
	enabled := true
	valid := DiscoveryConfig{
		Enabled: &enabled, Path: "models", Format: DiscoveryFormatOpenAIList,
		TimeoutSeconds: 3, Include: []string{"org/*"}, Exclude: []string{"*-private"},
	}
	if err := validateDiscoveryConfig("local", valid); err != nil {
		t.Fatalf("valid discovery: %v", err)
	}

	tests := []struct {
		name string
		cfg  DiscoveryConfig
		want string
	}{
		{name: "format", cfg: DiscoveryConfig{Format: "xml"}, want: "format is unsupported"},
		{name: "negative timeout", cfg: DiscoveryConfig{TimeoutSeconds: -1}, want: "between 1 and 10"},
		{name: "large timeout", cfg: DiscoveryConfig{TimeoutSeconds: 11}, want: "between 1 and 10"},
		{name: "absolute URL", cfg: DiscoveryConfig{Path: "https://evil.invalid/models"}, want: "same-origin path"},
		{name: "network path", cfg: DiscoveryConfig{Path: "//evil.invalid/models"}, want: "same-origin path"},
		{name: "query", cfg: DiscoveryConfig{Path: "models?token=no"}, want: "query or fragment"},
		{name: "parent segment", cfg: DiscoveryConfig{Path: "models/../secrets"}, want: "parent path segment"},
		{name: "glob", cfg: DiscoveryConfig{Include: []string{"[broken"}}, want: "syntax error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiscoveryConfig("local", tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseAndValidateGlobalDiscoverySchema(t *testing.T) {
	valid := []byte(`{"schemaVersion":2,"providers":{"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1","discovery":{"enabled":true,"path":"models","format":"openai-list","timeoutSeconds":3,"include":["org/*"],"exclude":[]}}},"models":{}}`)
	if _, _, err := parseAndValidateGlobal(valid, "config.jsonc"); err != nil {
		t.Fatalf("valid schema: %v", err)
	}
	invalid := []byte(`{"schemaVersion":2,"providers":{"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1","discovery":{"unknown":true}}},"models":{}}`)
	if _, _, err := parseAndValidateGlobal(invalid, "config.jsonc"); err == nil {
		t.Fatal("unknown discovery property was accepted")
	}
}

func TestCloneProviderPreservesNilAndExplicitEmptyDiscoveryFilters(t *testing.T) {
	tests := []struct {
		name     string
		include  []string
		exclude  []string
		wantIncl bool
		wantExcl bool
	}{
		{name: "nil", include: nil, exclude: nil},
		{name: "empty include", include: []string{}, exclude: nil, wantIncl: true},
		{name: "empty exclude", include: nil, exclude: []string{}, wantExcl: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloned := cloneProvider(Provider{Discovery: &DiscoveryConfig{Include: tt.include, Exclude: tt.exclude}})
			if got := cloned.Discovery.Include != nil; got != tt.wantIncl {
				t.Fatalf("include non-nil=%v want=%v (%#v)", got, tt.wantIncl, cloned.Discovery.Include)
			}
			if got := cloned.Discovery.Exclude != nil; got != tt.wantExcl {
				t.Fatalf("exclude non-nil=%v want=%v (%#v)", got, tt.wantExcl, cloned.Discovery.Exclude)
			}
		})
	}
}

func TestSnapshotClonePreservesNilAndExplicitEmptyDiscoveryFilters(t *testing.T) {
	snapshot := Snapshot{Document: Document{Providers: map[string]Provider{
		"empty-include": {Discovery: &DiscoveryConfig{Include: []string{}, Exclude: nil}},
		"empty-exclude": {Discovery: &DiscoveryConfig{Include: nil, Exclude: []string{}}},
	}}}

	cloned := snapshot.Clone()
	include := cloned.Document.Providers["empty-include"].Discovery
	if include.Include == nil || include.Exclude != nil {
		t.Fatalf("include clone lost nil/empty distinction: %#v", include)
	}
	exclude := cloned.Document.Providers["empty-exclude"].Discovery
	if exclude.Include != nil || exclude.Exclude == nil {
		t.Fatalf("exclude clone lost nil/empty distinction: %#v", exclude)
	}
}

func TestSnapshotCloneDeepCopiesEffectiveCatalogAndDiscoveryConfig(t *testing.T) {
	enabled := true
	stream := false
	attemptedAt := time.Unix(10, 20).UTC()
	snapshot := Snapshot{
		Document: Document{
			Providers: map[string]Provider{"local": {Discovery: &DiscoveryConfig{Enabled: &enabled, Include: []string{"a*"}, Exclude: []string{"b*"}}}},
			Models:    map[string]Model{},
		},
		EffectiveModels: map[string]CatalogModel{
			"local/chat": {ID: "local/chat", Source: ModelSourceDiscovered, Model: Model{Provider: "local", Name: "chat", Stream: &stream, Parameters: map[string]any{"nested": map[string]any{"value": true}}}},
		},
		Discovery: DiscoveryStatus{Attempted: true, AttemptedAt: attemptedAt},
	}

	clone := snapshot.Clone()
	*clone.Document.Providers["local"].Discovery.Enabled = false
	clone.Document.Providers["local"].Discovery.Include[0] = "changed"
	item := clone.EffectiveModels["local/chat"]
	*item.Model.Stream = true
	item.Model.Parameters["nested"].(map[string]any)["value"] = false
	clone.EffectiveModels["local/chat"] = item

	originalDiscovery := snapshot.Document.Providers["local"].Discovery
	if !*originalDiscovery.Enabled || originalDiscovery.Include[0] != "a*" {
		t.Fatalf("provider discovery was aliased: %#v", originalDiscovery)
	}
	original := snapshot.EffectiveModels["local/chat"].Model
	if *original.Stream || original.Parameters["nested"].(map[string]any)["value"] != true {
		t.Fatalf("catalog model was aliased: %#v", original)
	}
	if clone.Discovery.AttemptedAt != attemptedAt || snapshot.Discovery.AttemptedAt != attemptedAt {
		t.Fatalf("discovery time changed: clone=%v original=%v", clone.Discovery.AttemptedAt, snapshot.Discovery.AttemptedAt)
	}
}

func TestEnsureSchemaUpdatesChangedContentAndSkipsMatchingContent(t *testing.T) {
	root := t.TempDir()
	paths := Paths{Schemas: filepath.Join(root, "schemas"), Schema: filepath.Join(root, "schemas", "config-v2.schema.json")}
	if err := os.MkdirAll(paths.Schemas, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Schema, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchema(paths); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(paths.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, SchemaBytes()) {
		t.Fatal("changed schema was not updated")
	}
	info, err := os.Stat(paths.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("updated schema mode=%v", info.Mode().Perm())
	}

	if err := os.Chmod(paths.Schema, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchema(paths); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(paths.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("matching schema was unexpectedly rewritten: mode=%v", info.Mode().Perm())
	}
}
