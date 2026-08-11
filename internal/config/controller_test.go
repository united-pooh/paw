package config

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sync"
	"testing"

	"paw/internal/model"
)

type fakeModelRuntime struct {
	mu      sync.Mutex
	current model.Config
	applied []model.Config
}

func (f *fakeModelRuntime) CurrentModelConfig() model.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return model.CloneConfig(f.current)
}

func (f *fakeModelRuntime) ApplyModelConfig(cfg model.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := model.CloneConfig(cfg)
	f.current = cloned
	f.applied = append(f.applied, cloned)
	return nil
}

func (f *fakeModelRuntime) state() (model.Config, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return model.CloneConfig(f.current), len(f.applied)
}

type controllerTestHarness struct {
	paths      Paths
	manager    *Manager
	controller *Controller
	runtime    *fakeModelRuntime
}

func newControllerTestHarness(t *testing.T, document Document, discovered []DiscoveredModel, store CredentialStore) controllerTestHarness {
	t.Helper()
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	writeManagerDocument(t, paths, document)
	if store == nil {
		store = &FakeCredentialStore{Unavailable: true}
	}
	manager, err := Open(context.Background(), Options{
		Paths:        paths,
		Credentials:  store,
		DisableWatch: true,
		Discoverer:   &fakeModelDiscoverer{models: discovered},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeModelRuntime{}
	controller := NewController(manager, runtime)
	t.Cleanup(func() { _ = controller.Close() })
	return controllerTestHarness{paths: paths, manager: manager, controller: controller, runtime: runtime}
}

func controllerDiscoveryDocument(auth Auth) Document {
	document := emptyDocument()
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	provider.Auth = auth
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.Models["local/configured"] = Model{Provider: "local", Name: "configured", Adapter: AdapterGPT}
	document.ActiveModel = "local/manual"
	return document
}

func TestControllerSetActiveModelIDPinsOnlySelectedDiscoveredModel(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{
		{ProviderID: "local", Name: "a"},
		{ProviderID: "local", Name: "b"},
	}, nil)
	before := harness.manager.Snapshot()
	_, beforeApplies := harness.runtime.state()
	selected := before.EffectiveModels["local/a"]
	if selected.Source != ModelSourceDiscovered {
		t.Fatalf("selected source = %q, want %q", selected.Source, ModelSourceDiscovered)
	}

	if err := harness.controller.SetActiveModelID("local/a"); err != nil {
		t.Fatal(err)
	}

	after := harness.manager.Snapshot()
	if after.Revision != before.Revision+1 {
		t.Fatalf("revision = %d, want %d", after.Revision, before.Revision+1)
	}
	if after.ActiveModelID != "local/a" {
		t.Fatalf("active = %q", after.ActiveModelID)
	}
	if got, ok := after.Document.Models["local/a"]; !ok || !reflect.DeepEqual(got, selected.Model) {
		t.Fatalf("selected model was not persisted: %#v", got)
	}
	if _, ok := after.Document.Models["local/b"]; ok {
		t.Fatal("unselected discovered model was persisted")
	}
	if got := after.EffectiveModels["local/a"].Source; got != ModelSourceConfigured {
		t.Fatalf("persisted source = %q, want %q", got, ModelSourceConfigured)
	}
	current, afterApplies := harness.runtime.state()
	if current.ProfileID != "local" || current.Model != "a" {
		t.Fatalf("runtime config = %#v", current)
	}
	if afterApplies != beforeApplies+1 {
		t.Fatalf("runtime apply count = %d, want %d", afterApplies, beforeApplies+1)
	}
}

func TestControllerSaveModelConfigPinsMatchedDiscoveredModel(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{
		{ProviderID: "local", Name: "a"},
		{ProviderID: "local", Name: "b"},
	}, nil)
	before := harness.manager.Snapshot()

	if err := harness.controller.SaveModelConfig(model.Config{ProfileID: "local", Provider: "ignored", Model: "b"}); err != nil {
		t.Fatal(err)
	}

	after := harness.manager.Snapshot()
	if after.Revision != before.Revision+1 || after.ActiveModelID != "local/b" {
		t.Fatalf("revision/active = %d/%q, want %d/local/b", after.Revision, after.ActiveModelID, before.Revision+1)
	}
	if _, ok := after.Document.Models["local/b"]; !ok {
		t.Fatal("matched discovered model was not persisted")
	}
	if _, ok := after.Document.Models["local/a"]; ok {
		t.Fatal("unselected discovered model was persisted")
	}
	current, _ := harness.runtime.state()
	if current.ProfileID != "local" || current.Model != "b" {
		t.Fatalf("runtime config = %#v", current)
	}
}

func TestControllerConfiguredActivationOnlySwitchesActiveModel(t *testing.T) {
	tests := []struct {
		name     string
		activate func(*Controller) error
	}{
		{name: "set active ID", activate: func(controller *Controller) error {
			return controller.SetActiveModelID("  local/configured  ")
		}},
		{name: "save model config", activate: func(controller *Controller) error {
			return controller.SaveModelConfig(model.Config{Provider: "local", Model: "configured"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
			before := harness.manager.Snapshot()
			_, beforeApplies := harness.runtime.state()

			if err := test.activate(harness.controller); err != nil {
				t.Fatal(err)
			}

			after := harness.manager.Snapshot()
			if after.Revision != before.Revision+1 || after.ActiveModelID != "local/configured" {
				t.Fatalf("revision/active = %d/%q, want %d/local/configured", after.Revision, after.ActiveModelID, before.Revision+1)
			}
			if !reflect.DeepEqual(after.Document.Models, before.Document.Models) {
				t.Fatalf("configured activation changed models:\nbefore=%#v\nafter=%#v", before.Document.Models, after.Document.Models)
			}
			if _, ok := after.Document.Models["local/a"]; ok {
				t.Fatal("configured activation persisted a discovered model")
			}
			current, afterApplies := harness.runtime.state()
			if current.Model != "configured" || afterApplies != beforeApplies+1 {
				t.Fatalf("runtime model/applies = %q/%d, want configured/%d", current.Model, afterApplies, beforeApplies+1)
			}
		})
	}
}

type capturedControllerState struct {
	snapshot Snapshot
	file     []byte
	runtime  model.Config
	applies  int
}

func captureControllerState(t *testing.T, harness controllerTestHarness) capturedControllerState {
	t.Helper()
	raw, err := os.ReadFile(harness.paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	current, applies := harness.runtime.state()
	return capturedControllerState{snapshot: harness.manager.Snapshot(), file: raw, runtime: current, applies: applies}
}

func assertControllerStateUnchanged(t *testing.T, harness controllerTestHarness, before capturedControllerState) {
	t.Helper()
	after := harness.manager.Snapshot()
	if !reflect.DeepEqual(after, before.snapshot) {
		t.Fatalf("snapshot changed after failed activation:\nbefore=%#v\nafter=%#v", before.snapshot, after)
	}
	raw, err := os.ReadFile(harness.paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, before.file) {
		t.Fatalf("config file changed after failed activation:\nbefore=%s\nafter=%s", before.file, raw)
	}
	current, applies := harness.runtime.state()
	if !reflect.DeepEqual(current, before.runtime) || applies != before.applies {
		t.Fatalf("runtime changed after failed activation: config=%#v applies=%d, want %#v/%d", current, applies, before.runtime, before.applies)
	}
}

func TestControllerRejectsModelsOutsideEffectiveCatalogWithoutSideEffects(t *testing.T) {
	t.Run("unknown ID", func(t *testing.T) {
		harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
		before := captureControllerState(t, harness)
		if err := harness.controller.SetActiveModelID("local/missing"); err == nil {
			t.Fatal("expected unknown model error")
		}
		assertControllerStateUnchanged(t, harness, before)
	})

	t.Run("unknown provider and model", func(t *testing.T) {
		harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
		before := captureControllerState(t, harness)
		if err := harness.controller.SaveModelConfig(model.Config{Provider: "missing", Model: "a"}); err == nil {
			t.Fatal("expected unmatched model error")
		}
		assertControllerStateUnchanged(t, harness, before)
	})

	t.Run("ambiguous configured identity", func(t *testing.T) {
		document := controllerDiscoveryDocument(Auth{})
		document.Models["local/configured-alias"] = Model{Provider: "local", Name: "configured"}
		harness := newControllerTestHarness(t, document, nil, nil)
		before := captureControllerState(t, harness)
		if err := harness.controller.SaveModelConfig(model.Config{ProfileID: "local", Model: "configured"}); err == nil {
			t.Fatal("expected ambiguous model error")
		}
		assertControllerStateUnchanged(t, harness, before)
	})
}

func TestControllerActivationValidationFailureLeavesStateUnchanged(t *testing.T) {
	store := &FakeCredentialStore{Values: map[string]string{"local-key": "secret"}}
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{Credential: "local-key"}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, store)
	before := captureControllerState(t, harness)
	if err := store.Delete(context.Background(), "local-key"); err != nil {
		t.Fatal(err)
	}

	if err := harness.controller.SetActiveModelID("local/a"); err == nil {
		t.Fatal("expected activation validation error")
	}
	assertControllerStateUnchanged(t, harness, before)
}

func TestControllerActivationWriteFailureLeavesStateUnchanged(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
	before := captureControllerState(t, harness)
	if err := os.Chmod(harness.paths.Home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(harness.paths.Home, 0o700) })
	probe, err := os.CreateTemp(harness.paths.Home, ".controller-write-probe-*")
	if err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		_ = os.Chmod(harness.paths.Home, 0o700)
		t.Skip("filesystem does not enforce owner write permissions")
	}

	if err := harness.controller.SetActiveModelID("local/a"); err == nil {
		t.Fatal("expected atomic write error")
	}
	assertControllerStateUnchanged(t, harness, before)
}
