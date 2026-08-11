package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"paw/internal/model"
)

type ModelRuntime interface {
	CurrentModelConfig() model.Config
	ApplyModelConfig(model.Config) error
}

// Controller bridges the durable registry and the model runtime. It also
// satisfies the legacy TUI model-controller interface while keeping configured
// selections to activeModel-only updates and pinning only selected discoveries.
type Controller struct {
	manager         *Manager
	runtime         ModelRuntime
	stop            func()
	once            sync.Once
	applyMu         sync.Mutex
	appliedRevision uint64
	handlerMu       sync.RWMutex
	handler         func(Snapshot)
}

func NewController(manager *Manager, runtime ModelRuntime) *Controller {
	controller := &Controller{manager: manager, runtime: runtime}
	if manager != nil && runtime != nil {
		initial := manager.Snapshot()
		_ = controller.applySnapshot(initial)
		updates, stop := manager.Subscribe()
		controller.stop = stop
		go func() {
			for snapshot := range updates {
				_ = controller.applySnapshot(snapshot)
			}
		}()
	}
	return controller
}

// applySnapshot serializes runtime swaps and makes Controller updates
// synchronous. The subscription can observe the same revision afterwards;
// appliedRevision turns that delivery into a no-op.
func (c *Controller) applySnapshot(snapshot Snapshot) error {
	if c == nil || c.runtime == nil || !snapshot.Ready {
		return nil
	}
	c.applyMu.Lock()
	if snapshot.Revision <= c.appliedRevision {
		c.applyMu.Unlock()
		return nil
	}
	if err := c.runtime.ApplyModelConfig(snapshot.Active); err != nil {
		c.applyMu.Unlock()
		return err
	}
	c.appliedRevision = snapshot.Revision
	c.applyMu.Unlock()
	c.notify(snapshot)
	return nil
}

// SetSnapshotHandler observes successfully applied runtime snapshots. It is
// used to keep runner-level context limits aligned with external hot reloads.
func (c *Controller) SetSnapshotHandler(handler func(Snapshot)) {
	if c == nil {
		return
	}
	c.handlerMu.Lock()
	c.handler = handler
	c.handlerMu.Unlock()
	if handler != nil && c.manager != nil {
		snapshot := c.manager.Snapshot()
		if snapshot.Ready {
			handler(snapshot)
		}
	}
}

func (c *Controller) notify(snapshot Snapshot) {
	c.handlerMu.RLock()
	handler := c.handler
	c.handlerMu.RUnlock()
	if handler != nil {
		handler(snapshot.Clone())
	}
}

func (c *Controller) CurrentModelConfig() model.Config {
	if c == nil || c.manager == nil {
		return model.Config{}
	}
	return c.manager.Snapshot().Active
}

func (c *Controller) ApplyModelConfig(model.Config) error {
	if c == nil || c.manager == nil || c.runtime == nil {
		return nil
	}
	snapshot := c.manager.Snapshot()
	if !snapshot.Ready {
		return c.manager.RequireReady()
	}
	return c.applySnapshot(snapshot)
}

func (c *Controller) SaveModelConfig(value model.Config) error {
	if c == nil || c.manager == nil {
		return fmt.Errorf("configuration manager is unavailable")
	}
	snapshot := c.manager.Snapshot()
	wantedProvider := value.ProfileID
	if strings.TrimSpace(wantedProvider) == "" {
		wantedProvider = value.Provider
	}
	wantedIdentity := modelIdentity{providerID: wantedProvider, name: strings.TrimSpace(value.Model)}
	var selection CatalogSelection
	matches := 0
	for id, item := range snapshot.EffectiveModels {
		if identityForModel(item.Model) != wantedIdentity {
			continue
		}
		selection = newCatalogSelection(snapshot.Revision, id, item)
		matches++
	}
	switch matches {
	case 0:
		return fmt.Errorf("model %q under provider %q is not in the effective catalog", wantedIdentity.name, wantedIdentity.providerID)
	case 1:
		return c.ActivateCatalogSelection(selection)
	default:
		return fmt.Errorf("model %q is ambiguous under provider %q", wantedIdentity.name, wantedIdentity.providerID)
	}
}

// ActivateCatalogSelection activates the exact catalog identity observed by a
// selector. It rejects stale revisions and any ID whose identity or source has
// changed since selection.
func (c *Controller) ActivateCatalogSelection(selection CatalogSelection) error {
	if c == nil || c.manager == nil {
		return fmt.Errorf("configuration manager is unavailable")
	}
	c.applyMu.Lock()
	updated, notify, err := c.activateCatalogSelectionLocked(context.Background(), selection)
	c.applyMu.Unlock()
	if err != nil {
		return err
	}
	if notify {
		c.notify(updated)
	}
	return nil
}

func (c *Controller) activateCatalogSelectionLocked(ctx context.Context, selection CatalogSelection) (Snapshot, bool, error) {
	current := c.manager.Snapshot()
	item, ok := current.EffectiveModels[selection.ID]
	if !ok || !selection.matches(item) {
		return Snapshot{}, false, fmt.Errorf("%w: selected model %q no longer maps to the same catalog identity and source", ErrRevisionConflict, selection.ID)
	}
	operations := make([]Operation, 0, 2)
	if selection.Source == ModelSourceDiscovered {
		operations = append(operations, UpsertModel(selection.ID, item.Model))
	}
	operations = append(operations, setActiveModelExact(selection.ID))
	return c.commitCatalogSelectionLocked(ctx, selection, operations)
}

func (c *Controller) Manager() *Manager {
	if c == nil {
		return nil
	}
	return c.manager
}
func (c *Controller) Snapshot() Snapshot {
	if c == nil || c.manager == nil {
		return Snapshot{}
	}
	return c.manager.Snapshot()
}
func (c *Controller) ReloadConfig() error {
	if c == nil || c.manager == nil {
		return fmt.Errorf("configuration manager is unavailable")
	}
	if err := c.manager.Reload(); err != nil {
		return err
	}
	return c.applySnapshot(c.manager.Snapshot())
}
func (c *Controller) ConfigPath() string {
	if c == nil || c.manager == nil {
		return ""
	}
	return c.manager.ConfigPath()
}

func (c *Controller) SetActiveModelID(id string) error {
	if c == nil || c.manager == nil {
		return fmt.Errorf("configuration manager is unavailable")
	}
	selection, err := c.manager.Snapshot().CatalogSelection(id)
	if err != nil {
		return err
	}
	return c.ActivateCatalogSelection(selection)
}
func (c *Controller) UpdateConfig(ctx context.Context, revision uint64, operations []Operation) (Snapshot, error) {
	if c == nil || c.manager == nil {
		return Snapshot{}, fmt.Errorf("configuration manager is unavailable")
	}
	c.applyMu.Lock()
	snapshot, notify, err := c.commitOperationsLocked(ctx, revision, operations)
	c.applyMu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	if notify {
		c.notify(snapshot)
	}
	return snapshot, nil
}

// commitOperationsLocked preflights a prospective Snapshot, applies it to the
// runtime, and only then commits the same operations at the same revision. The
// caller must hold applyMu so subscription delivery cannot interleave. If the
// commit fails, the previous runtime config is restored before applyMu is
// released.
func (c *Controller) commitOperationsLocked(ctx context.Context, revision uint64, operations []Operation) (Snapshot, bool, error) {
	return c.commitOperationsValidatedLocked(ctx, revision, operations, nil)
}

func (c *Controller) commitCatalogSelectionLocked(ctx context.Context, selection CatalogSelection, operations []Operation) (Snapshot, bool, error) {
	return c.commitOperationsValidatedLocked(ctx, selection.Revision, operations, func(prospective Snapshot) error {
		if prospective.ActiveModelID == selection.ID {
			return nil
		}
		if override := strings.TrimSpace(os.Getenv("PAW_MODEL")); override != "" && prospective.ActiveModelID == override {
			return fmt.Errorf("cannot activate model %q while PAW_MODEL=%q overrides activeModel; remove or unset PAW_MODEL and retry", selection.ID, override)
		}
		if override := prospective.Workspace.ActiveModel; override != "" && prospective.ActiveModelID == override {
			return fmt.Errorf("cannot activate model %q while workspace activeModel %q in %s overrides the global selection; remove or change the workspace activeModel and retry", selection.ID, override, c.manager.Paths().WorkspaceConfig)
		}
		return fmt.Errorf("cannot activate model %q because the prospective active model is %q; remove the activeModel override and retry", selection.ID, prospective.ActiveModelID)
	})
}

func (c *Controller) commitOperationsValidatedLocked(ctx context.Context, revision uint64, operations []Operation, validate func(Snapshot) error) (Snapshot, bool, error) {
	prospective, err := c.manager.PreviewUpdate(ctx, revision, operations)
	if err != nil {
		return Snapshot{}, false, err
	}
	if validate != nil {
		if err := validate(prospective); err != nil {
			return Snapshot{}, false, err
		}
	}
	if c.runtime == nil || !prospective.Ready {
		committed, err := c.manager.Update(ctx, revision, operations)
		return committed, false, err
	}
	previous := c.runtime.CurrentModelConfig()
	if err := c.runtime.ApplyModelConfig(prospective.Active); err != nil {
		return Snapshot{}, false, c.restoreRuntime(previous, err)
	}
	committed, err := c.manager.commitPreview(ctx, revision, operations, prospective)
	if err != nil {
		return Snapshot{}, false, c.restoreRuntime(previous, err)
	}
	c.appliedRevision = committed.Revision
	return committed, true, nil
}

func (c *Controller) restoreRuntime(previous model.Config, cause error) error {
	if c.runtime == nil {
		return cause
	}
	if err := c.runtime.ApplyModelConfig(previous); err != nil {
		return fmt.Errorf("%w (restoring previous runtime config also failed: %v)", cause, err)
	}
	return cause
}
func (c *Controller) CredentialStore() CredentialStore {
	if c == nil || c.manager == nil {
		return nil
	}
	return c.manager.credentials
}

func (c *Controller) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
	})
	if c.manager != nil {
		return c.manager.Close()
	}
	return nil
}
