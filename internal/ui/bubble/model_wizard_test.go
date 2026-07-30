package bubble

import (
	"context"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	modelcfg "paw/internal/model"
)

func advanceModelWizard(t *testing.T, model appModel, msg tea.KeyMsg) appModel {
	t.Helper()
	next, _ := model.Update(msg)
	updated, ok := next.(appModel)
	if !ok {
		t.Fatalf("wizard update returned %T, want appModel", next)
	}
	return updated
}

func TestModelWizardPreservesProfileExtraBodiesWhenSwitchingProviderAndModel(t *testing.T) {
	primaryExtra := modelcfg.RequestBody{
		"profile_default": "primary",
		"nested":          map[string]any{"shared": true},
	}
	primaryModelExtra := map[string]modelcfg.RequestBody{
		"primary-fast": {"latency": "low"},
	}
	backupExtra := modelcfg.RequestBody{
		"profile_default": "backup",
		"nested":          map[string]any{"shared": false, "backup": true},
	}
	backupModelExtra := map[string]modelcfg.RequestBody{
		"backup-fast": {"reasoning": "high"},
		"backup-safe": {"temperature": 0.2},
	}
	controller := &fakeModelConfigController{current: modelcfg.Config{
		ProfileID: "primary",
		Provider:  "primary",
		Model:     "primary-fast",
		Models:    []string{"primary-fast"},
		Profiles: []modelcfg.Profile{
			{ID: "primary", Name: "Primary", Provider: "primary", Model: "primary-fast", Models: []string{"primary-fast"}, ExtraBody: primaryExtra, ModelExtraBody: primaryModelExtra},
			{ID: "backup", Name: "Backup", Provider: "backup", Model: "backup-fast", Models: []string{"backup-fast", "backup-safe"}, ExtraBody: backupExtra, ModelExtraBody: backupModelExtra},
		},
	}}
	app := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.modelWizard = newModelWizard(controller.current)
	app.modelWizard.selectedIndex = 1

	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})
	if app.modelWizard.step != modelWizardModel || !reflect.DeepEqual(app.modelWizard.modelOptions, []string{"backup-fast", "backup-safe"}) {
		t.Fatalf("provider selection = %#v", app.modelWizard)
	}
	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyDown})
	if got := app.modelWizard.selectedModelName(); got != "backup-safe" {
		t.Fatalf("selected model = %q, want backup-safe", got)
	}
	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})
	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})

	if app.modelWizard != nil || len(controller.saved) != 1 || len(controller.applied) != 1 {
		t.Fatalf("wizard/saved/applied = %#v/%d/%d", app.modelWizard, len(controller.saved), len(controller.applied))
	}
	for name, cfg := range map[string]modelcfg.Config{"saved": controller.saved[0], "applied": controller.applied[0]} {
		if cfg.ProfileID != "backup" || cfg.Model != "backup-safe" {
			t.Fatalf("%s selection = %#v", name, cfg)
		}
		if !reflect.DeepEqual(cfg.ExtraBody, backupExtra) || !reflect.DeepEqual(cfg.ModelExtraBody, backupModelExtra) {
			t.Fatalf("%s extra bodies = %#v/%#v", name, cfg.ExtraBody, cfg.ModelExtraBody)
		}
		if len(cfg.Profiles) != 2 || !reflect.DeepEqual(cfg.Profiles[0].ExtraBody, primaryExtra) || !reflect.DeepEqual(cfg.Profiles[1].ModelExtraBody, backupModelExtra) {
			t.Fatalf("%s profile extra bodies were not retained = %#v", name, cfg.Profiles)
		}
	}
}

func TestModelWizardAppliesCurrentModelMissingFromModels(t *testing.T) {
	orphanExtra := modelcfg.RequestBody{"profile_default": "orphan"}
	orphanModelExtra := map[string]modelcfg.RequestBody{
		"orphan-model": {"mode": "compatibility"},
	}
	controller := &fakeModelConfigController{current: modelcfg.Config{
		ProfileID: "orphan",
		Provider:  "gateway",
		Model:     "orphan-model",
		Models:    []string{"listed-model"},
		Profiles: []modelcfg.Profile{{
			ID: "orphan", Provider: "gateway", Model: "orphan-model", Models: []string{"listed-model"},
			ExtraBody: orphanExtra, ModelExtraBody: orphanModelExtra,
		}},
	}}
	app := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	app.modelWizard = newModelWizard(controller.current)

	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})
	if !reflect.DeepEqual(app.modelWizard.modelOptions, []string{"listed-model", "orphan-model"}) || app.modelWizard.selectedModelName() != "orphan-model" {
		t.Fatalf("model options/selection = %#v", app.modelWizard)
	}
	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})
	app = advanceModelWizard(t, app, tea.KeyMsg{Type: tea.KeyEnter})

	if app.modelWizard != nil || len(controller.saved) != 1 || len(controller.applied) != 1 {
		t.Fatalf("wizard/saved/applied = %#v/%d/%d", app.modelWizard, len(controller.saved), len(controller.applied))
	}
	if got := controller.applied[0]; got.Model != "orphan-model" || !reflect.DeepEqual(got.ExtraBody, orphanExtra) || !reflect.DeepEqual(got.ModelExtraBody, orphanModelExtra) {
		t.Fatalf("applied config = %#v", got)
	}
}
