package bubble

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/settings"
	"paw/internal/theme"
)

func TestThemeCommandIsRegistered(t *testing.T) {
	command, ok := NewCommandRegistry().Lookup("/theme")
	if !ok {
		t.Fatal("/theme is not registered")
	}
	if command.AllowWhileRunning {
		t.Fatal("/theme must not open while a turn is running")
	}
}

func TestOpenThemePickerSelectsCurrentTheme(t *testing.T) {
	model := newThemedTestModel(t, theme.Dracula)
	model.openThemePicker()
	if model.themePicker == nil {
		t.Fatal("theme picker not opened")
	}
	if model.themePicker.original != theme.Dracula || model.themePicker.selected != theme.Dracula {
		t.Fatalf("picker = %#v", model.themePicker)
	}
}

func newPickerTestModel(controller *fakeSettingsController) appModel {
	return newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, controller, nil, nil, newTerminalCursorAnchor())
}

func TestThemePickerNavigationPreviewsWithoutSaving(t *testing.T) {
	controller := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newPickerTestModel(controller)
	model.openThemePicker()
	next, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(appModel)
	if got.theme.ID != theme.TokyoNight {
		t.Fatalf("preview theme = %q, want %q", got.theme.ID, theme.TokyoNight)
	}
	if len(controller.saved) != 0 {
		t.Fatalf("preview saved %d configs", len(controller.saved))
	}
}

func TestThemePickerEscapeRestoresOriginal(t *testing.T) {
	controller := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newPickerTestModel(controller)
	model.openThemePicker()
	next, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.theme.ID != theme.Default || model.themePicker != nil {
		t.Fatalf("theme/picker = %q/%#v", model.theme.ID, model.themePicker)
	}
	if len(controller.saved) != 0 {
		t.Fatalf("cancel saved configs = %#v", controller.saved)
	}
}

func TestThemePickerEnterSavesAndCloses(t *testing.T) {
	controller := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newPickerTestModel(controller)
	model.openThemePicker()
	next, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.themePicker != nil || len(controller.saved) != 1 {
		t.Fatalf("picker/saved = %#v/%#v", model.themePicker, controller.saved)
	}
	if controller.saved[0].UI.Theme != theme.TokyoNight {
		t.Fatalf("saved theme = %q", controller.saved[0].UI.Theme)
	}
}

func TestThemePickerSaveFailureKeepsPreviewOpen(t *testing.T) {
	controller := &fakeSettingsController{current: settings.DefaultConfig(), err: errors.New("permission denied")}
	model := newPickerTestModel(controller)
	model.openThemePicker()
	next, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.themePicker == nil || model.theme.ID != theme.TokyoNight || model.themePicker.saveError == "" {
		t.Fatalf("failed save state = theme:%q picker:%#v", model.theme.ID, model.themePicker)
	}
	next, _ = model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.theme.ID != theme.Default || model.themePicker != nil {
		t.Fatalf("escape after failure = theme:%q picker:%#v", model.theme.ID, model.themePicker)
	}
}

func TestThemePickerHomeAndEnd(t *testing.T) {
	controller := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newPickerTestModel(controller)
	model.openThemePicker()
	next, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyEnd})
	model = next.(appModel)
	if model.theme.ID != theme.GruvboxDark {
		t.Fatalf("end theme = %q", model.theme.ID)
	}
	next, _ = model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyHome})
	model = next.(appModel)
	if model.theme.ID != theme.Default {
		t.Fatalf("home theme = %q", model.theme.ID)
	}
}
