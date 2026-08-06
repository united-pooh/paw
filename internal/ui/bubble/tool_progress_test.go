package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
)

// TestRunningToolElapsedAdvancesAcrossFrames 回归：running tool 的 elapsed
// 计时必须随帧驱动自动更新，不能被严格 revision cache 短路冻结。
// 历史 bug：refreshRunningToolProgress 每秒调用 refreshViewport，但
// transcriptRenderSignature 不含时间，签名不变 → 命中缓存 → 计时冻结；
// 只有鼠标 hover（touch entry）才会偶然刷新。修复后 elapsed 变化时
// touch running entry 使缓存失效。
func TestRunningToolElapsedAdvancesAcrossFrames(t *testing.T) {
	vp := viewport.New(100, 20)
	vp.Width = 100
	m := appModel{
		viewport: vp,
		transcript: []transcriptEntry{{
			kind: entryTool, title: "tool", toolName: "bash", toolStatus: "running",
			body: "bash running", toolStartedAt: time.Now().Add(-5 * time.Second),
		}},
	}
	base := time.Now()

	// 第一帧：渲染 5.00s
	m.cursorFrameAt = base
	first := m.renderTranscriptContent()
	if !strings.Contains(first, "5.00s") {
		t.Fatalf("first frame elapsed = %q, want 5.00s", first)
	}

	// 第二帧：推进 3 秒，模拟 cursorFrameMsg 分支调用 refreshRunningToolProgress
	m.cursorFrameAt = base.Add(3 * time.Second)
	m.refreshRunningToolProgress(base.Add(3 * time.Second))
	second := m.renderTranscriptContent()
	if !strings.Contains(second, "8.00s") {
		t.Fatalf("second frame elapsed = %q, want 8.00s (cache must not freeze progress)", second)
	}

	// 同一秒内重复帧：不应额外 touch（版本稳定，无抖动刷新）
	versionBefore := m.transcript[0].version
	m.cursorFrameAt = base.Add(3*time.Second + 500*time.Millisecond)
	m.refreshRunningToolProgress(base.Add(3*time.Second + 500*time.Millisecond))
	if m.transcript[0].version != versionBefore {
		t.Fatalf("same-second frame touched entry: version %d -> %d", versionBefore, m.transcript[0].version)
	}
}

// TestSubagentWaitProgressAdvances 回归：SubagentWait 状态行计时同样不被缓存冻结。
func TestSubagentWaitProgressAdvances(t *testing.T) {
	vp := viewport.New(100, 20)
	vp.Width = 100
	started := time.Now().Add(-2 * time.Second)
	m := appModel{
		viewport: vp,
		transcript: []transcriptEntry{{
			kind: entrySystem, title: "", body: "子智能体 正在运行 2s",
			subagentWaitRunning: true, subagentWaitNames: []string{"测试"},
			toolName: "SubagentWait", toolStatus: "running",
			createdAt: started, toolStartedAt: started,
		}},
	}
	base := time.Now()
	m.cursorFrameAt = base
	first := m.renderTranscriptContent()
	if !strings.Contains(first, "2s") {
		t.Fatalf("first frame = %q, want 2s", first)
	}
	m.cursorFrameAt = base.Add(4 * time.Second)
	m.refreshRunningToolProgress(base.Add(4 * time.Second))
	second := m.renderTranscriptContent()
	if !strings.Contains(second, "6s") {
		t.Fatalf("second frame = %q, want 6s", second)
	}
}
