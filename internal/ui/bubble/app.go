package bubble

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

func newModel(ctx context.Context, runner Runner, sessionID string, controller ModelConfigController, anchor *terminalCursorAnchor) appModel {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = ">"
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.CharLimit = 0
	input.MaxHeight = inputMaxVisibleLines
	input.SetWidth(72)
	input.SetHeight(1)
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j", "alt+enter", "shift+enter"),
		key.WithHelp("ctrl+j", "newline"),
	)
	input.Cursor.SetMode(cursor.CursorStatic)
	input.Focus()

	vp := viewport.New(80, 20)
	model := appModel{
		ctx:             ctx,
		runner:          runner,
		sessionID:       sessionID,
		modelConfig:     controller,
		cursorAnchor:    anchor,
		input:           input,
		viewport:        vp,
		cursorFrameAt:   time.Now(),
		activeAssistant: -1,
		historyIndex:    -1,
		transcript: []transcriptEntry{
			{
				kind:  entrySystem,
				title: "system",
				body:  "Interactive mode is running on Bubble Tea. Use /help for commands.",
			},
		},
	}
	model.applyCursorAnimation()
	model.refreshViewport()
	return model
}

func (m appModel) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), cursorFrameTick())
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ready = true
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		m.refreshViewport()
		return m, nil
	case cursorFrameMsg:
		m.cursorFrameAt = time.Time(msg)
		m.applyCursorAnimation()
		return m, cursorFrameTick()
	case assistantDeltaMsg:
		m.appendAssistantDelta(string(msg))
		return m, nil
	case toolCallMsg:
		m.activeAssistant = -1
		m.addEntry(transcriptEntry{
			kind:  entryTool,
			title: "tool",
			body:  fmt.Sprintf("%s %s", msg.Name, prettyJSON(json.RawMessage(msg.Input))),
		})
		return m, nil
	case toolResultMsg:
		m.activeAssistant = -1
		status := "ok"
		if msg.IsError {
			status = "error"
		}
		body := fmt.Sprintf("%s %s", msg.Name, status)
		if preview := summarizeToolContent(msg.Content); preview != "" {
			body = fmt.Sprintf("%s: %s", body, preview)
		}
		m.addEntry(transcriptEntry{
			kind:  entryTool,
			title: "result",
			body:  body,
		})
		return m, nil
	case doneMsg:
		m.activeAssistant = -1
		m.refreshViewport()
		return m, nil
	case turnFinishedMsg:
		m.running = false
		m.runningTerminal = false
		if msg.err != nil {
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: "error",
				body:  msg.err.Error(),
			})
		}
		cmds = append(cmds, m.input.Focus())
	case shellFinishedMsg:
		m.running = false
		m.runningTerminal = false
		kind := entryTool
		if msg.err != nil {
			kind = entryError
		}
		m.addEntry(transcriptEntry{
			kind:  kind,
			title: "terminal",
			body:  shellResultBody(msg),
		})
		cmds = append(cmds, m.input.Focus())
	case tea.KeyMsg:
		if m.modelWizard != nil {
			return m.handleModelWizardKey(msg)
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "up":
			return m.handleInputVerticalNavigation(-1)
		case "down":
			return m.handleInputVerticalNavigation(1)
		case "enter":
			return m.handleSubmit()
		}
	}

	var viewportCmd tea.Cmd
	m.viewport, viewportCmd = m.viewport.Update(msg)
	cmds = append(cmds, viewportCmd)

	if !m.running {
		var inputCmd tea.Cmd
		m.input, inputCmd = m.input.Update(msg)
		if isTextEditingKey(msg) {
			m.resetHistoryNavigation()
		}
		m.syncInputMode()
		m.relayout()
		cmds = append(cmds, inputCmd)
	}
	m.applyCursorAnimation()

	return m, tea.Batch(cmds...)
}
