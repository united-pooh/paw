package config

import (
	"context"
	"fmt"
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
	wantedProvider := strings.TrimSpace(firstNonEmpty(value.ProfileID, value.Provider))
	wantedModel := strings.TrimSpace(value.Model)
	match := ""
	for id, item := range snapshot.EffectiveModels {
		if item.Model.Provider == wantedProvider && item.Model.Name == wantedModel {
			if match != "" {
				return fmt.Errorf("model %q is ambiguous under provider %q", wantedModel, wantedProvider)
			}
			match = id
		}
	}
	if match == "" {
		return fmt.Errorf("model %q under provider %q is not in the effective catalog", wantedModel, wantedProvider)
	}
	updated, err := c.activateCatalogModel(context.Background(), match)
	if err != nil {
		return err
	}
	return c.applySnapshot(updated)
}

func (c *Controller) activateCatalogModel(ctx context.Context, id string) (Snapshot, error) {
	id = strings.TrimSpace(id)
	snapshot := c.manager.Snapshot()
	item, ok := snapshot.EffectiveModels[id]
	if !ok {
		return Snapshot{}, fmt.Errorf("model %q is not in the effective catalog", id)
	}
	operations := make([]Operation, 0, 2)
	if item.Source == ModelSourceDiscovered {
		operations = append(operations, UpsertModel(item.ID, item.Model))
	}
	operations = append(operations, SetActiveModel(item.ID))
	return c.manager.Update(ctx, snapshot.Revision, operations)
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
	updated, err := c.activateCatalogModel(context.Background(), id)
	if err != nil {
		return err
	}
	return c.applySnapshot(updated)
}
func (c *Controller) UpdateConfig(ctx context.Context, revision uint64, operations []Operation) (Snapshot, error) {
	if c == nil || c.manager == nil {
		return Snapshot{}, fmt.Errorf("configuration manager is unavailable")
	}
	snapshot, err := c.manager.Update(ctx, revision, operations)
	if err != nil {
		return Snapshot{}, err
	}
	if err := c.applySnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
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
