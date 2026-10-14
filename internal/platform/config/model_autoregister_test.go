package config

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func autoRegisterProvider(endpoint string) Provider {
	provider := discoveryTestProvider(endpoint)
	enabled := true
	provider.Discovery.AutoRegister = &enabled
	return provider
}

func readDocument(t *testing.T, paths Paths) Document {
	t.Helper()
	raw, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, _, err := parseAndValidateGlobal(raw, paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestManagerAutoRegisterAddsAndPrunesModelsOnStartup(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = autoRegisterProvider("http://127.0.0.1:1234/v1")
	document.Providers["other"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:9999/v1"}
	document.Models["local/one"] = Model{Provider: "local", Name: "one", ContextWindow: 64000}
	document.Models["local/gone"] = Model{Provider: "local", Name: "gone"}
	document.Models["other/keep"] = Model{Provider: "other", Name: "keep"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "two"}, {Name: "one"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	written := readDocument(t, paths)
	if _, ok := written.Models["local/two"]; !ok {
		t.Fatalf("new upstream model was not registered: %#v", written.Models)
	}
	if _, ok := written.Models["local/gone"]; ok {
		t.Fatalf("upstream-removed model was not pruned: %#v", written.Models)
	}
	if _, ok := written.Models["other/keep"]; !ok {
		t.Fatalf("unrelated provider model was pruned: %#v", written.Models)
	}
	if got := written.Models["local/one"].ContextWindow; got != 64000 {
		t.Fatalf("surviving manual model lost metadata: %#v", written.Models["local/one"])
	}
	if written.ActiveModel != "local/one" {
		t.Fatalf("activeModel = %q, want local/one", written.ActiveModel)
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 2 {
		t.Fatalf("revision = %d, want 2", snapshot.Revision)
	}
	if _, ok := snapshot.Document.Models["local/two"]; !ok {
		t.Fatalf("snapshot document missing registered model: %#v", snapshot.Document.Models)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, "auto-registration") {
			t.Fatalf("unexpected auto-registration diagnostic: %#v", diagnostic)
		}
	}
}

func TestManagerAutoRegisterFallsBackActiveModel(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = autoRegisterProvider("http://127.0.0.1:1234/v1")
	document.Models["local/gone"] = Model{Provider: "local", Name: "gone"}
	document.ActiveModel = "local/gone"
	writeManagerDocument(t, paths, document)

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "one"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	written := readDocument(t, paths)
	if written.ActiveModel != "local/one" {
		t.Fatalf("activeModel = %q, want local/one", written.ActiveModel)
	}
	snapshot := manager.Snapshot()
	if snapshot.ActiveModelID != "local/one" || !snapshot.Ready {
		t.Fatalf("snapshot active/ready = %q/%v", snapshot.ActiveModelID, snapshot.Ready)
	}
}

func TestManagerAutoRegisterDisabledLeavesConfigUntouched(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/gone"] = Model{Provider: "local", Name: "gone"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "two"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	written := readDocument(t, paths)
	if len(written.Models) != 2 {
		t.Fatalf("config models changed without autoRegister: %#v", written.Models)
	}
	snapshot := manager.Snapshot()
	if item, ok := snapshot.EffectiveModels["local/two"]; !ok || item.Source != ModelSourceDiscovered {
		t.Fatalf("discovered model not exposed as discovered source: %#v", snapshot.EffectiveModels)
	}
	if snapshot.Revision != 1 {
		t.Fatalf("revision = %d, want 1", snapshot.Revision)
	}
}

func TestManagerAutoRegisterSkipsFailedDiscovery(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = autoRegisterProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)

	discoverer := &fakeModelDiscoverer{err: errors.New("boom")}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	written := readDocument(t, paths)
	if len(written.Models) != 1 {
		t.Fatalf("failed discovery rewrote config: %#v", written.Models)
	}
	if manager.Snapshot().Revision != 1 {
		t.Fatalf("failed discovery advanced revision: %d", manager.Snapshot().Revision)
	}
}

func TestManagerAutoRegisterRefreshPrunesAndReportsWriteFailure(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = autoRegisterProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "one"}, {Name: "two"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, ok := readDocument(t, paths).Models["local/two"]; !ok {
		t.Fatalf("startup did not register two: %#v", readDocument(t, paths).Models)
	}

	// A refresh that prunes one model must persist the pruned list.
	discoverer.models = []DiscoveredModel{{Name: "two"}}
	refreshed, err := manager.RefreshModelDiscovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	written := readDocument(t, paths)
	if _, ok := written.Models["local/one"]; ok {
		t.Fatalf("refresh did not prune removed model: %#v", written.Models)
	}
	if written.ActiveModel != "local/two" {
		t.Fatalf("activeModel = %q, want local/two", written.ActiveModel)
	}
	if refreshed.ActiveModelID != "local/two" {
		t.Fatalf("refreshed active = %q", refreshed.ActiveModelID)
	}

	// A failed write must keep the previous config and surface a warning.
	manager.configWriter = func(configCASWriteRequest) error { return errors.New("disk full") }
	discoverer.models = []DiscoveredModel{{Name: "two"}, {Name: "three"}}
	refreshed, err = manager.RefreshModelDiscovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readDocument(t, paths).Models["local/three"]; ok {
		t.Fatalf("failed write still changed config: %#v", readDocument(t, paths).Models)
	}
	found := false
	for _, diagnostic := range refreshed.Diagnostics {
		if strings.Contains(diagnostic.Message, "model auto-registration could not write") {
			found = true
		}
	}
	if !found {
		t.Fatalf("write failure warning missing: %#v", refreshed.Diagnostics)
	}
	if _, ok := refreshed.EffectiveModels["local/three"]; !ok {
		t.Fatalf("failed write should still expose the discovered model: %#v", refreshed.EffectiveModels)
	}

	// A later successful refresh clears the stale warning.
	manager.configWriter = atomicWriteConfigFileCAS
	refreshed, err = manager.RefreshModelDiscovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range refreshed.Diagnostics {
		if strings.Contains(diagnostic.Message, "model auto-registration") {
			t.Fatalf("stale auto-registration warning survived: %#v", diagnostic)
		}
	}
	if _, ok := readDocument(t, paths).Models["local/three"]; !ok {
		t.Fatalf("recovered refresh did not register three: %#v", readDocument(t, paths).Models)
	}
}
