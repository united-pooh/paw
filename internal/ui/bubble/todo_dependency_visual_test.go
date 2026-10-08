package bubble

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/todo"
)

func TestCaptureTodoDependencyVisualFixtures(t *testing.T) {
	dir := os.Getenv("PAW_TODO_DEPENDENCY_VISUAL_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("set PAW_TODO_DEPENDENCY_VISUAL_DIR to capture Todo dependency fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	for _, fixture := range []struct {
		name                     string
		width, height, paneWidth int
		unblocked                bool
	}{
		{"blocked-wide", 140, 40, 48, false},
		{"blocked-narrow", 80, 24, 32, false},
		{"unblocked-wide", 140, 40, 48, true},
	} {
		model := newActivityVisualModel(fixture.width, fixture.height)
		model.taskController = nil
		model.activity.widthColumns = fixture.paneWidth
		snapshot := dependencyTodo(t, `["fix","review"]`)
		if fixture.unblocked {
			snapshot.Items[1].Status = todo.StatusCompleted
			snapshot.Items[2].Status = todo.StatusCompleted
		}
		snapshot.UpdatedAt = time.Unix(1791507600, 0)
		if !model.applyTodoSnapshot(snapshot, false) {
			t.Fatal("visual fixture snapshot was rejected")
		}
		model.replaceTranscript([]transcriptEntry{
			{kind: entryUser, title: "you", body: "交付前先修复测试并完成独立审查。"},
			{kind: entryAssistant, title: "assistant", body: "已保留原定交付事项。前置任务完成后，它会自动解除阻塞。"},
		})
		model.input.SetValue("继续当前任务")
		model.openActivity(activityTabTodo)
		model.input.Focus()
		model.relayout()
		model.refreshViewport()
		view := model.View()
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "TOKEN HISTORY") || !strings.Contains(plain, "继续当前任务") {
			t.Fatalf("%s lost persistent token history or input:\n%s", fixture.name, plain)
		}
		if fixture.unblocked {
			if strings.Contains(plain, "等待") || strings.Contains(plain, "⊘") || !strings.Contains(plain, "3/4") {
				t.Fatalf("%s did not display the unblocked item:\n%s", fixture.name, plain)
			}
		} else if !strings.Contains(plain, "等待 fix") || !strings.Contains(plain, "等待 review") || !strings.Contains(plain, "⊘") {
			t.Fatalf("%s lost blocked dependencies:\n%s", fixture.name, plain)
		}
		tokenMonitorWriteVisual(t, dir, fixture.name, fmt.Sprintf("Paw appModel.View · Todo dependencies · %d × %d cells", fixture.width, fixture.height), view)
	}
}
