package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
)

func (m *appModel) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	if m.activeAssistant < 0 || m.activeAssistant >= len(m.transcript) || m.transcript[m.activeAssistant].kind != entryAssistant {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
		})
		m.activeAssistant = len(m.transcript) - 1
	}
	m.transcript[m.activeAssistant].body += delta
	m.refreshViewport()
}

func (m *appModel) addEntry(entry transcriptEntry) {
	m.transcript = append(m.transcript, entry)
	m.refreshViewport()
}

func (m *appModel) refreshViewport() {
	m.viewport.SetContent(renderTranscript(m.transcript, maxInt(20, m.viewport.Width)))
	m.viewport.GotoBottom()
}

func renderTranscript(entries []transcriptEntry, width int) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, renderEntry(entry, width))
	}
	return strings.Join(parts, "\n\n")
}

func renderEntry(entry transcriptEntry, width int) string {
	const entryGutter = "  "
	label := labelStyle(entry.kind).Render(entry.title)
	bodyWidth := maxInt(20, width-lipgloss.Width(entryGutter))
	body := renderEntryBody(entry, bodyWidth)
	if body == "" {
		return entryGutter + label
	}
	return indentLines(label+"\n"+body, entryGutter)
}

func renderEntryBody(entry transcriptEntry, width int) string {
	body := strings.TrimRight(entry.body, "\n")
	if body == "" {
		return ""
	}
	if entry.kind == entryAssistant {
		return renderMarkdown(body, width)
	}
	return bodyStyle.Width(width).Render(body)
}

func labelStyle(kind entryKind) lipgloss.Style {
	switch kind {
	case entryUser:
		return labelUserStyle
	case entryAssistant:
		return labelAssistantStyle
	case entryTool:
		return labelToolStyle
	case entryError:
		return labelErrorStyle
	default:
		return labelSystemStyle
	}
}

func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
