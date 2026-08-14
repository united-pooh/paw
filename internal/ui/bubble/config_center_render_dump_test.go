package bubble

import (
	"context"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"paw/internal/settings"
)

// TestRenderDumpGeneral 在 -v 模式打印宽屏 General 页，供人工检查列起点、
// 搜索框宽度、右侧留白和 footer 位置；普通测试运行不会输出日志。
func TestRenderDumpGeneral(t *testing.T) {
	controller, _ := newConfigCenterHarness(t)
	sc := &fakeSettingsController{current: settings.DefaultConfig()}
	runner := &fakeRunner{}
	model := newModel(context.Background(), runner, "session", controller, sc, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.ready = true
	// 宽屏尺寸用于人工核对真实终端截图里的水平节奏和右侧留白。
	model.width = 180
	model.height = 44
	model.relayout()
	model.openConfigCenter()
	model.configCenter.page = configCenterGeneral
	model.configCenter.selected = 0
	t.Logf("GENERAL TAB RENDER (width=180, ANSI stripped):\n%s", ansi.Strip(model.renderConfigCenterBox()))
}
