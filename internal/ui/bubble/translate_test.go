// 双击选中词翻译的测试：语言方向正则、system prompt 拼装、双击触发、
// JSON 解析与回退、过期响应丢弃、错误态、面板渲染与 esc 关闭。
package bubble

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	modelcfg "paw/internal/model"
	"paw/internal/settings"
)

// TestDetectWordLanguage 验证中英文方向由应用层正则判定。
func TestDetectWordLanguage(t *testing.T) {
	cases := []struct {
		word string
		want wordLanguage
	}{
		{"apple", wordLangEnglish},
		{"don't", wordLangEnglish},
		{"well-known", wordLangEnglish},
		{"a-b", wordLangEnglish}, // 撇号/连字符词视为英文词
		{"GPT", wordLangEnglish},
		{"你好", wordLangChinese},
		{"世界", wordLangChinese},
		{"hello世界", wordLangUnknown},
		{"123", wordLangUnknown},
		{"a.b", wordLangUnknown}, // 点号是标点分隔符，不算英文词
		{"αβ", wordLangUnknown},  // 希腊字母不属于 [A-Za-z]
		{"", wordLangUnknown},
	}
	for _, tc := range cases {
		if got := detectWordLanguage(tc.word); got != tc.want {
			t.Fatalf("detectWordLanguage(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// TestTranslateSystemPromptDirection 验证方向指示按语言检测结果拼装。
func TestTranslateSystemPromptDirection(t *testing.T) {
	englishPrompt := translateSystemPrompt("apple")
	for _, want := range []string{"dictionary translator", `"phonetic"`, `"pos"`, "English", "Chinese"} {
		if !strings.Contains(englishPrompt, want) {
			t.Fatalf("english prompt = %q, want %q", englishPrompt, want)
		}
	}
	chinesePrompt := translateSystemPrompt("苹果")
	for _, want := range []string{"Chinese", "English"} {
		if !strings.Contains(chinesePrompt, want) {
			t.Fatalf("chinese prompt = %q, want %q", chinesePrompt, want)
		}
	}
	if strings.Contains(chinesePrompt, "include phonetic and pos") {
		t.Fatalf("chinese prompt = %q, should not demand phonetic/pos", chinesePrompt)
	}
	unknownPrompt := translateSystemPrompt("hello世界")
	if !strings.Contains(unknownPrompt, "detecting the direction yourself") {
		t.Fatalf("unknown prompt = %q, want direction self-detection", unknownPrompt)
	}
}

// TestParseTranslateResultStructured 验证词典 JSON 被解析为面板字段。
func TestParseTranslateResultStructured(t *testing.T) {
	panel := parseTranslateResult("world", `{"word":"world","phonetic":"/wɜːld/","pos":"n.","translation":"世界","note":"the earth"}`)
	if panel.state != translatePanelDone || panel.word != "world" ||
		panel.phonetic != "/wɜːld/" || panel.pos != "n." ||
		panel.translation != "世界" || panel.note != "the earth" {
		t.Fatalf("parsed panel = %+v", panel)
	}
}

// TestParseTranslateResultFencedJSON 验证 ```json 围栏包裹的 JSON 也能解析。
func TestParseTranslateResultFencedJSON(t *testing.T) {
	panel := parseTranslateResult("apple", "```json\n{\"word\":\"apple\",\"translation\":\"苹果\"}\n```")
	if panel.translation != "苹果" {
		t.Fatalf("fenced panel translation = %q, want 苹果", panel.translation)
	}
}

// TestParseTranslateResultFallback 验证非法 JSON / 空 translation 回退为原文。
func TestParseTranslateResultFallback(t *testing.T) {
	panel := parseTranslateResult("apple", "苹果。")
	if panel.state != translatePanelDone || panel.translation != "苹果。" {
		t.Fatalf("fallback panel = %+v", panel)
	}
	empty := parseTranslateResult("apple", `{"word":"apple","translation":""}`)
	if empty.translation != `{"word":"apple","translation":""}` {
		t.Fatalf("empty translation fallback = %+v", empty)
	}
}

// translateTestModel 构造开启了「翻译选中词」的测试模型。
func translateTestModel(t *testing.T) appModel {
	t.Helper()
	cfg := settings.DefaultConfig()
	cfg.UI.TranslateOnDoubleClick = true
	settingsController := &fakeSettingsController{current: cfg}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()
	return model
}

// doubleClickWord 在 transcript 首行指定词上完成一次双击，返回 press2 的 cmd。
func doubleClickWord(model appModel, word string) (appModel, tea.Cmd) {
	lines := model.transcriptLineSnapshots()
	offset := strings.Index(lines[0].plain, word)
	if offset < 0 {
		return model, nil
	}
	x := mainContentPadding + offset + 3 // 点击词内部
	y := model.transcriptScreenTop()
	press := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	next, _ := model.Update(press)
	model = next.(appModel)
	next, _ = model.Update(release)
	model = next.(appModel)
	next, cmd := model.Update(press) // 双击：第二次按下
	model = next.(appModel)
	next, _ = model.Update(release)
	model = next.(appModel)
	return model, cmd
}

// TestDoubleClickTranslateTriggersSingleShotRequest 验证开关开启时双击选词
// 触发一次单会话翻译请求：system prompt 含方向指示、user 为选中词，面板
// 从 loading 进入 done。
func TestDoubleClickTranslateTriggersSingleShotRequest(t *testing.T) {
	oldRun := runTranslateRequest
	var gotSystem, gotWord string
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		gotSystem = systemPrompt
		gotWord = word
		return `{"word":"world","phonetic":"/wɜːld/","pos":"n.","translation":"世界"}`, nil
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := translateTestModel(t)
	model, cmd := doubleClickWord(model, "world")
	if cmd == nil {
		t.Fatal("double-click with translate enabled returned no request command")
	}
	if model.translatePanel == nil || model.translatePanel.state != translatePanelLoading {
		t.Fatalf("translate panel = %+v, want loading", model.translatePanel)
	}
	if model.selectionMode != selectionModeWord || !model.selectionActive {
		t.Fatalf("double-click selection = mode:%v active:%v, want word selection", model.selectionMode, model.selectionActive)
	}

	msg := cmd()
	result, ok := msg.(translateResultMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want translateResultMsg", msg)
	}
	if gotWord != "world" {
		t.Fatalf("request word = %q, want world", gotWord)
	}
	for _, want := range []string{"dictionary translator", "English", "Chinese"} {
		if !strings.Contains(gotSystem, want) {
			t.Fatalf("request system prompt = %q, want %q", gotSystem, want)
		}
	}

	next, _ := model.Update(result)
	model = next.(appModel)
	if model.translatePanel == nil || model.translatePanel.state != translatePanelDone {
		t.Fatalf("translate panel after result = %+v, want done", model.translatePanel)
	}
	if model.translatePanel.translation != "世界" {
		t.Fatalf("translation = %q, want 世界", model.translatePanel.translation)
	}
}

// TestDoubleClickTranslateDisabledByDefault 验证开关关闭时双击只选词、不发起请求。
func TestDoubleClickTranslateDisabledByDefault(t *testing.T) {
	oldRun := runTranslateRequest
	called := false
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		called = true
		return "", nil
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{}) // 默认设置：开关关闭
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	model, cmd := doubleClickWord(model, "world")
	if cmd != nil {
		t.Fatal("double-click with translate disabled returned a request command")
	}
	if called {
		t.Fatal("translate request was sent with the setting disabled")
	}
	if model.translatePanel != nil {
		t.Fatalf("translate panel = %+v, want nil", model.translatePanel)
	}
	if !model.selectionActive || model.selectionMode != selectionModeWord {
		t.Fatalf("selection = active:%v mode:%v, want word selection preserved", model.selectionActive, model.selectionMode)
	}
}

// TestTranslateChineseWordPromptsEnglishDirection 验证中文词请求使用中译英方向。
func TestTranslateChineseWordPromptsEnglishDirection(t *testing.T) {
	oldRun := runTranslateRequest
	var gotSystem, gotWord string
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		gotSystem = systemPrompt
		gotWord = word
		return `{"word":"世界","translation":"world"}`, nil
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := translateTestModel(t)
	model.replaceTranscript([]transcriptEntry{{
		kind:  entryUser,
		title: "you",
		body:  "世界 你好",
	}})
	model.refreshViewport()
	model.viewport.GotoTop()

	model, cmd := doubleClickWord(model, "世界")
	if cmd == nil {
		t.Fatal("double-click on Chinese word returned no request command")
	}
	msg := cmd()
	result, ok := msg.(translateResultMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want translateResultMsg", msg)
	}
	if gotWord != "世界" {
		t.Fatalf("request word = %q, want 世界", gotWord)
	}
	if !strings.Contains(gotSystem, "The input is Chinese") {
		t.Fatalf("chinese system prompt = %q, want Chinese direction", gotSystem)
	}
	next, _ := model.Update(result)
	model = next.(appModel)
	if model.translatePanel.translation != "world" {
		t.Fatalf("translation = %q, want world", model.translatePanel.translation)
	}
}

// TestTranslateStaleResultDropped 验证旧请求的过期结果不会覆盖新面板。
func TestTranslateStaleResultDropped(t *testing.T) {
	oldRun := runTranslateRequest
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		return `{"translation":"stale"}`, nil
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := translateTestModel(t)
	model, _ = doubleClickWord(model, "world") // seq 1：丢弃结果不处理
	// 模拟双击窗口过期后再双击（第二次双击与第一次间隔超过 400ms）。
	model.clickCount = 0
	model.lastClickAt = time.Time{}
	model.clickActionPending = false
	model, cmd2 := doubleClickWord(model, "world")
	if cmd2 == nil {
		t.Fatal("second double-click returned no request command")
	}

	// 先到 seq 1 的过期结果：必须被丢弃。
	stale := translateResultMsg{seq: model.translateSeq - 1, word: "world", text: `{"translation":"stale"}`}
	next, _ := model.Update(stale)
	model = next.(appModel)
	if model.translatePanel == nil || model.translatePanel.translation == "stale" {
		t.Fatalf("stale result overwrote panel: %+v", model.translatePanel)
	}

	// 当前 seq 的结果正常生效。
	current := translateResultMsg{seq: model.translateSeq, word: "world", text: `{"translation":"fresh"}`}
	next, _ = model.Update(current)
	model = next.(appModel)
	if model.translatePanel.translation != "fresh" {
		t.Fatalf("current result did not apply: %+v", model.translatePanel)
	}
}

// TestTranslateErrorShowsPanel 验证请求失败时面板显示错误态。
func TestTranslateErrorShowsPanel(t *testing.T) {
	oldRun := runTranslateRequest
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		return "", errors.New("model timeout")
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := translateTestModel(t)
	model, cmd := doubleClickWord(model, "world")
	if cmd == nil {
		t.Fatal("double-click returned no request command")
	}
	msg := cmd()
	next, _ := model.Update(msg)
	model = next.(appModel)
	if model.translatePanel == nil || model.translatePanel.state != translatePanelError {
		t.Fatalf("translate panel = %+v, want error state", model.translatePanel)
	}
	if !strings.Contains(model.translatePanel.err, "model timeout") {
		t.Fatalf("panel error = %q, want model timeout", model.translatePanel.err)
	}
}

// TestTranslatePanelRendersDictionary 验证 done 面板渲染音标、词性、释义。
func TestTranslatePanelRendersDictionary(t *testing.T) {
	model := translateTestModel(t)
	model.height = 20
	model.relayout()
	model.translatePanel = &translatePanel{
		state:       translatePanelDone,
		word:        "world",
		phonetic:    "/wɜːld/",
		pos:         "n.",
		translation: "世界",
		note:        "the earth",
	}
	rendered := ansi.Strip(model.renderTranslatePanel())
	for _, want := range []string{"Translate: world", "/wɜːld/", "n.", "世界", "the earth", "Esc close"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("translate panel = %q, want %q", rendered, want)
		}
	}
}

// TestTranslatePanelEscClosesAndKeepsSelection 验证 esc 关闭面板且选区保留。
func TestTranslatePanelEscClosesAndKeepsSelection(t *testing.T) {
	model := translateTestModel(t)
	model.translatePanel = &translatePanel{state: translatePanelLoading, word: "world"}
	model.selectionActive = true
	model.selectionMode = selectionModeWord
	model.selectionStart = selectionPoint{row: 0, col: 8}
	model.selectionEnd = selectionPoint{row: 0, col: 13}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.translatePanel != nil {
		t.Fatalf("translate panel = %+v, want nil after esc", model.translatePanel)
	}
	if !model.selectionActive || model.selectionMode != selectionModeWord {
		t.Fatalf("esc closed selection too: active:%v mode:%v", model.selectionActive, model.selectionMode)
	}
	if got := model.selectedTranscriptText(); got != "world" {
		t.Fatalf("selection after esc = %q, want world", got)
	}
}

// TestTranslatePanelLoadingOverlay 验证 loading 面板出现在 modal 合成层。
func TestTranslatePanelLoadingOverlay(t *testing.T) {
	model := translateTestModel(t)
	model.translatePanel = &translatePanel{state: translatePanelLoading, word: "world"}
	rendered := ansi.Strip(model.renderActiveModalBox(model.currentLayout()))
	if !strings.Contains(rendered, "Translate: world") || !strings.Contains(rendered, "翻译中") {
		t.Fatalf("modal box = %q, want loading translate panel", rendered)
	}
}
