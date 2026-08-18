package bubble

import (
	"context"
	"strings"
	"testing"

	"paw/internal/settings"
	"paw/internal/theme"
)

func newThemedTestModel(t *testing.T, id theme.ThemeID) appModel {
	t.Helper()
	controller := &fakeSettingsController{current: settings.DefaultConfig()}
	controller.current.UI.Theme = id
	return newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, controller, nil, nil, newTerminalCursorAnchor())
}

func TestNewModelUsesConfiguredTheme(t *testing.T) {
	model := newThemedTestModel(t, theme.Dracula)
	if model.theme.ID != theme.Dracula {
		t.Fatalf("theme = %q, want %q", model.theme.ID, theme.Dracula)
	}
}

func TestApplyThemeRebuildsRenderCacheAndRefreshesViewport(t *testing.T) {
	model := newThemedTestModel(t, theme.Default)
	model.addEntry(transcriptEntry{kind: entryAssistant, body: "hello"})
	_ = model.renderTranscriptContent()
	if len(model.transcriptRenderCache) == 0 {
		t.Fatal("expected populated cache")
	}
	if err := model.applyTheme(theme.TokyoNight); err != nil {
		t.Fatal(err)
	}
	if model.theme.ID != theme.TokyoNight {
		t.Fatalf("theme = %q", model.theme.ID)
	}
	if len(model.transcriptRenderCache) != len(model.transcript) {
		t.Fatalf("cache length = %d, want rebuilt length %d", len(model.transcriptRenderCache), len(model.transcript))
	}
	if model.transcriptInvalidation.dirty {
		t.Fatalf("theme refresh left transcript dirty: %#v", model.transcriptInvalidation)
	}
	if !strings.Contains(model.viewport.View(), "hello") {
		t.Fatal("viewport was not refreshed")
	}
}

func TestTwoModelsCanUseDifferentThemes(t *testing.T) {
	dark := newThemedTestModel(t, theme.TokyoNight)
	light := newThemedTestModel(t, theme.TokyoNightLight)
	if dark.styles.Frame.GetBackground() == light.styles.Frame.GetBackground() {
		t.Fatal("models share theme styles")
	}
}
