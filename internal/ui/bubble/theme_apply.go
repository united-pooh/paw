package bubble

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/theme"
	"paw/internal/ui/bubble/textareax"
)

// activePalette 记录全局 legacy 样式当前对应的调色板。View() 每帧都会
// 调用 activateThemeStyles 兜底；调色板未变化时跳过 50+ 个 lipgloss 样式
// 的重建，避免给每帧渲染增加固定开销。
var activePalette theme.Palette

func (m *appModel) activateThemeStyles() {
	if m == nil || m.theme.ID == "" {
		return
	}
	if m.styles.Colors.palette == activePalette {
		return
	}
	colorManager = m.styles.Colors
	rebuildLegacyStyles()
	activePalette = m.styles.Colors.palette
}

func (m *appModel) applyTheme(id theme.ThemeID) error {
	next, ok := theme.ByID(id)
	if !ok {
		return fmt.Errorf("theme %q is not registered", id)
	}
	m.theme = next
	m.styles = NewStyleSet(next.Colors)
	m.activateThemeStyles()
	m.applyTextareaTheme()
	m.invalidateTranscriptRender()
	m.transcriptRefreshPending = false
	m.refreshViewportPreservingOffset()
	m.applyCursorAnimation()
	return nil
}

func (m *appModel) applyTextareaTheme() {
	if m == nil {
		return
	}
	bg := m.styles.Colors.LipglossColor(colorTerminalBackground)
	base := lipgloss.NewStyle().Background(bg)
	text := m.styles.Body
	placeholder := lipgloss.NewStyle().Foreground(m.styles.Colors.LipglossColor(colorMarkdownRule)).Background(bg)
	applyTextareaStyles(&m.input, base, text, placeholder)
}

func applyTextareaStyles(input *textareax.Model, base, text, placeholder lipgloss.Style) {
	if input == nil {
		return
	}
	input.FocusedStyle.Base = base
	input.FocusedStyle.CursorLine = base
	input.FocusedStyle.Text = text
	input.FocusedStyle.Placeholder = placeholder
	input.BlurredStyle.Base = base
	input.BlurredStyle.CursorLine = base
	input.BlurredStyle.Text = text
	input.BlurredStyle.Placeholder = placeholder
}
