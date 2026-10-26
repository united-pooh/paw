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

func TestDisplayToolTargetHumanizesSearchAndEncodedURLs(t *testing.T) {
	rawScholarURL := "https://scholar.google.com/scholar?q=%22Query-Conditioned+Recoverable+Foveated+Patching%22"
	scholarInput := json.RawMessage(`{"url":"` + rawScholarURL + `"}`)
	scholar := buildToolDisplay("WebFetch", scholarInput, "")
	wantScholar := `scholar.google.com · "Query-Conditioned Recoverable Foveated Patching"`
	if scholar.target != wantScholar {
		t.Fatalf("scholar.target = %q, want %q", scholar.target, wantScholar)
	}
	if gotURL := extractRawToolURL("WebFetch", scholarInput); gotURL != rawScholarURL {
		t.Fatalf("extractRawToolURL = %q, want %q", gotURL, rawScholarURL)
	}

	arxiv := buildToolDisplay("WebFetch", json.RawMessage(`{"url":"https://arxiv.org/search/?searchtype=all&query=%22Query-Conditioned+Recoverable%22"}`), "")
	wantArxiv := `arxiv.org · "Query-Conditioned Recoverable"`
	if arxiv.target != wantArxiv {
		t.Fatalf("arxiv.target = %q, want %q", arxiv.target, wantArxiv)
	}

	encodedPath := buildToolDisplay("WebFetch", json.RawMessage(`{"url":"https://en.wikipedia.org/wiki/Foveated_%28rendering%29"}`), "")
	wantDecoded := "https://en.wikipedia.org/wiki/Foveated_(rendering)"
	if encodedPath.target != wantDecoded {
		t.Fatalf("encodedPath.target = %q, want %q", encodedPath.target, wantDecoded)
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
