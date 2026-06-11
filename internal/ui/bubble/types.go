package bubble

import (
	"context"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"gocode/internal/model"
	"gocode/internal/ui"
	"time"
)

type entryKind int

const (
	entrySystem entryKind = iota
	entryUser
	entryAssistant
	entryTool
	entryError
)

type transcriptEntry struct {
	kind  entryKind
	title string
	body  string
}

type assistantDeltaMsg string
type toolCallMsg ui.ToolCallEvent
type toolResultMsg ui.ToolResultEvent
type doneMsg struct{}
type turnFinishedMsg struct{ err error }
type shellFinishedMsg struct {
	command string
	stdout  string
	stderr  string
	err     error
}
type cursorFrameMsg time.Time

type modelWizardStep int

const (
	modelWizardProvider modelWizardStep = iota
	modelWizardConfirm
)

type modelProviderOption struct {
	id          string
	label       string
	description string
}

var modelProviderOptions = []modelProviderOption{
	{
		id:          model.ProviderCustom,
		label:       "custom",
		description: "Local or custom OpenAI-compatible endpoint",
	},
	{
		id:          model.ProviderDeepSeek,
		label:       "deepseek",
		description: "DeepSeek official OpenAI-compatible endpoint",
	},
}

type modelWizard struct {
	step          modelWizardStep
	selectedIndex int
	err           string
}

func newModelWizard(current model.Config) *modelWizard {
	selected := 0
	for i, option := range modelProviderOptions {
		if option.id == current.Provider {
			selected = i
			break
		}
	}
	return &modelWizard{step: modelWizardProvider, selectedIndex: selected}
}

func (w *modelWizard) selectedProvider() modelProviderOption {
	if w == nil || len(modelProviderOptions) == 0 {
		return modelProviderOption{}
	}
	if w.selectedIndex < 0 {
		w.selectedIndex = 0
	}
	if w.selectedIndex >= len(modelProviderOptions) {
		w.selectedIndex = len(modelProviderOptions) - 1
	}
	return modelProviderOptions[w.selectedIndex]
}

type appModel struct {
	ctx             context.Context
	runner          Runner
	sessionID       string
	modelConfig     ModelConfigController
	cursorAnchor    *terminalCursorAnchor
	input           textarea.Model
	viewport        viewport.Model
	width           int
	height          int
	ready           bool
	running         bool
	runningTerminal bool
	terminalMode    bool
	terminalPreview bool
	cursorFrameAt   time.Time
	pending         []string
	inputHistory    []string
	historyIndex    int
	historyDraft    string
	historyDownLock bool
	transcript      []transcriptEntry
	activeAssistant int
	modelWizard     *modelWizard
}
