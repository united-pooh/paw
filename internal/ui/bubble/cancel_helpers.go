package bubble

import "strings"

func (m appModel) latestTurnUserEntry(text string) int {
	want := strings.TrimSpace(text)
	for index := len(m.transcript) - 1; index >= 0; index-- {
		entry := m.transcript[index]
		if entry.kind == entryUser && strings.TrimSpace(entry.body) == want {
			return index
		}
	}
	return -1
}

func (m *appModel) removeInterruptedTurnUserEntry() {
	if m == nil || m.activeTurnUserEntry < 0 || m.activeTurnUserEntry >= len(m.transcript) {
		return
	}
	index := m.activeTurnUserEntry
	if m.transcript[index].kind != entryUser {
		return
	}
	m.transcript = append(m.transcript[:index], m.transcript[index+1:]...)
	removed := 1
	// A queued user row is followed by its queue marker; remove that pair
	// together so restoring the draft does not leave a stale "queued" row.
	if index < len(m.transcript) && m.transcript[index].kind == entrySystem && m.transcript[index].title == "queued" {
		m.transcript = append(m.transcript[:index], m.transcript[index+1:]...)
		removed++
	}
	for _, target := range []*int{&m.activeAssistant, &m.activeThinking, &m.doneAssistant} {
		if *target > index {
			*target -= removed
		}
	}
	m.activeTurnUserEntry = -1
	m.transcriptRenderCache = nil
	m.transcriptLinesValid = false
	m.refreshViewport()
}
