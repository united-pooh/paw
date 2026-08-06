// 双击选中词翻译：应用层正则检测中英文方向，翻译专用 system prompt 要求
// 模型返回结构化 JSON，解析后经 renderModalPanel 弹出面板展示。
package bubble

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/message"
	"paw/internal/model"
)

// 中英文检测正则：英文词允许撇号/连字符（don't、well-known），中文词为纯汉字。
var (
	englishWordPattern = regexp.MustCompile(`^[A-Za-z]+(?:['-][A-Za-z]+)*$`)
	chineseWordPattern = regexp.MustCompile(`^[\p{Han}]+$`)
)

// wordLanguage 是选中词的语言方向。
type wordLanguage int

const (
	wordLangUnknown wordLanguage = iota
	wordLangEnglish
	wordLangChinese
)

// detectWordLanguage 用正则检测选中词语言，决定翻译方向指示（不依赖模型自判）。
func detectWordLanguage(word string) wordLanguage {
	switch {
	case englishWordPattern.MatchString(word):
		return wordLangEnglish
	case chineseWordPattern.MatchString(word):
		return wordLangChinese
	default:
		return wordLangUnknown
	}
}

// translateBasePrompt 是翻译专用 system prompt：只输出一个 JSON 对象。
// 方向指示按 detectWordLanguage 的结果追加。
const translateBasePrompt = `You are a dictionary translator. Respond ONLY with a JSON object:
{"word":"...","phonetic":"...","pos":"...","translation":"...","note":"..."}
- word: the input term as-is.
- phonetic: IPA phonetic notation for English words (omit for Chinese input).
- pos: part of speech, e.g. n./v./adj. (omit for Chinese input).
- translation: concise meaning in the target language.
- note: brief usage tip or example, or omit.`

func translateSystemPrompt(word string) string {
	switch detectWordLanguage(word) {
	case wordLangEnglish:
		return translateBasePrompt + "\n\nThe input is English: translate it to Chinese, and include phonetic and pos."
	case wordLangChinese:
		return translateBasePrompt + "\n\nThe input is Chinese: translate it to English, and omit phonetic and pos."
	default:
		return translateBasePrompt + "\n\nThe input is neither a pure English nor a pure Chinese word: translate it, detecting the direction yourself."
	}
}

// translateJSON 是模型返回的词典结构。
type translateJSON struct {
	Word        string `json:"word"`
	Phonetic    string `json:"phonetic"`
	Pos         string `json:"pos"`
	Translation string `json:"translation"`
	Note        string `json:"note"`
}

// parseTranslateResult 解析模型返回的翻译文本：优先按 JSON 提取词典字段；
// JSON 失败（模型不守格式）时回退为把原始文本当译文显示，保证任何模型可用。
func parseTranslateResult(word, text string) translatePanel {
	panel := translatePanel{state: translatePanelDone, word: word}
	parsed, ok := parseTranslateJSON(text)
	if !ok || strings.TrimSpace(parsed.Translation) == "" {
		panel.translation = strings.TrimSpace(text)
		return panel
	}
	panel.phonetic = parsed.Phonetic
	panel.pos = parsed.Pos
	panel.translation = parsed.Translation
	panel.note = parsed.Note
	return panel
}

func parseTranslateJSON(text string) (translateJSON, bool) {
	var parsed translateJSON
	clean := strings.TrimSpace(text)
	if err := json.Unmarshal([]byte(clean), &parsed); err == nil {
		return parsed, true
	}
	// 模型偶尔用 ```json 围栏包裹：剥掉围栏再试一次。
	if fenced := stripCodeFence(clean); fenced != clean {
		if err := json.Unmarshal([]byte(fenced), &parsed); err == nil {
			return parsed, true
		}
	}
	return parsed, false
}

func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		} else {
			text = ""
		}
	}
	if strings.HasSuffix(text, "```") {
		text = strings.TrimSuffix(text, "```")
	}
	return strings.TrimSpace(text)
}

// runTranslateRequest 执行一次「单会话」翻译请求：不进 transcript、不占
// 上下文，仅 system 翻译指示 + user 选中词。包级变量便于测试替换。
var runTranslateRequest = func(ctx context.Context, cfg model.Config, systemPrompt, word string) (string, error) {
	client := model.NewClient(cfg)
	return client.RunMessage(ctx, []message.Message{
		{Role: message.RoleSystem, Content: systemPrompt},
		{Role: message.RoleUser, Content: word},
	})
}

// startTranslateCmd 发起异步翻译请求，结果经 translateResultMsg 回传。
func (m appModel) startTranslateCmd(seq uint64, word string) tea.Cmd {
	cfg := m.modelConfig.CurrentModelConfig()
	systemPrompt := translateSystemPrompt(word)
	return func() tea.Msg {
		text, err := runTranslateRequest(context.Background(), cfg, systemPrompt, word)
		return translateResultMsg{seq: seq, word: word, text: text, err: err}
	}
}

// renderTranslatePanel 渲染「双击翻译」弹出面板，复用 renderModalPanel。
func (m appModel) renderTranslatePanel() string {
	if m.translatePanel == nil {
		return ""
	}
	panel := m.translatePanel
	lines := []string{wizardTitleStyle.Render("Translate: " + panel.word)}
	switch panel.state {
	case translatePanelLoading:
		lines = append(lines, generatingStatusStyle.Render("翻译中…"))
	case translatePanelError:
		lines = append(lines, labelErrorStyle.Render(panel.err))
	case translatePanelDone:
		meta := make([]string, 0, 2)
		if panel.phonetic != "" {
			meta = append(meta, unselectedProviderStyle.Render(panel.phonetic))
		}
		if panel.pos != "" {
			meta = append(meta, selectedProviderStyle.Render(panel.pos))
		}
		if len(meta) > 0 {
			lines = append(lines, strings.Join(meta, "  "))
		}
		lines = append(lines, bodyStyle.Render(panel.translation))
		if panel.note != "" {
			lines = append(lines, "", thinkingBodyStyle.Render(panel.note))
		}
	}
	lines = append(lines, "", unselectedProviderStyle.Render("Esc close"))
	return m.renderModalPanel(strings.Join(lines, "\n"))
}
