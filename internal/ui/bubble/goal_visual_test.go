package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestGoalModeKeepsInputBodyChatColored(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.height = 24
	model.ready = true
	model.relayout()
	model.goalMode = true
	model.input.SetValue("build the release")
	model.input.CursorEnd()

	rendered := model.renderInputBox()
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "build the release") {
		t.Fatalf("goal input = %q, want input text", plain)
	}
	if strings.Contains(plain, "goal") {
		t.Fatalf("goal input leaked mode label into body: %q", plain)
	}
	if !strings.Contains(rendered, bodyStyle.Render("build the release")) {
		t.Fatalf("goal input body is missing ordinary body style: %q", rendered)
	}
}

func TestGoalModeDoesNotRenderGoalPlaceholderOrStatusWordsInInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.goalMode = true
	model.input.SetValue("")

	input := ansi.Strip(model.renderInputBox())
	for _, unwanted := range []string{"goal", "ready", "working", "generating"} {
		if strings.Contains(strings.ToLower(input), unwanted) {
			t.Fatalf("goal input = %q, should not contain %q", input, unwanted)
		}
	}
}

func TestGoalModeKeepsSingleInputFrameAndModeIndicator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.goalMode = true
	model.width = 80
	model.height = 24
	model.ready = true
	model.relayout()

	inputBox := model.renderInputBox()
	plainInput := ansi.Strip(inputBox)
	if strings.Contains(plainInput, "┌") || strings.Contains(plainInput, "┐") || strings.Contains(plainInput, "└") || strings.Contains(plainInput, "┘") {
		t.Fatalf("goal input = %q, contains a nested rounded input frame", plainInput)
	}

	bottom := ansi.Strip(model.renderBottomDockLine(80))
	if !strings.Contains(bottom, "goal") {
		t.Fatalf("goal bottom border = %q, want goal mode indicator", bottom)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(bottom, unwanted) {
			t.Fatalf("goal bottom border = %q, should not contain %q", bottom, unwanted)
		}
	}
}

func TestChatModeKeepsChatIndicatorWithoutStatusWord(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	bottom := ansi.Strip(model.renderBottomDockLine(80))
	if !strings.Contains(bottom, "chat") {
		t.Fatalf("chat bottom border = %q, want chat indicator", bottom)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(bottom, unwanted) {
			t.Fatalf("chat bottom border = %q, should not contain %q", bottom, unwanted)
		}
	}
}

func TestGoalModeWorkingStateStillHidesStatusWords(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.goalMode = true
	model.isGenerating = true

	status := strings.ToLower(ansi.Strip(model.renderDockStatusLine(80)))
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(status, unwanted) {
			t.Fatalf("goal working status = %q, should not contain %q", status, unwanted)
		}
	}
}

func TestGoalModeStyleDoesNotSetInputForeground(t *testing.T) {
	style := inputDockGoalStyle
	if _, hasForeground := style.GetForeground().(lipgloss.NoColor); !hasForeground {
		t.Fatalf("goal dock style sets a foreground %v; goal should only change border", style.GetForeground())
	}
}
