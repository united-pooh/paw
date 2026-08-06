package bubble

import (
	"bytes"
	"fmt"
	"paw/internal/message"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// inputTokenKind describes the semantic style of a completion-created token.
type inputTokenKind uint8

const (
	inputTokenCommand inputTokenKind = iota
	inputTokenSkill
	inputTokenFile
	inputTokenImage
)

// inputToken keeps a half-open rune range into the underlying textarea value.
// AutoSpace records the single separator inserted by completion so atomic
// deletion can remove it together with the token.
type inputToken struct {
	Kind      inputTokenKind
	Start     int
	End       int
	Label     string
	AutoSpace bool
	Image     *message.ImagePart
}

// inputDraft is the in-memory input representation. Text is always the exact
// syntax submitted to Runner; Tokens only affect the Bubble Tea presentation.
type inputDraft struct {
	Text   string
	Tokens []inputToken
}

// inputProjectionAtom 是投影行中的一个原子。start 记录该原子在输入文本
// rune 序列中的起始偏移（cursor 原子即光标偏移），供投影网格垂直移动把
// 光标映射回 textarea 的 rune 坐标。
type inputProjectionAtom struct {
	text   string
	kind   inputTokenKind
	token  bool
	cursor bool
	width  int
	start  int
}

// inputProjectionLine 是投影的一行。start 记录该行首个原子在输入文本 rune
// 序列中的起始偏移（空行取逻辑行首），是 soft-wrap 行间移动的映射锚点。
type inputProjectionLine struct {
	atoms       []inputProjectionAtom
	width       int
	logicalLine int
	start       int
}

type inputProjection struct {
	lines         []inputProjectionLine
	cursorRow     int
	cursorColumn  int
	cursorAbsolute int
}

func cloneInputTokens(tokens []inputToken) []inputToken {
	out := make([]inputToken, len(tokens))
	copy(out, tokens)
	for i := range out {
		if tokens[i].Image != nil {
			image := *tokens[i].Image
			image.Data = append([]byte(nil), tokens[i].Image.Data...)
			out[i].Image = &image
		}
	}
	return out
}

func cloneInputDraft(draft inputDraft) inputDraft {
	return inputDraft{Text: draft.Text, Tokens: cloneInputTokens(draft.Tokens)}
}

func (m appModel) currentInputDraft() inputDraft {
	return inputDraft{Text: m.input.Value(), Tokens: cloneInputTokens(m.inputTokens)}
}

func (m *appModel) setInputDraft(draft inputDraft) {
	if m == nil {
		return
	}
	draft.Tokens = normalizeInputTokens(draft.Text, draft.Tokens)
	m.input.SetValue(draft.Text)
	m.input.CursorEnd()
	m.inputTokens = cloneInputTokens(draft.Tokens)
	m.submittedDraft = inputDraft{}
}

func (m *appModel) clearInputTokens() {
	if m == nil {
		return
	}
	m.inputTokens = nil
	m.submittedDraft = inputDraft{}
}

func normalizeInputTokens(text string, tokens []inputToken) []inputToken {
	runeCount := len([]rune(text))
	out := make([]inputToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Start < 0 || token.End <= token.Start || token.End > runeCount || strings.TrimSpace(token.Label) == "" {
			continue
		}
		token.Label = sanitizeTerminalText(token.Label)
		out = append(out, token)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End < out[j].End
		}
		return out[i].Start < out[j].Start
	})
	compacted := out[:0]
	for _, token := range out {
		if len(compacted) > 0 && token.Start < compacted[len(compacted)-1].End {
			continue
		}
		compacted = append(compacted, token)
	}
	return compacted
}

// canonicalSkillReferenceTokens recovers only the Markdown references emitted
// by skill completion. It intentionally does not infer tokens from bare
// prefixes in restored or manually typed text.
func canonicalSkillReferenceTokens(text string) []inputToken {
	const prefix = "[$"
	tokens := make([]inputToken, 0)
	search := 0
	for search < len(text) {
		relativeStart := strings.Index(text[search:], prefix)
		if relativeStart < 0 {
			break
		}
		start := search + relativeStart
		nameEndRelative := strings.IndexByte(text[start+len(prefix):], ']')
		if nameEndRelative < 0 {
			break
		}
		nameStart := start + len(prefix)
		nameEnd := nameStart + nameEndRelative
		name := text[nameStart:nameEnd]
		if name == "" || strings.IndexAny(name, " \t\r\n") >= 0 {
			search = nameEnd + 1
			continue
		}
		if nameEnd+1 >= len(text) || text[nameEnd+1] != '(' {
			search = nameEnd + 1
			continue
		}
		pathStart := nameEnd + 2
		pathEndRelative := strings.IndexByte(text[pathStart:], ')')
		if pathEndRelative < 0 {
			break
		}
		pathEnd := pathStart + pathEndRelative
		path := text[pathStart:pathEnd]
		end := pathEnd + 1
		if path == "" || strings.IndexAny(path, "\r\n") >= 0 ||
			(path != "SKILL.md" && !strings.HasSuffix(path, "/SKILL.md")) {
			search = end
			continue
		}
		tokens = append(tokens, inputToken{
			Kind:  inputTokenSkill,
			Start: len([]rune(text[:start])),
			End:   len([]rune(text[:end])),
			Label: name,
		})
		search = end
	}
	return normalizeInputTokens(text, tokens)
}

// replaceInputRange replaces a rune range and rebases every unaffected token.
func (m *appModel) replaceInputRange(start, end int, replacement string) {
	if m == nil {
		return
	}
	before := []rune(m.input.Value())
	start = clampInt(start, 0, len(before))
	end = clampInt(end, start, len(before))
	inserted := []rune(replacement)
	next := append(append(append([]rune(nil), before[:start]...), inserted...), before[end:]...)
	delta := len(inserted) - (end - start)
	tokens := make([]inputToken, 0, len(m.inputTokens))
	for _, token := range m.inputTokens {
		switch {
		case token.End <= start:
			tokens = append(tokens, token)
		case token.Start >= end:
			token.Start += delta
			token.End += delta
			tokens = append(tokens, token)
		}
	}
	m.input.SetValue(string(next))
	setTextareaAbsoluteCursor(&m.input, start+len(inserted))
	m.inputTokens = normalizeInputTokens(string(next), tokens)
	m.submittedDraft = inputDraft{}
}

func (m *appModel) replaceInputRangeWithToken(start, end int, raw, label string, kind inputTokenKind, autoSpace bool) {
	if m == nil {
		return
	}
	replacement := raw
	if autoSpace {
		replacement += " "
	}
	m.replaceInputRange(start, end, replacement)
	token := inputToken{
		Kind:      kind,
		Start:     start,
		End:       start + len([]rune(raw)),
		Label:     label,
		AutoSpace: autoSpace,
	}
	m.inputTokens = normalizeInputTokens(m.input.Value(), append(m.inputTokens, token))
}

func textareaAbsoluteCursor(input textarea.Model) int {
	valueLines := strings.Split(input.Value(), "\n")
	line := clampInt(input.Line(), 0, maxInt(0, len(valueLines)-1))
	offset := 0
	for i := 0; i < line; i++ {
		offset += len([]rune(valueLines[i])) + 1
	}
	info := input.LineInfo()
	// StartColumn and ColumnOffset are rune offsets into the logical textarea
	// line. CharOffset is a display-cell width and must not be used for token
	// ranges or edit positions.
	return offset + info.StartColumn + info.ColumnOffset
}

func setTextareaAbsoluteCursor(input *textarea.Model, absolute int) {
	if input == nil {
		return
	}
	lines := strings.Split(input.Value(), "\n")
	absolute = clampInt(absolute, 0, len([]rune(input.Value())))
	targetLine := 0
	targetColumn := absolute
	for targetLine < len(lines)-1 {
		lineRunes := len([]rune(lines[targetLine]))
		if targetColumn <= lineRunes {
			break
		}
		targetColumn -= lineRunes + 1
		targetLine++
	}
	for input.Line() > targetLine {
		input.CursorUp()
	}
	for input.Line() < targetLine {
		input.CursorDown()
	}
	input.SetCursor(targetColumn)
}

func (m *appModel) updateTokenAwareInput(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	beforeText := m.input.Value()
	beforeCursor := textareaAbsoluteCursor(m.input)
	beforeTokens := cloneInputTokens(m.inputTokens)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	afterText := m.input.Value()
	afterCursor := textareaAbsoluteCursor(m.input)

	if beforeText != afterText {
		m.reconcileInputTokenEdit(beforeText, afterText, beforeCursor, afterCursor, beforeTokens)
	} else {
		m.snapInputCursorToToken(beforeCursor, afterCursor)
	}
	return cmd
}

// reconcileInputTokenEdit derives the textarea edit from its common
// prefix/suffix. If the deletion touches a token, the edit expands to consume
// the full token and its completion-created separator.
func (m *appModel) reconcileInputTokenEdit(beforeText, afterText string, beforeCursor, afterCursor int, beforeTokens []inputToken) {
	before := []rune(beforeText)
	after := []rune(afterText)
	oldStart, oldEnd, newEnd := inputEditRange(before, after, beforeCursor, afterCursor)
	inserted := append([]rune(nil), after[oldStart:newEnd]...)
	if len(before) == len(after) {
		for _, token := range beforeTokens {
			if oldStart < token.End && oldEnd > token.Start {
				m.input.SetValue(beforeText)
				setTextareaAbsoluteCursor(&m.input, beforeCursor)
				m.inputTokens = cloneInputTokens(beforeTokens)
				return
			}
		}
	}

	expandedStart := oldStart
	expandedEnd := oldEnd
	for _, token := range beforeTokens {
		touched := oldStart < token.End && oldEnd > token.Start
		if token.AutoSpace && oldStart == token.End && oldEnd == token.End+1 {
			touched = true
		}
		if oldStart == oldEnd {
			touched = oldStart > token.Start && oldStart < token.End
		}
		if !touched {
			continue
		}
		expandedStart = minInt(expandedStart, token.Start)
		expandedEnd = maxInt(expandedEnd, token.End)
		if token.AutoSpace && token.End < len(before) && before[token.End] == ' ' {
			expandedEnd = maxInt(expandedEnd, token.End+1)
		}
	}

	if expandedStart != oldStart || expandedEnd != oldEnd {
		next := append(append(append([]rune(nil), before[:expandedStart]...), inserted...), before[expandedEnd:]...)
		nextCursor := expandedStart + len(inserted)
		m.input.SetValue(string(next))
		setTextareaAbsoluteCursor(&m.input, nextCursor)
		afterText = string(next)
		afterCursor = nextCursor
		oldStart = expandedStart
		oldEnd = expandedEnd
		newEnd = expandedStart + len(inserted)
	}

	delta := (newEnd - oldStart) - (oldEnd - oldStart)
	tokens := make([]inputToken, 0, len(beforeTokens))
	for _, token := range beforeTokens {
		switch {
		case token.End <= oldStart:
			tokens = append(tokens, token)
		case token.Start >= oldEnd:
			token.Start += delta
			token.End += delta
			tokens = append(tokens, token)
		}
	}
	m.inputTokens = normalizeInputTokens(afterText, tokens)
	m.submittedDraft = inputDraft{}
	m.snapInputCursorToToken(beforeCursor, afterCursor)
}

func inputEditRange(before, after []rune, beforeCursor, afterCursor int) (oldStart, oldEnd, newEnd int) {
	beforeCursor = clampInt(beforeCursor, 0, len(before))
	afterCursor = clampInt(afterCursor, 0, len(after))
	switch {
	case len(after) > len(before) && afterCursor >= beforeCursor:
		inserted := len(after) - len(before)
		return beforeCursor, beforeCursor, clampInt(beforeCursor+inserted, 0, len(after))
	case len(after) < len(before) && afterCursor < beforeCursor:
		deleted := len(before) - len(after)
		return afterCursor, clampInt(afterCursor+deleted, afterCursor, len(before)), afterCursor
	case len(after) < len(before):
		deleted := len(before) - len(after)
		return beforeCursor, clampInt(beforeCursor+deleted, beforeCursor, len(before)), beforeCursor
	}

	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	return prefix, len(before) - suffix, len(after) - suffix
}

func (m *appModel) snapInputCursorToToken(beforeCursor, afterCursor int) {
	if m == nil {
		return
	}
	for _, token := range m.inputTokens {
		if afterCursor <= token.Start || afterCursor >= token.End {
			continue
		}
		target := token.Start
		if afterCursor > beforeCursor {
			target = token.End
		} else if afterCursor == beforeCursor && afterCursor-token.Start > token.End-afterCursor {
			target = token.End
		}
		setTextareaAbsoluteCursor(&m.input, target)
		return
	}
	snapped := snapRuneOffsetToGrapheme(m.input.Value(), afterCursor, afterCursor-beforeCursor)
	if snapped != afterCursor {
		setTextareaAbsoluteCursor(&m.input, snapped)
	}
}

func snapRuneOffsetToGrapheme(text string, offset, direction int) int {
	runes := []rune(text)
	offset = clampInt(offset, 0, len(runes))
	runeOffset := 0
	for remaining := text; remaining != ""; {
		cluster, _ := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		clusterRunes := len([]rune(cluster))
		clusterEnd := runeOffset + clusterRunes
		if offset > runeOffset && offset < clusterEnd {
			if direction > 0 {
				return clusterEnd
			}
			if direction < 0 {
				return runeOffset
			}
			if offset-runeOffset > clusterEnd-offset {
				return clusterEnd
			}
			return runeOffset
		}
		runeOffset = clusterEnd
	}
	return offset
}

func trimInputDraft(draft inputDraft) inputDraft {
	runes := []rune(draft.Text)
	start := 0
	for start < len(runes) && isInputSpace(runes[start]) {
		start++
	}
	end := len(runes)
	for end > start && isInputSpace(runes[end-1]) {
		end--
	}
	out := inputDraft{Text: string(runes[start:end])}
	for _, token := range draft.Tokens {
		if token.Start < start || token.End > end {
			continue
		}
		token.Start -= start
		token.End -= start
		token.AutoSpace = token.AutoSpace && token.End < end-start && runes[start+token.End] == ' '
		out.Tokens = append(out.Tokens, token)
	}
	out.Tokens = normalizeInputTokens(out.Text, out.Tokens)
	return out
}

func isInputSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func joinInputDrafts(drafts []inputDraft, separator string) inputDraft {
	var out inputDraft
	for i, draft := range drafts {
		if i > 0 {
			out.Text += separator
		}
		offset := len([]rune(out.Text))
		out.Text += draft.Text
		for _, token := range draft.Tokens {
			token.Start += offset
			token.End += offset
			out.Tokens = append(out.Tokens, token)
		}
	}
	out.Tokens = normalizeInputTokens(out.Text, out.Tokens)
	return out
}

func stripContinuationDraft(draft inputDraft) (bool, inputDraft) {
	trimmed := trimInputDraft(draft)
	runes := []rune(trimmed.Text)
	if len(runes) == 0 || runes[len(runes)-1] != '\\' {
		return false, trimmed
	}
	trimmed.Text = string(runes[:len(runes)-1])
	return true, trimInputDraft(trimmed)
}

func projectInput(raw string, tokens []inputToken, cursor, width int, folded bool) inputProjection {
	width = maxInt(1, width)
	tokens = normalizeInputTokens(raw, tokens)
	rawRunes := []rune(raw)
	hasCursor := cursor >= 0
	if hasCursor {
		cursor = clampInt(cursor, 0, len(rawRunes))
	}
	lines := []inputProjectionLine{{logicalLine: 0, start: 0}}
	row := 0
	column := 0
	logicalLine := 0
	cursorSet := false

	markCursor := func() {
		if cursorSet {
			return
		}
		lines[row].atoms = append(lines[row].atoms, inputProjectionAtom{cursor: true, start: cursor})
		cursorSet = true
	}
	newVisualLine := func(nextLogical, start int) {
		lines = append(lines, inputProjectionLine{logicalLine: nextLogical, start: start})
		row++
		column = 0
	}
	appendCluster := func(cluster string, kind inputTokenKind, token bool, start int) {
		clusterWidth := terminalCellWidth(cluster)
		if clusterWidth < 1 {
			clusterWidth = 1
		}
		if column > 0 && column+clusterWidth > width {
			newVisualLine(logicalLine, start)
		}
		lines[row].atoms = append(lines[row].atoms, inputProjectionAtom{
			text:  cluster,
			kind:  kind,
			token: token,
			width: clusterWidth,
			start: start,
		})
		column += clusterWidth
		lines[row].width = column
	}
	appendText := func(text string, kind inputTokenKind, token bool, start int) {
		for remaining := text; remaining != ""; {
			cluster, _ := terminalFirstGraphemeCluster(remaining)
			remaining = remaining[len(cluster):]
			appendCluster(cluster, kind, token, start)
			start += len([]rune(cluster))
		}
	}

	tokenIndex := 0
	for i := 0; i < len(rawRunes); {
		if hasCursor && cursor == i {
			markCursor()
		}
		if tokenIndex < len(tokens) && tokens[tokenIndex].Start == i {
			token := tokens[tokenIndex]
			appendText(token.Label, token.Kind, true, i)
			i = token.End
			tokenIndex++
			if hasCursor && cursor > token.Start && cursor < token.End {
				cursor = token.End
			}
			continue
		}
		if rawRunes[i] == '\n' {
			i++
			logicalLine++
			newVisualLine(logicalLine, i)
			continue
		}
		cluster, _ := terminalFirstGraphemeCluster(string(rawRunes[i:]))
		if cluster == "" {
			break
		}
		clusterRunes := len([]rune(cluster))
		if hasCursor && cursor > i && cursor < i+clusterRunes {
			if cursor-i > i+clusterRunes-cursor {
				cursor = i + clusterRunes
			} else {
				cursor = i
				markCursor()
			}
		}
		appendCluster(cluster, inputTokenCommand, false, i)
		i += clusterRunes
	}
	if hasCursor && cursor == len(rawRunes) {
		markCursor()
	}
	if hasCursor && !cursorSet {
		markCursor()
	}
	if hasCursor {
		lines = normalizeProjectedCursorWrap(lines, width)
	}

	if folded {
		start, end, ok := inputPasteFoldHiddenRangeWithWidth(raw, width)
		if ok {
			filtered := make([]inputProjectionLine, 0, len(lines))
			markerAdded := false
			hidden := end - start
			for index, line := range lines {
				var inRange bool
				if inputPasteFoldable(raw) {
					inRange = line.logicalLine >= start && line.logicalLine < end
				} else {
					// 单条长行折叠：投影行索引就是 soft-wrap 后的可视行偏移。
					inRange = index >= start && index < end
				}
				if inRange {
					if !markerAdded {
						marker := inputProjectionLine{logicalLine: start, start: line.start}
						text := truncateStyledCellLine(
							formatInputFoldMarker(hidden),
							width,
						)
						markerWidth := terminalCellWidth(text)
						marker.atoms = []inputProjectionAtom{{text: text, width: markerWidth, start: line.start}}
						marker.width = markerWidth
						filtered = append(filtered, marker)
						markerAdded = true
					}
					continue
				}
				filtered = append(filtered, line)
			}
			lines = filtered
		}
	}

	projection := inputProjection{lines: lines, cursorAbsolute: -1}
	if hasCursor {
		projection.cursorAbsolute = cursor
	}
	for lineIndex := range projection.lines {
		cell := 0
		for atomIndex := range projection.lines[lineIndex].atoms {
			atom := &projection.lines[lineIndex].atoms[atomIndex]
			if atom.cursor {
				projection.cursorRow = lineIndex
				projection.cursorColumn = cell
			}
			cell += atom.width
		}
	}
	return projection
}

func normalizeProjectedCursorWrap(lines []inputProjectionLine, width int) []inputProjectionLine {
	for lineIndex := range lines {
		cell := 0
		for atomIndex, atom := range lines[lineIndex].atoms {
			if !atom.cursor {
				cell += atom.width
				continue
			}
			moveToNext := atomIndex == len(lines[lineIndex].atoms)-1 &&
				lineIndex+1 < len(lines) &&
				lines[lineIndex+1].logicalLine == lines[lineIndex].logicalLine
			if cell >= width {
				moveToNext = true
			}
			if !moveToNext {
				return lines
			}
			cursorStart := atom.start
			lines[lineIndex].atoms = append(
				lines[lineIndex].atoms[:atomIndex],
				lines[lineIndex].atoms[atomIndex+1:]...,
			)
			if lineIndex+1 >= len(lines) || lines[lineIndex+1].logicalLine != lines[lineIndex].logicalLine {
				lines = append(lines, inputProjectionLine{})
				copy(lines[lineIndex+2:], lines[lineIndex+1:])
				lines[lineIndex+1] = inputProjectionLine{logicalLine: lines[lineIndex].logicalLine, start: cursorStart}
			}
			lines[lineIndex+1].atoms = append(
				[]inputProjectionAtom{{cursor: true, start: cursorStart}},
				lines[lineIndex+1].atoms...,
			)
			return lines
		}
	}
	return lines
}

func formatInputFoldMarker(hidden int) string {
	return fmt.Sprintf(inputPasteFoldMarkerLine, hidden)
}

// projectedLineRuneEnd 返回投影行内文本的 rune 末端（不含 cursor/折叠 marker）。
// 空行的 end 与 start 相同。
func projectedLineRuneEnd(line inputProjectionLine) int {
	end := line.start
	for _, atom := range line.atoms {
		if atom.text == "" || atom.cursor {
			continue
		}
		end = maxInt(end, atom.start+len([]rune(atom.text)))
	}
	return end
}

// projectedVerticalMoveTarget 把光标在投影网格中向 direction（±1）移动一行，
// 返回目标绝对 rune 偏移。soft-wrap 行之间保持行内 rune 偏移；跨逻辑行时
// 落在目标行内（可能为行首）。光标已在投影边缘或目标行是折叠占位行时
// 返回 moved=false。
func projectedVerticalMoveTarget(projection inputProjection, absoluteCursor, direction int) (int, bool) {
	if direction == 0 || len(projection.lines) == 0 {
		return absoluteCursor, false
	}
	targetRow := projection.cursorRow + direction
	if targetRow < 0 || targetRow >= len(projection.lines) {
		return absoluteCursor, false
	}
	current := projection.lines[projection.cursorRow]
	target := projection.lines[targetRow]
	relative := absoluteCursor - current.start
	if relative < 0 {
		relative = 0
	}
	targetEnd := projectedLineRuneEnd(target)
	if targetEnd <= target.start && len(target.atoms) == 0 {
		// 空目标行：光标落在该逻辑行首（空行没有可停留的文本）。
		return target.start, true
	}
	targetOffset := target.start + minInt(relative, targetEnd-target.start)
	return targetOffset, true
}

func (m appModel) inputTokenProjection() inputProjection {
	return projectInput(
		m.input.Value(),
		m.inputTokens,
		textareaAbsoluteCursor(m.input),
		m.input.Width(),
		m.shouldRenderFoldedInput(),
	)
}

func inputTokenStyleFor(kind inputTokenKind) lipgloss.Style {
	if kind == inputTokenImage {
		return inputImageTokenStyle
	}
	if kind == inputTokenFile {
		return inputFileTokenStyle
	}
	return inputCommandTokenStyle
}

func renderProjectedTextLine(line inputProjectionLine, baseStyle lipgloss.Style) string {
	var out strings.Builder
	for _, atom := range line.atoms {
		if atom.text == "" {
			continue
		}
		style := baseStyle
		if atom.token {
			style = inputTokenStyleFor(atom.kind)
		}
		out.WriteString(style.Render(atom.text))
	}
	return out.String()
}

func renderTokenizedTranscriptBody(raw string, tokens []inputToken, width int) string {
	projection := projectInput(raw, tokens, -1, width, false)
	lines := make([]string, 0, len(projection.lines))
	// Ordinary atoms must carry the user foreground themselves. renderEntryAt's
	// outer style cannot override the bodyStyle ANSI foreground previously
	// emitted here. Semantic token atoms retain their dedicated colors.
	baseStyle := userTranscriptRowStyle.UnsetBackground()
	for _, line := range projection.lines {
		lines = append(lines, renderProjectedTextLine(line, baseStyle))
	}
	return strings.Join(lines, "\n")
}

func (m appModel) renderTokenInputContent() string {
	projection := m.inputTokenProjection()
	height := maxInt(1, m.input.Height())
	rowWidth := maxInt(1, m.input.Width())

	if m.input.Value() == "" {
		placeholderText := m.input.Placeholder
		placeholderStyle := m.input.FocusedStyle.Placeholder
		if m.isTerminalInputActive() || m.runningTerminal {
			placeholderText = "$"
			placeholderStyle = terminalInputLabelStyle
		}
		if placeholderText != "" {
			line := fitStyledCellLine(placeholderStyle.Render(placeholderText), rowWidth)
			rendered := make([]string, 0, height)
			rendered = append(rendered, m.input.FocusedStyle.CursorLine.Render(line))
			for len(rendered) < height {
				rendered = append(rendered, "")
			}
			return strings.Join(rendered, "\n")
		}
	}

	start := 0
	if len(projection.lines) > height && projection.cursorRow >= height {
		start = projection.cursorRow - height + 1
	}
	start = clampInt(start, 0, maxInt(0, len(projection.lines)-height))
	end := minInt(len(projection.lines), start+height)
	rendered := make([]string, 0, height)
	for row, line := range projection.lines[start:end] {
		if row == projection.cursorRow-start {
			rendered = append(rendered, m.renderProjectedCursorLine(line, rowWidth))
			continue
		}
		style := bodyStyle
		if m.isTerminalInputActive() || m.runningTerminal {
			style = terminalInputTextStyle
		}
		var out strings.Builder
		for _, atom := range line.atoms {
			if atom.text == "" {
				continue
			}
			atomStyle := style
			if atom.token {
				atomStyle = inputTokenStyleFor(atom.kind)
			}
			out.WriteString(atomStyle.Render(atom.text))
		}
		rendered = append(rendered, padProjectedCellLine(out.String(), rowWidth, style))
	}
	for len(rendered) < height {
		rendered = append(rendered, "")
	}
	return strings.Join(rendered, "\n")
}

// renderProjectedCursorLine 渲染光标所在投影行。与 textarea 行为一致：文本
// 段与光标块各自成段（文本段背景由 CursorLine 提供，光标块按覆盖内容着色，
// 真实光标可见性由终端光标锚定负责）。
func (m appModel) renderProjectedCursorLine(line inputProjectionLine, rowWidth int) string {
	var out strings.Builder
	var text strings.Builder
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		out.WriteString(m.input.FocusedStyle.CursorLine.Render(text.String()))
		text.Reset()
	}
	for atomIndex := 0; atomIndex < len(line.atoms); atomIndex++ {
		atom := line.atoms[atomIndex]
		if atom.cursor {
			flushText()
			if m.input.Focused() {
				char := " "
				charStyle := m.input.FocusedStyle.CursorLine
				if atomIndex+1 < len(line.atoms) {
					next := line.atoms[atomIndex+1]
					char = next.text
					if next.token {
						charStyle = inputTokenStyleFor(next.kind)
					}
					atomIndex++
				}
				cursorModel := m.input.Cursor
				cursorModel.SetMode(cursor.CursorHide)
				cursorModel.SetChar(char)
				cursorModel.TextStyle = charStyle
				cursorModel.Style = charStyle
				out.WriteString(cursorModel.View())
			}
			continue
		}
		if atom.text == "" {
			continue
		}
		if atom.token {
			flushText()
			out.WriteString(inputTokenStyleFor(atom.kind).Render(atom.text))
			continue
		}
		text.WriteString(atom.text)
	}
	flushText()
	if width := terminalCellWidth(out.String()); width < rowWidth {
		out.WriteString(m.input.FocusedStyle.CursorLine.Render(strings.Repeat(" ", rowWidth-width)))
	} else if width > rowWidth {
		return cutStyledCellsExact(out.String(), 0, rowWidth)
	}
	return out.String()
}

// padProjectedCellLine 截断或补齐投影行到 rowWidth，padding 使用给定样式以
// 保持行尾背景与 textarea 一致。
func padProjectedCellLine(line string, rowWidth int, style lipgloss.Style) string {
	if width := terminalCellWidth(line); width < rowWidth {
		return line + style.Render(strings.Repeat(" ", rowWidth-width))
	} else if width > rowWidth {
		return cutStyledCellsExact(line, 0, rowWidth)
	}
	return line
}

func inputDraftEqual(a, b inputDraft) bool {
	if a.Text != b.Text || len(a.Tokens) != len(b.Tokens) {
		return false
	}
	for i := range a.Tokens {
		left, right := a.Tokens[i], b.Tokens[i]
		if left.Kind != right.Kind || left.Start != right.Start || left.End != right.End ||
			left.Label != right.Label || left.AutoSpace != right.AutoSpace {
			return false
		}
		if (left.Image == nil) != (right.Image == nil) {
			return false
		}
		if left.Image != nil && (left.Image.MIMEType != right.Image.MIMEType ||
			left.Image.Attachment != right.Image.Attachment || !bytes.Equal(left.Image.Data, right.Image.Data)) {
			return false
		}
	}
	return true
}
