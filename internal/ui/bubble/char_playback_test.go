package bubble

import (
	"context"
	"strings"
	"testing"
	"time"

	"paw/internal/settings"
)

// TestAssistantCharModeAcceleratesPlaybackWhenBacklogged 验证 char 模式播放
// 速率自适应：积压超过目标播放窗口（charPlaybackTargetFrames 帧）时按比例
// 加速，且单帧释放封顶 charPlaybackMaxPerFrame。这是"连续输入滚动后延迟
// 积累"的回归测试：旧实现恒为 1 字符/帧（30 字符/s），长回答下播放队列
// 随内容量无限积压。
func TestAssistantCharModeAcceleratesPlaybackWhenBacklogged(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		UI: settings.UIConfig{TranscriptOutputMode: settings.TranscriptOutputModeChar},
	}}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()
	frame := time.Unix(100, 0)

	// 中等积压（600 字符）：单帧释放必须 > 1（加速追平）。
	next, _ := model.Update(assistantDeltaMsg(strings.Repeat("a", 600)))
	model = next.(appModel)
	if !model.assistantStream.HasPendingCharacters() {
		t.Fatal("char-mode backlog was not buffered for playback")
	}
	next, _ = model.Update(cursorFrameMsg(frame.Add(cursorFrameInterval)))
	model = next.(appModel)
	if n := len([]rune(model.transcript[0].body)); n <= 1 {
		t.Fatalf("backlogged frame released %d chars, want accelerated release > 1", n)
	}

	// 深度积压（6000 字符）：单帧释放恰好封顶 charPlaybackMaxPerFrame。
	model.assistantStream.Reset()
	model.transcript = nil
	model.activeAssistant = -1
	next, _ = model.Update(assistantDeltaMsg(strings.Repeat("a", 6000)))
	model = next.(appModel)
	next, _ = model.Update(cursorFrameMsg(frame.Add(2 * cursorFrameInterval)))
	model = next.(appModel)
	if n := len([]rune(model.transcript[0].body)); n != charPlaybackMaxPerFrame {
		t.Fatalf("deep backlog frame released %d chars, want capped %d", n, charPlaybackMaxPerFrame)
	}
}

// TestAssistantCharModeKeepsTypewriterPaceWithoutBacklog 验证无积压时保持
// 逐字打字机节奏（每帧恰好 1 字符），打字机效果不受自适应逻辑影响。
func TestAssistantCharModeKeepsTypewriterPaceWithoutBacklog(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		UI: settings.UIConfig{TranscriptOutputMode: settings.TranscriptOutputModeChar},
	}}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()
	frame := time.Unix(100, 0)

	next, _ := model.Update(assistantDeltaMsg("abcdef"))
	model = next.(appModel)
	next, _ = model.Update(cursorFrameMsg(frame.Add(cursorFrameInterval)))
	model = next.(appModel)
	if got := model.transcript[0].body; got != "a" {
		t.Fatalf("first frame body = %q, want exactly one char without backlog", got)
	}
	next, _ = model.Update(cursorFrameMsg(frame.Add(2*cursorFrameInterval)))
	model = next.(appModel)
	if got := model.transcript[0].body; got != "ab" {
		t.Fatalf("second frame body = %q, want typewriter pace preserved", got)
	}
}
