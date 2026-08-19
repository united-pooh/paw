package bubble

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestConfigCenterRendersFullscreenPage 验证配置中心走全屏覆盖：页面只留小
// gutter、搜索框接近全宽、默认激活 General，footer 固定在底部。
func TestConfigCenterRendersFullscreenPage(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, &fakeSettingsController{}, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.ready = true
	model.width = 101
	model.height = 30
	model.relayout()
	model.openConfigCenter()

	view := model.View()
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) != 30 {
		t.Fatalf("rendered height = %d, want 30 (full screen)", len(lines))
	}
	const width = 101
	for i, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("line %d width = %d, want %d (full screen)", i+1, got, width)
		}
	}
	// 101 列终端只留 2 列 gutter，页面内容宽 97 列。
	const leftMargin = 2
	const contentWidth = 97
	prefix := strings.Repeat(" ", leftMargin)
	var foundTabs, foundSearch bool
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if strings.Contains(trimmed, "服务商") && strings.Contains(trimmed, "诊断") {
			if !strings.HasPrefix(line, prefix+"设置") {
				t.Fatalf("tabs not aligned to page gutter %d: %q", leftMargin, line)
			}
			if strings.Contains(line, "当前模型") {
				t.Fatalf("duplicate current-model tab is still rendered: %q", line)
			}
			foundTabs = true
		}
		if strings.HasPrefix(line, prefix+"╭") {
			if got := lipgloss.Width(trimmed); got != leftMargin+contentWidth {
				t.Fatalf("search border width = %d, want %d: %q", got, leftMargin+contentWidth, line)
			}
			foundSearch = true
		}
	}
	if !foundTabs || !foundSearch {
		t.Fatalf("tabs/search not found in render:\n%s", plain)
	}
	if !strings.Contains(plain, "设置项") || !strings.Contains(plain, "压缩模式") {
		t.Fatalf("General page content missing:\n%s", plain)
	}
	if strings.Contains(plain, "Paw 配置") || strings.Contains(plain, "通用设置") {
		t.Fatalf("detached Home page was rendered instead of General:\n%s", plain)
	}
	if !strings.Contains(lines[len(lines)-2], "/ 搜索") {
		t.Fatalf("footer is not fixed above bottom border: %q", lines[len(lines)-2])
	}
}

// TestConfigCenterFullscreenHidesTerminalCursor 验证全屏覆盖层打开时真实终端
// 光标被隐藏（编辑页光标由渲染层的反色块表达）；恢复为可见会让它停在帧
// 末尾的左下角，形成"两个光标"。
func TestConfigCenterFullscreenHidesTerminalCursor(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session", controller, &fakeSettingsController{}, nil, nil, anchor)
	model.configCenterController = controller
	model.ready = true
	model.width = 101
	model.height = 30
	model.relayout()
	model.openConfigCenter()

	_ = model.View()
	position, ok := anchor.consume()
	if !ok {
		t.Fatal("View did not publish a cursor anchor update")
	}
	if !position.hidden || position.active {
		t.Fatalf("fullscreen modal cursor position = %#v, want hidden and inactive", position)
	}
}

// TestConfigCenterSettingWizardRoutesFullscreen 验证 /setting 在无配置中心控制
// 器时退回的向导也走全屏覆盖并使用相同小 gutter。
func TestConfigCenterSettingWizardRoutesFullscreen(t *testing.T) {
	model := newModel(context.Background(), &fakeRunner{}, "session", &fakeModelConfigController{}, &fakeSettingsController{}, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 81
	model.height = 20
	model.relayout()
	if handled, _ := model.handleCommand("/setting"); !handled || model.settingWizard == nil {
		t.Fatalf("/setting did not open wizard: handled=%v wizard=%v", handled, model.settingWizard)
	}
	view := model.View()
	plain := ansi.Strip(view)
	lines := strings.Split(plain, "\n")
	if len(lines) != 20 {
		t.Fatalf("wizard rendered height = %d, want 20 (full screen)", len(lines))
	}
	leftMargin := 2
	prefix := strings.Repeat(" ", leftMargin)
	var found bool
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if !strings.Contains(trimmed, "Task context") {
			continue
		}
		if !strings.HasPrefix(line, prefix+"Task context") {
			t.Fatalf("wizard title not left-aligned to content column left edge (col %d): %q", leftMargin, line)
		}
		found = true
	}
	if !found {
		t.Fatalf("wizard title not found in render:\n%s", plain)
	}
}
