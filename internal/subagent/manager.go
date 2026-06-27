package subagent

import (
	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/session"
	"codex-agent-go/internal/settings"
	"codex-agent-go/internal/skill"
	"codex-agent-go/internal/tokentracer"
	"codex-agent-go/internal/tool"
	toolexec "codex-agent-go/internal/tool/exec"
	toolfile "codex-agent-go/internal/tool/file"
	toolwebfetch "codex-agent-go/internal/tool/webfetch"
	"codex-agent-go/internal/ui"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

type ContextSink interface {
	SubmitSupplement(input string) bool
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
	Context  ContextSink
	Launcher Launcher
	Tracer   *tokentracer.Tracer

	Depth        int
	MaxDepth     int
	ParentTaskID string
}

type Manager struct {
	model        loop.ModelStreamer
	store        Store
	root         string
	settings     SettingsProvider
	notifier     Notifier
	contextSink  ContextSink
	launcher     Launcher
	tracer       *tokentracer.Tracer
	registry     taskRegistry
	depth        int
	maxDepth     int
	parentTaskID string

	startMu sync.Mutex
	mu      sync.RWMutex
	tasks   map[string]TaskSnapshot
	running map[string]Process
}

type Request struct {
	SessionID       string
	ParentSessionID string
	Prompt          string
	SystemPrompt    string
	Description     string
	ContextMode     settings.ContextMode
	RunMode         settings.RunMode
	DisableTools    bool
}

type Result struct {
	AgentID        string               `json:"agent_id"`
	AgentName      string               `json:"agent_name,omitempty"`
	AgentColor     string               `json:"agent_color,omitempty"`
	SessionID      string               `json:"session_id"`
	ContextMode    settings.ContextMode `json:"context_mode"`
	RunMode        settings.RunMode     `json:"run_mode"`
	TranscriptPath string               `json:"transcript_path"`
	OutputPath     string               `json:"output_path,omitempty"`
	ExitCode       *int                 `json:"exit_code,omitempty"`
	Depth          int                  `json:"depth"`
	ParentTaskID   string               `json:"parent_task_id,omitempty"`
	Content        string               `json:"content,omitempty"`
}

type Stream struct {
	Events         <-chan model.StreamEvent
	AgentID        string
	AgentName      string
	AgentColor     string
	SessionID      string
	TranscriptPath string
	OutputPath     string
}

type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskStopped   TaskStatus = "stopped"
)

type TaskSnapshot struct {
	ID              string               `json:"id"`
	Name            string               `json:"name,omitempty"`
	Color           string               `json:"color,omitempty"`
	SessionID       string               `json:"session_id"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	Description     string               `json:"description,omitempty"`
	Prompt          string               `json:"prompt"`
	SystemPrompt    string               `json:"system_prompt,omitempty"`
	DisableTools    bool                 `json:"disable_tools,omitempty"`
	ContextMode     settings.ContextMode `json:"context_mode"`
	RunMode         settings.RunMode     `json:"run_mode"`
	Status          TaskStatus           `json:"status"`
	TranscriptPath  string               `json:"transcript_path"`
	OutputPath      string               `json:"output_path,omitempty"`
	PID             int                  `json:"pid,omitempty"`
	ExitCode        *int                 `json:"exit_code,omitempty"`
	Depth           int                  `json:"depth"`
	ParentTaskID    string               `json:"parent_task_id,omitempty"`
	StartedAt       time.Time            `json:"started_at"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
	Content         string               `json:"content,omitempty"`
	Error           string               `json:"error,omitempty"`
	UsedTokens      int                  `json:"used_tokens,omitempty"`
	Usage           *tokentracer.Usage   `json:"usage,omitempty"`
}

type sinkUI struct{}

var _ ui.UI = sinkUI{}

func NewManager(cfg Config) *Manager {
	m := &Manager{
		model:        cfg.Model,
		store:        cfg.Store,
		root:         cfg.Root,
		settings:     cfg.Settings,
		notifier:     cfg.Notifier,
		contextSink:  cfg.Context,
		launcher:     cfg.Launcher,
		tracer:       cfg.Tracer,
		registry:     newTaskRegistry(cfg.Root),
		depth:        cfg.Depth,
		maxDepth:     cfg.MaxDepth,
		parentTaskID: strings.TrimSpace(cfg.ParentTaskID),
		tasks:        make(map[string]TaskSnapshot),
		running:      make(map[string]Process),
	}
	if m.maxDepth <= 0 {
		m.maxDepth = 4
	}
	if m.launcher == nil && m.model != nil {
		m.launcher = newInProcessLauncher(m.runWorkerInProcess)
	}
	return m
}

func (m *Manager) SetTokenTracer(tracer *tokentracer.Tracer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracer = tracer
}

func (m *Manager) Run(ctx context.Context, req Request) (Result, error) {
	req = m.normalizeRequest(req)
	req.RunMode = settings.RunModeSync
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	if err := m.validate(); err != nil {
		return Result{}, err
	}

	task, process, err := m.startTask(ctx, req)
	if err != nil {
		return Result{}, err
	}
	workerResult, waitErr := process.Wait()
	task = m.finishTask(ctx, task.ID, workerResult, waitErr)
	result := resultFromTask(task)
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

func (m *Manager) Stream(ctx context.Context, req Request) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	req = m.normalizeRequest(req)
	req.RunMode = settings.RunModeSync
	if err := validateRequest(req); err != nil {
		return Stream{}, err
	}
	if err := m.validateStreaming(); err != nil {
		return Stream{}, err
	}

	task, err := m.startStreamingTask(ctx, req)
	if err != nil {
		return Stream{}, err
	}

	events := make(chan model.StreamEvent)
	go m.runStreamingTask(ctx, task, events)

	return Stream{
		Events:         events,
		AgentID:        task.SessionID,
		AgentName:      task.Name,
		AgentColor:     task.Color,
		SessionID:      task.SessionID,
		TranscriptPath: task.TranscriptPath,
		OutputPath:     task.OutputPath,
	}, nil
}

func (m *Manager) Launch(ctx context.Context, req Request) (TaskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return TaskSnapshot{}, err
	}
	req = m.normalizeRequest(req)
	req.RunMode = settings.RunModeBackground
	if err := validateRequest(req); err != nil {
		return TaskSnapshot{}, err
	}
	if err := m.validate(); err != nil {
		return TaskSnapshot{}, err
	}

	task, process, err := m.startTask(context.Background(), req)
	if err != nil {
		return TaskSnapshot{}, err
	}
	go m.waitBackground(task.ID, process)
	return task, nil
}

func (m *Manager) Stop(ctx context.Context, id string) (TaskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return TaskSnapshot{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return TaskSnapshot{}, fmt.Errorf("subagent task id is required")
	}

	m.mu.RLock()
	process, ok := m.running[id]
	task, taskOK := m.tasks[id]
	m.mu.RUnlock()
	if !taskOK {
		var loadErr error
		task, taskOK, loadErr = m.registry.loadTask(ctx, id)
		if loadErr != nil {
			return TaskSnapshot{}, loadErr
		}
	}
	if !taskOK {
		return TaskSnapshot{}, fmt.Errorf("subagent task not found: %s", id)
	}
	if task.Status != TaskRunning {
		return task, fmt.Errorf("subagent task %s is not running (status=%s)", id, task.Status)
	}

	if ok && process != nil {
		_ = process.Stop()
	}

	now := time.Now().UTC()
	exitCode := -1
	task.Status = TaskStopped
	task.FinishedAt = &now
	task.ExitCode = &exitCode
	task.Error = "stopped"

	m.mu.Lock()
	m.tasks[id] = task
	delete(m.running, id)
	m.mu.Unlock()

	_ = m.registry.saveTask(ctx, task)
	_ = m.registry.saveOutput(ctx, id, WorkerResult{
		TaskID:    id,
		SessionID: task.SessionID,
		Error:     task.Error,
		ExitCode:  exitCode,
	})
	m.notifyTaskFinished(task)
	return task, nil
}

func (m *Manager) Status(id string) (TaskSnapshot, bool) {
	id = strings.TrimSpace(id)
	m.mu.RLock()
	task, ok := m.tasks[id]
	m.mu.RUnlock()
	if ok {
		return task, true
	}
	task, ok, err := m.registry.loadTask(context.Background(), id)
	if err != nil {
		return TaskSnapshot{}, false
	}
	return task, ok
}

func (m *Manager) ListTasks() []TaskSnapshot {
	m.mu.RLock()
	tasksByID := make(map[string]TaskSnapshot, len(m.tasks))
	for _, task := range m.tasks {
		tasksByID[task.ID] = task
	}
	m.mu.RUnlock()

	diskTasks, err := m.registry.listTasks(context.Background())
	if err == nil {
		for _, task := range diskTasks {
			if _, ok := tasksByID[task.ID]; !ok {
				tasksByID[task.ID] = task
			}
		}
	}

	tasks := make([]TaskSnapshot, 0, len(tasksByID))
	for _, task := range tasksByID {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.Before(tasks[j].StartedAt)
	})
	return tasks
}

// TotalSubagentTokens returns the sum of UsedTokens for all completed tasks
// whose ParentSessionID matches the given session ID.
func (m *Manager) TotalSubagentTokens(parentSessionID string) int {
	if m == nil || parentSessionID == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, task := range m.tasks {
		if task.ParentSessionID == parentSessionID {
			total += task.UsedTokens
		}
	}
	return total
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
	if m.launcher == nil {
		return fmt.Errorf("subagent launcher is nil")
	}
	if m.store == nil {
		return fmt.Errorf("subagent store is nil")
	}
	if strings.TrimSpace(m.root) == "" {
		return fmt.Errorf("subagent root is empty")
	}
	return nil
}

func (m *Manager) validateStreaming() error {
	if m == nil {
		return fmt.Errorf("subagent manager is nil")
	}
	if m.model == nil {
		return fmt.Errorf("subagent streaming model is nil")
	}
	if m.store == nil {
		return fmt.Errorf("subagent store is nil")
	}
	if strings.TrimSpace(m.root) == "" {
		return fmt.Errorf("subagent root is empty")
	}
	return nil
}

func (m *Manager) startTask(ctx context.Context, req Request) (TaskSnapshot, Process, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	depth := m.depth + 1
	if depth > m.maxDepth {
		return TaskSnapshot{}, nil, fmt.Errorf("subagent depth limit exceeded: %d > %d", depth, m.maxDepth)
	}

	persona, err := m.assignPersona(ctx)
	if err != nil {
		return TaskSnapshot{}, nil, err
	}

	sessionID, err := m.prepareSession(ctx, req)
	if err != nil {
		return TaskSnapshot{}, nil, err
	}

	task := TaskSnapshot{
		ID:              sessionID,
		Name:            persona.Name,
		Color:           persona.Color,
		SessionID:       sessionID,
		ParentSessionID: strings.TrimSpace(req.ParentSessionID),
		Description:     req.Description,
		Prompt:          req.Prompt,
		SystemPrompt:    strings.TrimSpace(req.SystemPrompt),
		DisableTools:    req.DisableTools,
		ContextMode:     req.ContextMode,
		RunMode:         req.RunMode,
		Status:          TaskRunning,
		TranscriptPath:  m.store.TranscriptPath(sessionID),
		OutputPath:      m.registry.outputPath(sessionID),
		Depth:           depth,
		ParentTaskID:    m.parentTaskID,
		StartedAt:       time.Now().UTC(),
	}
	workerReq := WorkerRequest{
		TaskID:          task.ID,
		SessionID:       task.SessionID,
		ParentSessionID: strings.TrimSpace(req.ParentSessionID),
		ParentTaskID:    task.ParentTaskID,
		Prompt:          req.Prompt,
		Description:     req.Description,
		DisableTools:    req.DisableTools,
		ContextMode:     req.ContextMode,
		RunMode:         req.RunMode,
		Depth:           task.Depth,
		MaxDepth:        m.maxDepth,
	}

	if err := m.registry.saveTask(ctx, task); err != nil {
		return TaskSnapshot{}, nil, err
	}
	m.setTask(task)

	process, err := m.launcher.Start(ctx, workerReq)
	if err != nil {
		task.Status = TaskFailed
		task.Error = err.Error()
		now := time.Now().UTC()
		exitCode := 1
		task.FinishedAt = &now
		task.ExitCode = &exitCode
		m.setTask(task)
		_ = m.registry.saveTask(ctx, task)
		_ = m.registry.saveOutput(ctx, task.ID, WorkerResult{
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Error:     task.Error,
			ExitCode:  exitCode,
		})
		return TaskSnapshot{}, nil, err
	}
	task.PID = process.PID()
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.running[task.ID] = process
	m.mu.Unlock()
	if err := m.registry.saveTask(ctx, task); err != nil {
		_ = process.Stop()
		now := time.Now().UTC()
		exitCode := 1
		task.Status = TaskFailed
		task.Error = err.Error()
		task.FinishedAt = &now
		task.ExitCode = &exitCode
		m.mu.Lock()
		m.tasks[task.ID] = task
		delete(m.running, task.ID)
		m.mu.Unlock()
		_ = m.registry.saveOutput(ctx, task.ID, WorkerResult{
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Error:     task.Error,
			ExitCode:  exitCode,
		})
		_ = m.registry.saveTask(ctx, task)
		return TaskSnapshot{}, nil, err
	}
	m.recordTaskStarted(task)
	return task, process, nil
}

func (m *Manager) startStreamingTask(ctx context.Context, req Request) (TaskSnapshot, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	depth := m.depth + 1
	if depth > m.maxDepth {
		return TaskSnapshot{}, fmt.Errorf("subagent depth limit exceeded: %d > %d", depth, m.maxDepth)
	}

	persona, err := m.assignPersona(ctx)
	if err != nil {
		return TaskSnapshot{}, err
	}

	sessionID, err := m.prepareSession(ctx, req)
	if err != nil {
		return TaskSnapshot{}, err
	}

	task := TaskSnapshot{
		ID:              sessionID,
		Name:            persona.Name,
		Color:           persona.Color,
		SessionID:       sessionID,
		ParentSessionID: strings.TrimSpace(req.ParentSessionID),
		Description:     req.Description,
		Prompt:          req.Prompt,
		SystemPrompt:    strings.TrimSpace(req.SystemPrompt),
		DisableTools:    req.DisableTools,
		ContextMode:     req.ContextMode,
		RunMode:         req.RunMode,
		Status:          TaskRunning,
		TranscriptPath:  m.store.TranscriptPath(sessionID),
		OutputPath:      m.registry.outputPath(sessionID),
		PID:             os.Getpid(),
		Depth:           depth,
		ParentTaskID:    m.parentTaskID,
		StartedAt:       time.Now().UTC(),
	}

	if err := m.registry.saveTask(ctx, task); err != nil {
		return TaskSnapshot{}, err
	}
	m.setTask(task)
	m.recordTaskStarted(task)
	return task, nil
}

func (m *Manager) runStreamingTask(ctx context.Context, task TaskSnapshot, events chan<- model.StreamEvent) {
	defer close(events)

	content, usedTokens, usage, done, err := m.runStreamingSession(ctx, task.SessionID, task.Prompt, task.SystemPrompt, task.DisableTools, events)
	result := WorkerResult{
		TaskID:     task.ID,
		SessionID:  task.SessionID,
		Content:    content,
		ExitCode:   0,
		UsedTokens: usedTokens,
		Usage:      usage,
	}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		if !done {
			_ = sendModelStreamEvent(context.Background(), events, model.StreamEvent{Err: err})
		}
	}
	m.finishTask(context.Background(), task.ID, result, err)
}

func (m *Manager) waitBackground(taskID string, process Process) {
	result, err := process.Wait()
	task := m.finishTask(context.Background(), taskID, result, err)
	m.notifyTaskFinished(task)
}

func (m *Manager) finishTask(ctx context.Context, taskID string, result WorkerResult, err error) TaskSnapshot {
	now := time.Now().UTC()
	m.mu.Lock()
	task := m.tasks[taskID]
	if task.ID == "" {
		task.ID = taskID
		task.SessionID = result.SessionID
		task.OutputPath = m.registry.outputPath(taskID)
	}
	if task.Status != TaskRunning {
		delete(m.running, taskID)
		m.mu.Unlock()
		return task
	}
	exitCode := result.ExitCode
	if exitCode == 0 && err != nil {
		exitCode = 1
	}
	task.FinishedAt = &now
	task.ExitCode = &exitCode
	if result.Content != "" {
		task.Content = strings.TrimSpace(result.Content)
	}
	if result.UsedTokens > 0 {
		task.UsedTokens = result.UsedTokens
	}
	if usage := normalizedUsage(result.Usage); usage != nil {
		task.Usage = usage
		if task.UsedTokens == 0 {
			task.UsedTokens = usageTokenTotal(*usage)
		}
	}
	if err != nil || result.Error != "" {
		task.Status = TaskFailed
		task.Error = strings.TrimSpace(result.Error)
		if task.Error == "" && err != nil {
			task.Error = err.Error()
		}
	} else {
		task.Status = TaskCompleted
	}
	m.tasks[taskID] = task
	delete(m.running, taskID)
	m.mu.Unlock()

	_ = m.registry.saveOutput(ctx, taskID, result)
	_ = m.registry.saveTask(ctx, task)
	m.submitTaskContext(task)
	m.recordTaskFinished(task)
	return task
}

func (m *Manager) prepareSession(ctx context.Context, req Request) (string, error) {
	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		if err := validateRequestedSessionID(sessionID); err != nil {
			return "", err
		}
		exists, err := m.store.Exists(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if exists {
			return sessionID, nil
		}
		switch req.ContextMode {
		case settings.ContextModeFork:
			parentID := strings.TrimSpace(req.ParentSessionID)
			if err := m.ensureSessionExists(ctx, parentID); err != nil {
				return "", err
			}
			if _, err := m.store.Fork(ctx, session.ForkRequest{
				SessionID:       sessionID,
				ParentSessionID: parentID,
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
	sessionID, err := session.GenerateSessionID()
	if err != nil {
		return "", err
	}
	switch req.ContextMode {
	case settings.ContextModeFork:
		parentID := strings.TrimSpace(req.ParentSessionID)
		if err := m.ensureSessionExists(ctx, parentID); err != nil {
			return "", err
		}
		if _, err := m.store.Fork(ctx, session.ForkRequest{
			SessionID:       sessionID,
			ParentSessionID: parentID,
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

func validateRequestedSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if strings.Contains(sessionID, "..") || strings.ContainsAny(sessionID, `/\`) {
		return fmt.Errorf("invalid requested session id: %s", sessionID)
	}
	return nil
}

func (m *Manager) ensureSessionExists(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("parent session id is required")
	}
	exists, err := m.store.Exists(ctx, sessionID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := m.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
		return err
	}
	return nil
}

func (m *Manager) runSession(ctx context.Context, sessionID, prompt string, disableTools bool) (string, int, *tokentracer.Usage, error) {
	usageUI := &usageSinkUI{}
	runner := loop.NewRunnerWithInstructionRoot(m.model, usageUI, m.toolRegistry(disableTools), m.store, sessionID, m.root)
	msg, err := runner.RunTurn(ctx, prompt)
	usedTokens := runner.ContextStats(1<<30, "").UsedTokens
	usage := usageUI.Usage()
	if err != nil {
		return "", usedTokens, usage, err
	}
	return strings.TrimSpace(msg.Content), usedTokens, usage, nil
}

func (m *Manager) runStreamingSession(ctx context.Context, sessionID, prompt, systemPrompt string, disableTools bool, events chan<- model.StreamEvent) (string, int, *tokentracer.Usage, bool, error) {
	streamUI := &streamingUI{ctx: ctx, events: events}
	runner := loop.NewRunnerWithInstructionRoot(m.model, streamUI, m.toolRegistry(disableTools), m.store, sessionID, m.root)
	runner.SetSystemSupplement(systemPrompt)
	if isStreamMASystemPrompt(systemPrompt) {
		runner.SetCompactToolPrompt(true)
	}
	msg, err := runner.RunTurn(ctx, prompt)
	usedTokens := runner.ContextStats(1<<30, "").UsedTokens
	usage := streamUI.Usage()
	done := streamUI.Done()
	if err != nil {
		return streamUI.Content(), usedTokens, usage, done, err
	}
	return strings.TrimSpace(msg.Content), usedTokens, usage, done, nil
}

func isStreamMASystemPrompt(systemPrompt string) bool {
	return strings.Contains(systemPrompt, "streamma_agent_id=")
}

func (m *Manager) runWorkerInProcess(ctx context.Context, req WorkerRequest) (WorkerResult, error) {
	content, usedTokens, usage, err := m.runSession(ctx, req.SessionID, req.Prompt, req.DisableTools)
	result := WorkerResult{
		TaskID:     req.TaskID,
		SessionID:  req.SessionID,
		Content:    content,
		ExitCode:   0,
		UsedTokens: usedTokens,
		Usage:      usage,
	}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		return result, err
	}
	return result, nil
}

func (m *Manager) setTask(task TaskSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[task.ID] = task
}

func (m *Manager) currentTokenTracer() *tokentracer.Tracer {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tracer
}

func (m *Manager) recordTaskStarted(task TaskSnapshot) {
	tracer := m.currentTokenTracer()
	if tracer == nil {
		return
	}
	tracer.RecordEvent("subagent_task_start", map[string]any{
		"task_id":           task.ID,
		"name":              task.Name,
		"session_id":        task.SessionID,
		"parent_session_id": task.ParentSessionID,
		"description":       task.Description,
		"context_mode":      task.ContextMode,
		"run_mode":          task.RunMode,
		"pid":               task.PID,
		"depth":             task.Depth,
		"started_at":        task.StartedAt.Format(time.RFC3339Nano),
	})
}

func (m *Manager) recordTaskFinished(task TaskSnapshot) {
	tracer := m.currentTokenTracer()
	if tracer == nil {
		return
	}
	data := map[string]any{
		"task_id":           task.ID,
		"name":              task.Name,
		"session_id":        task.SessionID,
		"parent_session_id": task.ParentSessionID,
		"description":       task.Description,
		"status":            task.Status,
		"run_mode":          task.RunMode,
		"used_tokens":       task.UsedTokens,
		"exit_code":         task.ExitCode,
	}
	if task.FinishedAt != nil {
		data["finished_at"] = task.FinishedAt.Format(time.RFC3339Nano)
	}
	if task.Error != "" {
		data["error"] = task.Error
	}
	if usage := normalizedUsage(task.Usage); usage != nil {
		data["usage"] = *usage
	}
	tracer.RecordEvent("subagent_task_end", data)
}

func normalizedUsage(usage *tokentracer.Usage) *tokentracer.Usage {
	if usage == nil {
		return nil
	}
	normalized := usage.Normalized()
	if normalized.Empty() {
		return nil
	}
	return &normalized
}

func usageTokenTotal(usage tokentracer.Usage) int {
	usage = usage.Normalized()
	return usage.Input + usage.CacheRead + usage.CacheCreation + usage.Output
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

const parentContextResultMaxRunes = 6000

func (m *Manager) submitTaskContext(task TaskSnapshot) {
	if m == nil || m.contextSink == nil || task.RunMode != settings.RunModeBackground {
		return
	}
	if strings.TrimSpace(task.ParentSessionID) == "" {
		return
	}
	update := renderTaskContextUpdate(task)
	if strings.TrimSpace(update) == "" {
		return
	}
	_ = m.contextSink.SubmitSupplement(update)
}

func renderTaskContextUpdate(task TaskSnapshot) string {
	var lines []string
	lines = append(lines, "Background subagent completed.")
	lines = append(lines, "id: "+task.ID)
	if task.Name != "" {
		lines = append(lines, "name: "+task.Name)
	}
	if task.Description != "" {
		lines = append(lines, "description: "+task.Description)
	}
	lines = append(lines, "status: "+string(task.Status))
	lines = append(lines, "context_mode: "+string(task.ContextMode))
	lines = append(lines, "run_mode: "+string(task.RunMode))
	if task.TranscriptPath != "" {
		lines = append(lines, "transcript: "+task.TranscriptPath)
	}
	if task.OutputPath != "" {
		lines = append(lines, "output: "+task.OutputPath)
	}
	if task.Error != "" {
		lines = append(lines, "error: "+task.Error)
	}
	content := strings.TrimSpace(task.Content)
	if content != "" {
		lines = append(lines, "result:")
		lines = append(lines, truncateForParentContext(content, parentContextResultMaxRunes, task.OutputPath))
	}
	return strings.Join(lines, "\n")
}

func truncateForParentContext(content string, limit int, outputPath string) string {
	if limit <= 0 {
		limit = parentContextResultMaxRunes
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	suffix := "\n[truncated]"
	if outputPath != "" {
		suffix = "\n[truncated; full result: " + outputPath + "]"
	}
	return string(runes[:limit]) + suffix
}

func resultFromTask(task TaskSnapshot) Result {
	return Result{
		AgentID:        task.SessionID,
		AgentName:      task.Name,
		AgentColor:     task.Color,
		SessionID:      task.SessionID,
		ContextMode:    task.ContextMode,
		RunMode:        task.RunMode,
		TranscriptPath: task.TranscriptPath,
		OutputPath:     task.OutputPath,
		ExitCode:       task.ExitCode,
		Depth:          task.Depth,
		ParentTaskID:   task.ParentTaskID,
		Content:        task.Content,
	}
}

func (m *Manager) toolRegistry(disableTools bool) *tool.Registry {
	if disableTools {
		return tool.NewRegistry()
	}
	root := ""
	if m != nil {
		root = m.root
	}
	return newBaseToolRegistry(root)
}

func newBaseToolRegistry(root string) *tool.Registry {
	registry := tool.NewRegistry()
	readRoots := skill.DefaultRoots(root)
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
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

type streamingUI struct {
	ctx    context.Context
	events chan<- model.StreamEvent

	mu      sync.RWMutex
	done    bool
	content strings.Builder
	usage   *tokentracer.Usage
}

type usageSinkUI struct {
	sinkUI

	mu    sync.RWMutex
	usage *tokentracer.Usage
}

func (u *streamingUI) OnAssistantDelta(text string) error {
	if text == "" {
		return nil
	}
	u.mu.Lock()
	u.content.WriteString(text)
	u.mu.Unlock()
	return sendModelStreamEvent(u.ctx, u.events, model.StreamEvent{Delta: text})
}

func (u *streamingUI) OnToolCall(ui.ToolCallEvent) error {
	return nil
}

func (u *streamingUI) OnToolResult(ui.ToolResultEvent) error {
	return nil
}

func (u *streamingUI) OnDone() error {
	u.mu.Lock()
	u.done = true
	u.mu.Unlock()
	return sendModelStreamEvent(u.ctx, u.events, model.StreamEvent{Done: true})
}

func (u *streamingUI) OnModelUsage(usage model.Usage) {
	copied := usage
	structured := tokentracer.UsageFromModelUsage(usage)
	if !structured.Empty() {
		u.mu.Lock()
		u.usage = &structured
		u.mu.Unlock()
	}
	_ = sendModelStreamEvent(u.ctx, u.events, model.StreamEvent{Usage: &copied})
}

func (u *streamingUI) Done() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.done
}

func (u *streamingUI) Content() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return strings.TrimSpace(u.content.String())
}

func (u *streamingUI) Usage() *tokentracer.Usage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.usage == nil {
		return nil
	}
	copied := *u.usage
	return &copied
}

func (u *usageSinkUI) OnModelUsage(usage model.Usage) {
	structured := tokentracer.UsageFromModelUsage(usage)
	if structured.Empty() {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.usage = &structured
}

func (u *usageSinkUI) Usage() *tokentracer.Usage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.usage == nil {
		return nil
	}
	copied := *u.usage
	return &copied
}

func sendModelStreamEvent(ctx context.Context, events chan<- model.StreamEvent, event model.StreamEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type subagentTool struct {
	manager         *Manager
	parentSessionID string
}

type statusTool struct {
	manager *Manager
}

type stopTool struct {
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

type stopInput struct {
	ID string `json:"id"`
}

func NewTool(manager *Manager, parentSessionID string) tool.Tool {
	return &subagentTool{manager: manager, parentSessionID: parentSessionID}
}

func NewStatusTool(manager *Manager) tool.Tool {
	return &statusTool{manager: manager}
}

func NewStopTool(manager *Manager) tool.Tool {
	return &stopTool{manager: manager}
}

func (t *subagentTool) Name() string {
	return "Subagent"
}

func (t *subagentTool) Description() string {
	return "Launch a focused subagent. Use context_mode empty for a self-contained spec, or fork to inherit committed parent session history. run_mode defaults to background and returns a task id immediately; explicit sync waits for the result."
}

func (t *subagentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"description":{"type":"string"},"context_mode":{"type":"string","enum":["empty","fork"]},"run_mode":{"type":"string","enum":["sync","background"],"default":"background"}},"required":["prompt"]}`)
}

func (t *subagentTool) buildRequest(raw json.RawMessage) (Request, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, err
	}
	req := Request{
		ParentSessionID: t.parentSessionID,
		Prompt:          in.Prompt,
		Description:     in.Description,
		ContextMode:     in.ContextMode,
		RunMode:         in.RunMode,
	}
	if req.RunMode == "" {
		req.RunMode = settings.RunModeBackground
	}
	req = t.manager.normalizeRequest(req)
	return req, nil
}

func (t *subagentTool) IsConcurrencySafe(raw json.RawMessage) bool {
	req, err := t.buildRequest(raw)
	return err == nil && req.RunMode == settings.RunModeBackground
}

func (t *subagentTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	req, err := t.buildRequest(raw)
	if err != nil {
		return "", err
	}
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

func (t *statusTool) IsConcurrencySafe(json.RawMessage) bool {
	return t != nil && t.manager != nil
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

func (t *stopTool) Name() string {
	return "SubagentStop"
}

func (t *stopTool) Description() string {
	return "Stop a running background subagent task by id."
}

func (t *stopTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
}

func (t *stopTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in stopInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	task, err := t.manager.Stop(ctx, in.ID)
	if err != nil {
		return marshalResult(task), err
	}
	return marshalResult(task), nil
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
