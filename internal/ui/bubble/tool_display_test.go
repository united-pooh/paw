package bubble

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestBuildToolDisplayUsesChineseMCPAction(t *testing.T) {
	display := buildToolDisplay("codegraph__read_url", json.RawMessage(`{"url":"https://example.com/docs"}`), "")

	if display.name != "CodeGraph: 读取页面" {
		t.Fatalf("display.name = %q, want CodeGraph: 读取页面", display.name)
	}
	if display.target != "https://example.com/docs" {
		t.Fatalf("display.target = %q, want URL", display.target)
	}
}

func TestBuildToolDisplayUsesWorkspaceRelativeNativePath(t *testing.T) {
	root := t.TempDir()
	display := buildToolDisplay("Edit", json.RawMessage(`{"file_path":"internal/ui/bubble/transcript.go"}`), root)

	if display.name != "Edit:" {
		t.Fatalf("display.name = %q, want Edit:", display.name)
	}
	want := "internal/ui/bubble/transcript.go"
	if display.target != want {
		t.Fatalf("display.target = %q, want %q", display.target, want)
	}
}

func TestDisplayToolNameHidesUnknownMCPNamespace(t *testing.T) {
	if got := displayToolName("codegraph__explore"); got != "CodeGraph: explore" {
		t.Fatalf("displayToolName() = %q, want CodeGraph: explore", got)
	}
}

func TestDisplayToolTargetKeepsOutsideAbsolutePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.go")
	display := buildToolDisplay("Read", json.RawMessage(`{"file_path":"`+target+`"}`), root)

	if display.target != filepath.ToSlash(target) {
		t.Fatalf("display.target = %q, want %q", display.target, filepath.ToSlash(target))
	}
}

func TestToolStatusLabelsAreChinese(t *testing.T) {
	tests := map[string]string{
		"ok":      "完成",
		"running": "运行中",
		"error":   "失败",
	}
	for status, want := range tests {
		if got := statusTextLabel(status); got != want {
			t.Fatalf("statusTextLabel(%q) = %q, want %q", status, got, want)
		}
	}
}

// 状态词是彩色文字（不再是实底药丸）：前景色来自 status 语义角色。
func TestToolStatusTextUsesSemanticForegrounds(t *testing.T) {
	newRGBTestModel(t)
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	tests := map[string]string{
		"ok":      colorManager.Hex(colorStatusSuccess),
		"running": colorManager.Hex(colorStatusRunning),
		"error":   colorManager.Hex(colorStatusError),
	}
	for status, wantForeground := range tests {
		style := statusTextStyle(status)
		if got := style.GetForeground(); got != lipgloss.Color(wantForeground) {
			t.Fatalf("%s foreground = %q, want %q", status, got, wantForeground)
		}
		rendered := renderStatusText(status, "1.2s")
		if !strings.Contains(rendered, statusTextLabel(status)) || !strings.Contains(rendered, "1.2s") {
			t.Fatalf("status text = %q, want label and duration", rendered)
		}
		if !strings.Contains(rendered, "\x1b[") {
			t.Fatalf("status text = %q, want color escape under TrueColor", rendered)
		}
	}
}

func TestToolStatusTextKeepsTextWithoutColorProfile(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	rendered := renderStatusText("error", "")
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("status text = %q, want no ANSI under ASCII profile", rendered)
	}
	if !strings.Contains(rendered, "失败") {
		t.Fatalf("status text = %q, want status text", rendered)
	}
}
