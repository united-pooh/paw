package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/loop"
	"gocode/internal/session"
	"gocode/internal/settings"
	"gocode/internal/tool"
	toolexec "gocode/internal/tool/exec"
	toolfile "gocode/internal/tool/file"
	toolwebfetch "gocode/internal/tool/webfetch"
	"gocode/internal/ui"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type SettingsProvider interface {
	CurrentSettings() settings.Config
}

type Notifier interface {
	OnSystemMessage(event ui.SystemEvent) error
}

type Store interface {
	session.Store
	TranscriptPath(sessionID string) string
}

type Config struct {
	Model    loop.ModelStreamer
	Store    Store
	Root     string
	Settings SettingsProvider
	Notifier Notifier
}

type Manager struct {
	model    loop.ModelStreamer
	store    Store
	root     string
	settings SettingsProvider
	notifier Notifier

	mu    sync.RWMutex
	tasks map[string]TaskSnapshot
}

type Request struct {
	ParentSessionID string
	Prompt          string
	Description     string
	ContextMode     settings.ContextMode
	RunMode         settings.RunMode
}

type Result struct {
	AgentID        string               `json:"agent_id"`
	SessionID      string               `json:"session_id"`
	ContextMode    settings.ContextMode `json:"context_mode"`
	RunMode        settings.RunMode     `json:"run_mode"`
	TranscriptPath string               `json:"transcript_path"`
	Content        string               `json:"content,omitempty"`
}

type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

type TaskSnapshot struct {
	ID             string               `json:"id"`
	SessionID      string               `json:"session_id"`
	Description    string               `json:"description,omitempty"`
	Prompt         string               `json:"prompt"`
	ContextMode    settings.ContextMode `json:"context_mode"`
	RunMode        settings.RunMode     `json:"run_mode"`
	Status         TaskStatus           `json:"status"`
	TranscriptPath string               `json:"transcript_path"`
	StartedAt      time.Time            `json:"started_at"`
	FinishedAt     *time.Time           `json:"finished_at,omitempty"`
	Content        string               `json:"content,omitempty"`
	Error          string               `json:"error,omitempty"`
}

type sinkUI struct{}

var _ ui.UI = sinkUI{}

func NewManager(cfg Config) *Manager {
	return &Manager{
		model:    cfg.Model,
		store:    cfg.Store,
		root:     cfg.Root,
		settings: cfg.Settings,
		notifier: cfg.Notifier,
		tasks:    make(map[string]TaskSnapshot),
	}
}

func (m *Manager) Run(ctx context.Context, req Request) (Result, error) {
	req = m.normalizeRequest(req)
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	if err := m.validate(); err != nil {
		return Result{}, err
	}

	sessionID, err := m.prepareSession(ctx, req)
	if err != nil {
		return Result{}, err
	}
	content, err := m.runSession(ctx, sessionID, req.Prompt)
	result := Result{
		AgentID:        sessionID,
		SessionID:      sessionID,
		ContextMode:    req.ContextMode,
		RunMode:        settings.RunModeSync,
		TranscriptPath: m.store.TranscriptPath(sessionID),
		Content:        content,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (m *Manager) Launch(ctx context.Context, req Request) (TaskSnapshot, error) {
	req = m.normalizeRequest(req)
	req.RunMode = settings.RunModeBackground
	if err := validateRequest(req); err != nil {
		return TaskSnapshot{}, err
	}
	if err := m.validate(); err != nil {
		return TaskSnapshot{}, err
	}

	sessionID, err := m.prepareSession(ctx, req)
	if err != nil {
		return TaskSnapshot{}, err
	}

	task := TaskSnapshot{
		ID:             sessionID,
		SessionID:      sessionID,
		Description:    req.Description,
		Prompt:         req.Prompt,
		ContextMode:    req.ContextMode,
		RunMode:        settings.RunModeBackground,
		Status:         TaskRunning,
		TranscriptPath: m.store.TranscriptPath(sessionID),
		StartedAt:      time.Now().UTC(),
	}
	m.setTask(task)

	go m.runBackground(sessionID, req)
	return task, nil
}

func (m *Manager) Status(id string) (TaskSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[strings.TrimSpace(id)]
	return task, ok
}

func (m *Manager) ListTasks() []TaskSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tasks := make([]TaskSnapshot, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.Before(tasks[j].StartedAt)
	})
	return tasks
}

func (m *Manager) normalizeRequest(req Request) Request {
	cfg := settings.DefaultConfig()
	if m != nil && m.settings != nil {
		cfg = m.settings.CurrentSettings()
	}
	if req.ContextMode == "" {
		req.ContextMode = cfg.Subagent.DefaultContextMode
	}
	if req.RunMode == "" {
		req.RunMode = cfg.Subagent.DefaultRunMode
	}
	req.ContextMode = settings.NormalizeContextMode(req.ContextMode)
	req.RunMode = settings.NormalizeRunMode(req.RunMode)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Description = strings.TrimSpace(req.Description)
	return req
}

func validateRequest(req Request) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if req.ContextMode == settings.ContextModeFork && strings.TrimSpace(req.ParentSessionID) == "" {
		return fmt.Errorf("parent_session_id is required for fork context")
	}
	return nil
}

func (m *Manager) validate() error {
	if m == nil {
		return fmt.Errorf("subagent manager is nil")
	}
	if m.model == nil {
		return fmt.Errorf("subagent model is nil")
	}
	if m.store == nil {
		return fmt.Errorf("subagent store is nil")
	}
	if strings.TrimSpace(m.root) == "" {
		return fmt.Errorf("subagent root is empty")
	}
	return nil
}

func (m *Manager) prepareSession(ctx context.Context, req Request) (string, error) {
	sessionID, err := session.GenerateSessionID()
	if err != nil {
		return "", err
	}
	switch req.ContextMode {
	case settings.ContextModeFork:
		if _, err := m.store.Fork(ctx, session.ForkRequest{
			SessionID:        sessionID,
			ParentSessionID: strings.TrimSpace(req.ParentSessionID),
			ForkFromSeq:     -1,
		}); err != nil {
			return "", err
		}
	default:
		if _, err := m.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
			return "", err
		}
	}
	return sessionID, nil
}

func (m *Manager) runSession(ctx context.Context, sessionID, prompt string) (string, error) {
	runner := loop.NewRunnerWithInstructionRoot(m.model, sinkUI{}, newBaseToolRegistry(m.root), m.store, sessionID, m.root)
	msg, err := runner.RunTurn(ctx, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(msg.Content), nil
}

func (m *Manager) runBackground(sessionID string, req Request) {
	ctx := context.Background()
	content, err := m.runSession(ctx, sessionID, req.Prompt)
	now := time.Now().UTC()

	m.mu.Lock()
	task := m.tasks[sessionID]
	task.FinishedAt = &now
	if err != nil {
		task.Status = TaskFailed
		task.Error = err.Error()
	} else {
		task.Status = TaskCompleted
		task.Content = content
	}
	m.tasks[sessionID] = task
	m.mu.Unlock()

	m.notifyTaskFinished(task)
}

func (m *Manager) setTask(task TaskSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[task.ID] = task
}

func (m *Manager) notifyTaskFinished(task TaskSnapshot) {
	if m == nil || m.notifier == nil {
		return
	}
	status := string(task.Status)
	body := fmt.Sprintf("%s finished with status=%s", shortID(task.ID), status)
	if task.Error != "" {
		body += ": " + task.Error
	} else if task.Content != "" {
		body += ": " + summarize(task.Content)
	}
	_ = m.notifier.OnSystemMessage(ui.SystemEvent{
		Title: "subagent",
		Body:  body,
	})
}

func newBaseToolRegistry(root string) *tool.Registry {
	registry := tool.NewRegistry()
	registry.Register(&toolfile.LSTool{Root: root})
	registry.Register(&toolfile.ReadTool{Root: root})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root})
	registry.Register(&toolfile.GlobTool{Root: root})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
	return registry
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func summarize(text string) string {
	fields := strings.Join(strings.Fields(text), " ")
	runes := []rune(fields)
	if len(runes) <= 160 {
		return fields
	}
	return string(runes[:160]) + "..."
}

func (sinkUI) OnAssistantDelta(string) error {
	return nil
}

func (sinkUI) OnToolCall(ui.ToolCallEvent) error {
	return nil
}

func (sinkUI) OnToolResult(ui.ToolResultEvent) error {
	return nil
}

func (sinkUI) OnDone() error {
	return nil
}

type subagentTool struct {
	manager         *Manager
	parentSessionID string
}

type statusTool struct {
	manager *Manager
}

type toolInput struct {
	Prompt      string               `json:"prompt"`
	Description string               `json:"description,omitempty"`
	ContextMode settings.ContextMode `json:"context_mode,omitempty"`
	RunMode     settings.RunMode     `json:"run_mode,omitempty"`
}

type statusInput struct {
	ID string `json:"id,omitempty"`
}

func NewTool(manager *Manager, parentSessionID string) tool.Tool {
	return &subagentTool{manager: manager, parentSessionID: parentSessionID}
}

func NewStatusTool(manager *Manager) tool.Tool {
	return &statusTool{manager: manager}
}

func (t *subagentTool) Name() string {
	return "Subagent"
}

func (t *subagentTool) Description() string {
	return "Launch a focused subagent. Use context_mode empty for a self-contained spec, or fork to inherit committed parent session history. run_mode sync waits for the result; background returns a task id."
}

func (t *subagentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"description":{"type":"string"},"context_mode":{"type":"string","enum":["empty","fork"]},"run_mode":{"type":"string","enum":["sync","background"]}},"required":["prompt"]}`)
}

func (t *subagentTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	req := Request{
		ParentSessionID: t.parentSessionID,
		Prompt:          in.Prompt,
		Description:     in.Description,
		ContextMode:     in.ContextMode,
		RunMode:         in.RunMode,
	}
	req = t.manager.normalizeRequest(req)
	if req.RunMode == settings.RunModeBackground {
		task, err := t.manager.Launch(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalResult(task), nil
	}
	result, err := t.manager.Run(ctx, req)
	if err != nil {
		return "", err
	}
	return marshalResult(result), nil
}

func (t *statusTool) Name() string {
	return "SubagentStatus"
}

func (t *statusTool) Description() string {
	return "List background subagent tasks or get one task by id."
}

func (t *statusTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
}

func (t *statusTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in statusInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(in.ID) != "" {
		task, ok := t.manager.Status(in.ID)
		if !ok {
			return "", fmt.Errorf("subagent task not found: %s", in.ID)
		}
		return marshalResult(task), nil
	}
	return marshalResult(t.manager.ListTasks()), nil
}

func marshalResult(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func TranscriptPath(root, sessionID string) string {
	return filepath.Join(root, ".ccagent", "sessions", sessionID, "transcript.jsonl")
}
