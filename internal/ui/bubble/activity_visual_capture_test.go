package bubble

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/task"
)

func TestCaptureActivityDockVisualFixtures(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	dir := os.Getenv("PAW_ACTIVITY_VISUAL_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("set PAW_ACTIVITY_VISUAL_DIR to capture Activity visual fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	capture := func(name, title string, model appModel) {
		t.Helper()
		model.relayout()
		model.refreshViewport()
		view := model.View()
		if strings.TrimSpace(ansi.Strip(view)) == "" {
			t.Fatalf("%s view is empty", name)
		}
		page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#0d0f13;color:#d8dde7;font:15px/1.35 ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
main{display:inline-block;min-width:100vw;box-sizing:border-box;padding:28px 32px 36px}h1{font:600 20px/1.3 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#f0f3f8;margin:0 0 16px}
pre{display:inline-block;margin:0;padding:18px 20px;background:#11151b;border:1px solid #303846;border-radius:8px;box-shadow:0 16px 40px #0008;white-space:pre;color:#d8dde7}
</style></head><body><main><h1>` + html.EscapeString(title) + `</h1><pre>` + activityVisualANSIToHTML(view) + `</pre></main></body></html>`
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	base := newActivityVisualModel(120, 30)
	base.openActivity(activityTabTasks)
	capture("activity-docked-default", "Activity docked · default 36%", base)

	resized := newActivityVisualModel(120, 30)
	resized.activity.widthColumns = 52
	resized.openActivity(activityTabTasks)
	capture("activity-docked-resized", "Activity docked · resized to 52 columns", resized)

	preview := newActivityVisualModel(120, 30)
	preview.openActivity(activityTabTasks)
	preview.taskPreview = &taskTranscriptPreview{
		task:             preview.activity.tasks[0],
		parentSessionID:  preview.sessionID,
		parentTranscript: copyTranscriptEntries(preview.transcript),
		entries: []transcriptEntry{
			{kind: entryUser, title: "you", body: "Inspect the current Activity overlay"},
			{kind: entryAssistant, title: "assistant", body: "The old panel is composed with placeOpaqueOverlay."},
		},
	}
	preview.replaceTranscript(renderTaskPreviewTranscript(preview.taskPreview, preview.cursorFrameAt))
	preview.activity.focus = activityFocusWorkspace
	preview.input.Focus()
	capture("activity-docked-preview", "Task transcript preview · input stays on main session", preview)

	closed := newActivityVisualModel(120, 30)
	capture("activity-docked-closed", "Activity closed · running task in top hairline", closed)

	narrow := newActivityVisualModel(80, 24)
	narrow.openActivity(activityTabTasks)
	capture("activity-fullscreen-narrow", "Activity fullscreen · 80 columns", narrow)

	overlay := newActivityVisualModel(120, 30)
	overlay.openActivity(activityTabTasks)
	overlay.activity.focus = activityFocusWorkspace
	overlay.input.Focus()
	overlay.completion = &completion{kind: completionKindCommand, items: []string{"/help", "/tasks", "/todo"}}
	capture("activity-docked-overlay-priority", "Completion remains inside workspace", overlay)
}

func newActivityVisualModel(width, height int) appModel {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = width
	model.height = height
	model.cursorFrameAt = time.Date(2026, 8, 30, 12, 48, 0, 0, time.Local)
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "layout-research", SessionID: "layout-research", ParentSessionID: model.sessionID, Name: "八潮瑠唯", Color: "#669988", Description: "检查 Activity 中 worker 身份、状态、优先级排序与选中任务描述布局。", Status: task.TaskRunning, StartedAt: time.Now().Add(-84 * time.Second), UsedTokens: 8200},
		{ID: "visual-review", SessionID: "visual-review", ParentSessionID: model.sessionID, Name: "高松灯", Color: "#CC0033", Description: "复核应援色与多行任务描述在窄侧栏中的可读性。", Status: task.TaskCompleted, UsedTokens: 3100},
		{ID: "implementation-plan", SessionID: "implementation-plan", ParentSessionID: model.sessionID, Name: "千早爱音", Color: "#FF99CC", Description: "整理实现计划并确认所有回归测试。", Status: task.TaskStopped},
	}}
	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "把 Ctrl+G 面板改成侧边框"},
		{kind: entryAssistant, title: "assistant", body: "Activity 将参与布局，不再覆盖 transcript。"},
	}
	model.input.SetValue("继续给主 session 输入")
	return model
}

type activityVisualANSIStyle struct {
	foreground string
	background string
	bold       bool
	italic     bool
	underline  bool
	reverse    bool
}

func activityVisualANSIToHTML(input string) string {
	var output strings.Builder
	style := activityVisualANSIStyle{}
	for len(input) > 0 {
		index := strings.Index(input, "\x1b[")
		if index < 0 {
			output.WriteString(activityVisualStyledHTML(input, style))
			break
		}
		output.WriteString(activityVisualStyledHTML(input[:index], style))
		input = input[index+2:]
		end := strings.IndexByte(input, 'm')
		if end < 0 {
			output.WriteString(html.EscapeString("\x1b[" + input))
			break
		}
		activityVisualApplySGR(&style, input[:end])
		input = input[end+1:]
	}
	return output.String()
}

func activityVisualStyledHTML(text string, style activityVisualANSIStyle) string {
	if text == "" {
		return ""
	}
	foreground, background := style.foreground, style.background
	if style.reverse {
		foreground, background = background, foreground
	}
	declarations := make([]string, 0, 5)
	if foreground != "" {
		declarations = append(declarations, "color:"+foreground)
	}
	if background != "" {
		declarations = append(declarations, "background:"+background)
	}
	if style.bold {
		declarations = append(declarations, "font-weight:700")
	}
	if style.italic {
		declarations = append(declarations, "font-style:italic")
	}
	if style.underline {
		declarations = append(declarations, "text-decoration:underline")
	}
	escaped := html.EscapeString(text)
	if len(declarations) == 0 {
		return escaped
	}
	return `<span style="` + strings.Join(declarations, ";") + `">` + escaped + `</span>`
}

func activityVisualApplySGR(style *activityVisualANSIStyle, raw string) {
	if style == nil {
		return
	}
	if raw == "" {
		*style = activityVisualANSIStyle{}
		return
	}
	parts := strings.Split(raw, ";")
	params := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		params = append(params, value)
	}
	for index := 0; index < len(params); index++ {
		switch params[index] {
		case 0:
			*style = activityVisualANSIStyle{}
		case 1:
			style.bold = true
		case 3:
			style.italic = true
		case 4:
			style.underline = true
		case 7:
			style.reverse = true
		case 22:
			style.bold = false
		case 23:
			style.italic = false
		case 24:
			style.underline = false
		case 27:
			style.reverse = false
		case 39:
			style.foreground = ""
		case 49:
			style.background = ""
		case 38, 48:
			if index+4 >= len(params) || params[index+1] != 2 {
				continue
			}
			color := fmt.Sprintf("#%02x%02x%02x", params[index+2], params[index+3], params[index+4])
			if params[index] == 38 {
				style.foreground = color
			} else {
				style.background = color
			}
			index += 4
		}
	}
}
