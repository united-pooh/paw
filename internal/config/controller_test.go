package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"paw/internal/model"
)

type fakeModelRuntime struct {
	mu        sync.Mutex
	current   model.Config
	applied   []model.Config
	applyHook func(model.Config) error
}

func (f *fakeModelRuntime) CurrentModelConfig() model.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return model.CloneConfig(f.current)
}

func (f *fakeModelRuntime) ApplyModelConfig(cfg model.Config) error {
	cloned := model.CloneConfig(cfg)
	f.mu.Lock()
	hook := f.applyHook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(model.CloneConfig(cloned)); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = cloned
	f.applied = append(f.applied, cloned)
	return nil
}

func (f *fakeModelRuntime) state() (model.Config, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return model.CloneConfig(f.current), len(f.applied)
}

func (f *fakeModelRuntime) setApplyHook(hook func(model.Config) error) {
	f.mu.Lock()
	f.applyHook = hook
	f.mu.Unlock()
}

func (f *fakeModelRuntime) appliedModels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	models := make([]string, len(f.applied))
	for index, cfg := range f.applied {
		models[index] = cfg.Model
	}
	return models
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

func TestControllerSuccessfulActivationAppliesAndNotifiesCommittedRevisionOnce(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
	notifications := make(chan Snapshot, 3)
	harness.controller.SetSnapshotHandler(func(snapshot Snapshot) {
		notifications <- snapshot
	})
	initial := <-notifications
	_, beforeApplies := harness.runtime.state()

	if err := harness.controller.SetActiveModelID("local/a"); err != nil {
		t.Fatal(err)
	}
	committed := <-notifications
	if committed.Revision != initial.Revision+1 || committed.ActiveModelID != "local/a" {
		t.Fatalf("committed notification = revision %d active %q", committed.Revision, committed.ActiveModelID)
	}
	_, afterApplies := harness.runtime.state()
	if afterApplies != beforeApplies+1 {
		t.Fatalf("runtime apply count = %d, want %d", afterApplies, beforeApplies+1)
	}
	select {
	case duplicate := <-notifications:
		t.Fatalf("duplicate notification for committed update: revision %d active %q", duplicate.Revision, duplicate.ActiveModelID)
	case <-time.After(50 * time.Millisecond):
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

func assertControllerPersistenceUnchanged(t *testing.T, harness controllerTestHarness, before capturedControllerState) {
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
}

func assertControllerStateUnchanged(t *testing.T, harness controllerTestHarness, before capturedControllerState) {
	t.Helper()
	assertControllerPersistenceUnchanged(t, harness, before)
	current, applies := harness.runtime.state()
	if !reflect.DeepEqual(current, before.runtime) || applies != before.applies {
		t.Fatalf("runtime changed after failed activation: config=%#v applies=%d, want %#v/%d", current, applies, before.runtime, before.applies)
	}
}

func assertControllerRuntimeRestored(t *testing.T, harness controllerTestHarness, before capturedControllerState) {
	t.Helper()
	current, _ := harness.runtime.state()
	if !reflect.DeepEqual(current, before.runtime) {
		t.Fatalf("runtime config = %#v, want restored %#v", current, before.runtime)
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

func TestControllerActivationWriteFailureRollsBackRuntime(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
	before := captureControllerState(t, harness)
	harness.manager.configWriter = func(string, []byte, os.FileMode) error {
		return errors.New("forced config write failure")
	}

	if err := harness.controller.SetActiveModelID("local/a"); err == nil {
		t.Fatal("expected atomic write error")
	}
	assertControllerPersistenceUnchanged(t, harness, before)
	assertControllerRuntimeRestored(t, harness, before)
}

func TestControllerActivationRuntimeFailureLeavesPersistenceCoherent(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
	before := captureControllerState(t, harness)
	harness.runtime.setApplyHook(func(cfg model.Config) error {
		if cfg.Model == "a" {
			return errors.New("forced runtime apply failure")
		}
		return nil
	})

	if err := harness.controller.SetActiveModelID("local/a"); err == nil {
		t.Fatal("expected runtime apply error")
	}
	assertControllerPersistenceUnchanged(t, harness, before)
	assertControllerRuntimeRestored(t, harness, before)
}

func TestControllerProspectiveRuntimeDriftBeforeCommitRollsBack(t *testing.T) {
	store := &FakeCredentialStore{Values: map[string]string{"local-key": "old-secret"}}
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{Credential: "local-key"}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, store)
	before := captureControllerState(t, harness)
	harness.runtime.setApplyHook(func(cfg model.Config) error {
		if cfg.Model == "a" {
			return store.Set(context.Background(), "local-key", "new-secret")
		}
		return nil
	})

	err := harness.controller.SetActiveModelID("local/a")
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("activation error = %v, want prospective drift conflict", err)
	}
	assertControllerPersistenceUnchanged(t, harness, before)
	assertControllerRuntimeRestored(t, harness, before)
}

func TestControllerUpdateConfigRuntimeFailureLeavesPersistenceCoherent(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), nil, nil)
	before := captureControllerState(t, harness)
	harness.runtime.setApplyHook(func(cfg model.Config) error {
		if cfg.Model == "configured" {
			return errors.New("forced runtime apply failure")
		}
		return nil
	})

	if _, err := harness.controller.UpdateConfig(context.Background(), before.snapshot.Revision, []Operation{SetActiveModel("local/configured")}); err == nil {
		t.Fatal("expected runtime apply error")
	}
	assertControllerPersistenceUnchanged(t, harness, before)
	assertControllerRuntimeRestored(t, harness, before)
}

func TestControllerCommitConflictRollsBackRuntimeBeforeSubscriptionAppliesWinner(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), nil, nil)
	selection, err := harness.manager.Snapshot().CatalogSelection("local/configured")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var winnerErr error
	harness.runtime.setApplyHook(func(cfg model.Config) error {
		if cfg.Model == "configured" {
			once.Do(func() {
				_, winnerErr = harness.manager.Update(context.Background(), selection.Revision, []Operation{SetActiveModel("local/manual")})
			})
		}
		return winnerErr
	})

	err = harness.controller.ActivateCatalogSelection(selection)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("activation error = %v, want revision conflict", err)
	}
	if winnerErr != nil {
		t.Fatalf("winning update error = %v", winnerErr)
	}
	snapshot := harness.manager.Snapshot()
	if snapshot.ActiveModelID != "local/manual" || snapshot.Revision != selection.Revision+1 {
		t.Fatalf("winning snapshot = revision %d active %q", snapshot.Revision, snapshot.ActiveModelID)
	}
	current, _ := harness.runtime.state()
	if current.Model != "manual" {
		t.Fatalf("runtime model = %q, want winning manual model", current.Model)
	}
}

func TestControllerCatalogSelectionRejectsDiscoveredIDRemappedToConfiguredOccupant(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "a"}}, nil)
	selection, err := harness.manager.Snapshot().CatalogSelection("local/a")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Source != ModelSourceDiscovered {
		t.Fatalf("selection source = %q", selection.Source)
	}
	occupant := Model{Provider: "local", Name: "occupant", Adapter: AdapterDeepSeek}
	remapped, err := harness.manager.Update(context.Background(), selection.Revision, []Operation{UpsertModel(selection.ID, occupant)})
	if err != nil {
		t.Fatal(err)
	}
	if got := remapped.EffectiveModels[selection.ID]; got.Source != ModelSourceConfigured || !reflect.DeepEqual(got.Model, occupant) {
		t.Fatalf("remapped catalog entry = %#v", got)
	}

	err = harness.controller.ActivateCatalogSelection(selection)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("activation error = %v, want selection conflict", err)
	}
	after := harness.manager.Snapshot()
	if after.ActiveModelID != "local/manual" || !reflect.DeepEqual(after.Document.Models[selection.ID], occupant) {
		t.Fatalf("stale selection activated occupant: active=%q occupant=%#v", after.ActiveModelID, after.Document.Models[selection.ID])
	}
	for _, appliedModel := range harness.runtime.appliedModels() {
		if appliedModel == occupant.Name {
			t.Fatalf("configured occupant was applied to runtime: %v", harness.runtime.appliedModels())
		}
	}
}

func TestControllerSaveModelConfigCarriesResolvedSelectionThroughCommitConflict(t *testing.T) {
	harness := newControllerTestHarness(t, controllerDiscoveryDocument(Auth{}), []DiscoveredModel{{ProviderID: "local", Name: "b"}}, nil)
	origin := harness.manager.Snapshot()
	occupant := Model{Provider: "local", Name: "occupant"}
	var once sync.Once
	var winnerErr error
	harness.runtime.setApplyHook(func(cfg model.Config) error {
		if cfg.Model == "b" {
			once.Do(func() {
				_, winnerErr = harness.manager.Update(context.Background(), origin.Revision, []Operation{UpsertModel("local/b", occupant)})
			})
		}
		return winnerErr
	})

	err := harness.controller.SaveModelConfig(model.Config{ProfileID: "local", Model: "b"})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("save error = %v, want revision conflict", err)
	}
	if winnerErr != nil {
		t.Fatalf("winning remap error = %v", winnerErr)
	}
	after := harness.manager.Snapshot()
	if after.ActiveModelID != "local/manual" || !reflect.DeepEqual(after.Document.Models["local/b"], occupant) {
		t.Fatalf("save re-resolved remapped ID: active=%q occupant=%#v", after.ActiveModelID, after.Document.Models["local/b"])
	}
	current, _ := harness.runtime.state()
	if current.Model != "manual" {
		t.Fatalf("runtime model = %q, want restored manual", current.Model)
	}
}

func TestControllerCanonicalCatalogSelectionPreservesExactConfiguredID(t *testing.T) {
	document := controllerDiscoveryDocument(Auth{})
	document.Models[" local/spaced "] = Model{Provider: "local", Name: " spaced-name "}
	harness := newControllerTestHarness(t, document, nil, nil)

	if err := harness.controller.SetActiveModelID("local/spaced"); err != nil {
		t.Fatal(err)
	}
	after := harness.manager.Snapshot()
	if after.ActiveModelID != " local/spaced " || after.Document.ActiveModel != " local/spaced " {
		t.Fatalf("active ID = snapshot %q document %q, want exact catalog ID", after.ActiveModelID, after.Document.ActiveModel)
	}
	current, _ := harness.runtime.state()
	if current.Model != " spaced-name " {
		t.Fatalf("runtime model = %q, want configured catalog value", current.Model)
	}
}

func TestControllerCanonicalCatalogSelectionRejectsAmbiguousTrimmedID(t *testing.T) {
	document := controllerDiscoveryDocument(Auth{})
	document.Models["local/alias"] = Model{Provider: "local", Name: "first"}
	document.Models[" local/alias "] = Model{Provider: "local", Name: "second"}
	harness := newControllerTestHarness(t, document, nil, nil)
	before := captureControllerState(t, harness)

	if err := harness.controller.SetActiveModelID("  local/alias  "); err == nil {
		t.Fatal("expected ambiguous trimmed ID error")
	}
	assertControllerStateUnchanged(t, harness, before)

	if err := harness.controller.SetActiveModelID("local/alias"); err != nil {
		t.Fatalf("exact ID lookup should win before fallback: %v", err)
	}
	if got := harness.manager.Snapshot().ActiveModelID; got != "local/alias" {
		t.Fatalf("active ID = %q, want exact match", got)
	}
}

func TestControllerSaveModelConfigUsesCatalogIdentityNormalization(t *testing.T) {
	t.Run("normalized model name is ambiguous across configured aliases", func(t *testing.T) {
		document := controllerDiscoveryDocument(Auth{})
		document.Models["local/configured-space"] = Model{Provider: "local", Name: " configured "}
		harness := newControllerTestHarness(t, document, nil, nil)
		before := captureControllerState(t, harness)

		if err := harness.controller.SaveModelConfig(model.Config{ProfileID: "local", Model: " configured "}); err == nil {
			t.Fatal("expected normalized identity ambiguity")
		}
		assertControllerStateUnchanged(t, harness, before)
	})

	t.Run("exact whitespace provider key and exact catalog ID are preserved", func(t *testing.T) {
		const providerKey = " local "
		document := emptyDocument()
		document.Providers[providerKey] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
		document.Models["manual"] = Model{Provider: providerKey, Name: "manual"}
		document.Models[" selected alias "] = Model{Provider: providerKey, Name: " selected "}
		document.ActiveModel = "manual"
		harness := newControllerTestHarness(t, document, nil, nil)

		if err := harness.controller.SaveModelConfig(model.Config{ProfileID: providerKey, Provider: "ignored", Model: "selected"}); err != nil {
			t.Fatal(err)
		}
		if got := harness.manager.Snapshot().ActiveModelID; got != " selected alias " {
			t.Fatalf("active ID = %q, want exact configured alias", got)
		}
	})
}
