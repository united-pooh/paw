package bubble

import (
	"strings"
	"testing"
)

func TestRenderWorkStatusLineIdleEmpty(t *testing.T) {
	if got := renderWorkStatusLine(workStatusIdle, "", "", ""); got != "" {
		t.Errorf("空闲状态行应整行不渲染, got %q", got)
	}
}

func TestRenderWorkStatusLineThinking(t *testing.T) {
	got := renderWorkStatusLine(workStatusThinking, "", "2m 28s", "8.0k")
	for _, want := range []string{glyphStatus, "思考中", "ctrl+c 中断", "2m 28s", "↓ 8.0k tok"} {
		if !strings.Contains(got, want) {
			t.Errorf("思考中状态行缺 %q: %q", want, got)
		}
	}
}

func TestRenderWorkStatusLineTool(t *testing.T) {
	got := renderWorkStatusLine(workStatusTool, "Bash", "0m 12s", "1.2k")
	for _, want := range []string{glyphStatus, "执行", "中", "Shell", "ctrl+c 中断", "0m 12s"} {
		if !strings.Contains(got, want) {
			t.Errorf("工具状态行缺 %q: %q", want, got)
		}
	}
}

func TestRenderWorkStatusLineDoneNoInterruptHint(t *testing.T) {
	got := renderWorkStatusLine(workStatusDone, "", "13.1s", "")
	if strings.Contains(got, "ctrl+c") {
		t.Errorf("完成态不应显示中断提示: %q", got)
	}
	if !strings.Contains(got, "完成") || !strings.Contains(got, "13.1s") {
		t.Errorf("完成态缺动作或耗时: %q", got)
	}
	if strings.Contains(got, "↓") {
		t.Errorf("tokRate 为空时不应渲染 ↓ 分段: %q", got)
	}
}

func TestRenderWorkStatusLinePermission(t *testing.T) {
	got := renderWorkStatusLine(workStatusPermission, "", "5s", "")
	if !strings.Contains(got, "等待权限") {
		t.Errorf("权限状态行缺动作: %q", got)
	}
}
