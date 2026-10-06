package bubble

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCaptureModeWorkflowVisualFixtures(t *testing.T) {
	dir := os.Getenv("PAW_MODE_WORKFLOW_VISUAL_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("set PAW_MODE_WORKFLOW_VISUAL_DIR to capture mode workflow fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	for _, fixture := range []struct {
		name          string
		width, height int
		goal          bool
	}{
		{"plan-ready-wide", 120, 36, false},
		{"plan-ready-narrow", 80, 24, false},
		{"goal-paused-wide", 120, 36, true},
		{"goal-paused-narrow", 80, 24, true},
	} {
		model := newActivityVisualModel(fixture.width, fixture.height)
		model.taskController = nil
		model.closeActivity()
		model.replaceTranscript(nil)
		var wants []string
		if fixture.goal {
			model.goalMode, model.goalWorking = true, true
			model.goalController = &workflowGoalController{fakeGoalController: fakeGoalController{status: strings.Join([]string{
				"id: goal-accuracy", "session: visual", "objective: 将准确率提高到 90%", "status: paused",
				"continuations: 3/20", "no-progress: 0", "验收指标：accuracy gte 0.9 ratio",
				"最近测量：0.82 ratio（来源 measure-1）", "测量命令：python3 evaluate.py", "数值路径：/accuracy",
			}, "\n")}}
			model.input.SetValue("补充：保持相同验证集")
			next, _ := model.Update(goalStoppedMsg{reason: "goal.paused"})
			model = next.(appModel)
			wants = []string{"目标已暂停", "将准确率提高到 90%", "accuracy gte 0.9", "0.82 ratio", "measure-1", "补充：保持相同验证集"}
		} else {
			model.planMode, model.planWorking = true, true
			model.replaceTranscript([]transcriptEntry{
				{kind: entryUser, title: "you (plan)", body: "先讨论需求，整理一个可供审核的迁移计划。"},
				{kind: entryAssistant, title: "assistant", body: "计划包含兼容策略、逐步迁移和回归验证。请补充修改，或明确确认执行。"},
			})
			model.input.SetValue("补充：保留旧接口兼容性")
			next, _ := model.Update(planFinalizedMsg{path: "docs/local/plans/migration.md"})
			model = next.(appModel)
			wants = []string{"计划已就绪", "/plan execute", "补充：保留旧接口兼容性"}
		}
		model.input.Focus()
		model.relayout()
		model.refreshViewport()
		model.viewport.GotoBottom()
		view := model.View()
		plain := ansi.Strip(view)
		for _, want := range wants {
			if !strings.Contains(plain, want) {
				t.Fatalf("%s missing %q:\n%s", fixture.name, want, plain)
			}
		}
		if model.goalWorking || model.planWorking || model.isModelWorkRunning() || strings.Contains(plain, "已结束") {
			t.Fatalf("%s shows waiting workflow as running or ended", fixture.name)
		}
		tokenMonitorWriteVisual(t, dir, fixture.name, fmt.Sprintf("Paw appModel.View · Goal/Plan workflow · %d × %d cells", fixture.width, fixture.height), view)
	}
}
