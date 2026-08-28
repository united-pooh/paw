package task

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/sessionactor"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	toolwebfetch "paw/internal/tool/webfetch"
	"paw/internal/ui"
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
	MCPBroker    coremcp.Broker
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
	mcpBroker    coremcp.Broker
	actors       *taskActorHost

	startMu sync.Mutex
	mu      sync.RWMutex
	close   sync.Once
	workers sync.WaitGroup

	lastMCPSnapshot uint64
	mcpSnapshotSet  bool
	mcpStop         func()
}

type Request struct {
	SessionID       string
	ParentSessionID string
	ParentTurnID    string
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

const (
	streamEventBufferSize = 64
)

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
	// TaskInterrupted 是孤儿回收产生的终态：任务曾以 running 写盘，但宿主
	// 进程退出后其 worker 进程已不存在（异常退出路径不会执行 finishTask）。
	// 语义区别于 TaskFailed（任务自身出错）与 TaskStopped（用户主动停止）。
	TaskInterrupted TaskStatus = "interrupted"
	// TaskNotFound 标记 TaskWait/TaskStatus 中请求了未知任务 id。
	// 它不参与终态判断，仅用于宽容地告知调用方该 id 不存在。
	TaskNotFound TaskStatus = "not_found"
)

type TaskSnapshot struct {
	ID              string               `json:"id"`
	Name            string               `json:"name,omitempty"`
	Color           string               `json:"color,omitempty"`
	SessionID       string               `json:"session_id"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	ParentTurnID    string               `json:"parent_turn_id,omitempty"`
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

// TaskSummary 是面向模型/工具的轻量任务摘要：绝不包含 content、prompt、
// system_prompt、usage 等大字段。需要完整结果时用 Read 读取 output_path。
type TaskSummary struct {
	ID             string     `json:"id"`
	Name           string     `json:"name,omitempty"`
	Status         TaskStatus `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Depth          int        `json:"depth"`
	ParentTaskID   string     `json:"parent_task_id,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	Error          string     `json:"error,omitempty"`
	OutputPath     string     `json:"output_path,omitempty"`
	TranscriptPath string     `json:"transcript_path"`
	NotFound       bool       `json:"not_found,omitempty"`
}

// WaitResult 是 TaskWait 的返回体。AnyTerminal 仅供内部判断使用，
// 不进入 JSON 输出。
type WaitResult struct {
	TimedOut    bool          `json:"timed_out"`
	Tasks       []TaskSummary `json:"tasks"`
	AnyTerminal bool          `json:"-"`
}

// summarizeTask 把 TaskSnapshot 折叠为 TaskSummary，永不携带大字段。
func summarizeTask(task TaskSnapshot) TaskSummary {
	summary := TaskSummary{
		ID:             task.ID,
		Name:           task.Name,
		Status:         task.Status,
		StartedAt:      task.StartedAt,
		FinishedAt:     task.FinishedAt,
		Depth:          task.Depth,
		ParentTaskID:   task.ParentTaskID,
		ExitCode:       task.ExitCode,
		Error:          task.Error,
		OutputPath:     task.OutputPath,
		TranscriptPath: task.TranscriptPath,
	}
	if task.Status == TaskNotFound {
		summary.NotFound = true
	}
	return summary
}

type sinkUI struct{}

var _ ui.UI = sinkUI{}

func NewManager(cfg Config) *Manager {
	registry := newTaskRegistry(cfg.Root)
	m := &Manager{
		model:        cfg.Model,
		store:        cfg.Store,
		root:         cfg.Root,
		settings:     cfg.Settings,
		notifier:     cfg.Notifier,
		contextSink:  cfg.Context,
		launcher:     cfg.Launcher,
		tracer:       cfg.Tracer,
		registry:     registry,
		depth:        cfg.Depth,
		maxDepth:     cfg.MaxDepth,
		parentTaskID: strings.TrimSpace(cfg.ParentTaskID),
		mcpBroker:    cfg.MCPBroker,
	}
	m.actors = newTaskActorHost(cfg.Root, registry)
	if m.maxDepth <= 0 {
		m.maxDepth = 4
	}
	if m.launcher == nil && m.model != nil {
		m.launcher = newInProcessLauncher(m.runWorkerInProcess)
	}
	if brokerAware, ok := m.launcher.(interface{ SetMCPBroker(coremcp.Broker) }); ok {
		brokerAware.SetMCPBroker(m.mcpBroker)
	}
	if subscriber, ok := m.mcpBroker.(interface {
		Subscribe() (<-chan coremcp.Snapshot, func())
	}); ok {
		updates, stopUpdates := subscriber.Subscribe()
		m.mcpStop = stopUpdates
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			m.forwardMCPSnapshots(updates, stopUpdates)
		}()
	}
	// 孤儿回收：上一实例异常退出后磁盘上残留 running 的任务（进程已死）
	// 立即转为 interrupted 终态，避免任务卡/活动面板/RunningTasks 把它们
	// 当作运行中，也避免 TaskWait 对僵尸 id 一直等到超时。
	m.importLegacyTasks(context.Background())
	m.reconcileOrphanedTasks(context.Background())
	return m
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.close.Do(func() {
		if m.mcpStop != nil {
			m.mcpStop()
		}
		m.workers.Wait()
		m.actors.close()
	})
	return nil
}

func (m *Manager) importLegacyTasks(ctx context.Context) {
	if m == nil || m.actors == nil || m.registry.root == "" {
		return
	}
	tasks, err := m.registry.listTasks(ctx)
	if err != nil {
		return
	}
	for _, task := range tasks {
		actorTask, found, statusErr := m.actors.status(ctx, task.ID)
		if statusErr != nil {
			continue
		}
		if found {
			_ = m.registry.saveTask(ctx, actorTask)
			continue
		}
		_ = m.actors.record(ctx, taskEventForStatus(task.Status), task)
	}
}

// reconcileOrphanedTasks 把磁盘上 status=running 但 worker 进程已不存在的
// 任务标记为 TaskInterrupted 并写盘。本实例 Host 中仍有进程句柄的任务不受影响；
// PID 存活检查保证多实例场景下不会误杀其他实例仍在运行的任务。
// 回收是尽力而为的：任何磁盘/进程检查失败都跳过，不影响 Manager 启动。
func (m *Manager) reconcileOrphanedTasks(ctx context.Context) {
	if m == nil || m.actors == nil || m.registry.root == "" {
		return
	}
	tasks, err := m.actors.list(ctx)
	if err != nil {
		return
	}
	for _, task := range tasks {
		_ = m.registry.saveTask(ctx, task)
		if task.Status != TaskRunning || m.actors.hasActiveTask(task.ID) {
			continue
		}
		// 有存活 PID（可能是其他 paw 实例正在运行的任务）→ 保持 running。
		if task.PID > 0 && processAlive(task.PID) {
			continue
		}
		_, _, _ = m.actors.stop(ctx, task.ID, TaskInterrupted, "interrupted: worker process exited unexpectedly")
	}
}

func (m *Manager) forwardMCPSnapshots(updates <-chan coremcp.Snapshot, stop func()) {
	if stop != nil {
		defer stop()
	}
	for snapshot := range updates {
		if m.snapshotAlreadyForwarded(snapshot) {
			continue
		}
		for _, process := range m.actors.runningProcesses() {
			if updater, ok := process.(interface{ UpdateMCPSnapshot(coremcp.Snapshot) error }); ok {
				_ = updater.UpdateMCPSnapshot(snapshot)
			}
		}
	}
}

func (m *Manager) snapshotAlreadyForwarded(snapshot coremcp.Snapshot) bool {
	// MCP Manager assigns monotonically increasing versions to capability
	// snapshots. Prefer that stable identity: Snapshot.Clone normalizes empty
	// schemas, so hashing the marshalled clone could treat equivalent snapshots
	// as different values.
	var fingerprint uint64
	if snapshot.Version != 0 {
		fingerprint = uint64(snapshot.Version)
	} else {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return false
		}
		hash := fnv.New64a()
		_, _ = hash.Write(data)
		fingerprint = hash.Sum64()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpSnapshotSet && m.lastMCPSnapshot == fingerprint {
		return true
	}
	m.lastMCPSnapshot = fingerprint
	m.mcpSnapshotSet = true
	return false
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
	task, _ = m.finishTask(ctx, task.ID, workerResult, waitErr)
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

	events := make(chan model.StreamEvent, streamEventBufferSize)
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
		return TaskSnapshot{}, fmt.Errorf("task task id is required")
	}

	task, taskOK := m.Status(id)
	if !taskOK {
		return TaskSnapshot{}, fmt.Errorf("task task not found: %s", id)
	}
	if task.Status != TaskRunning {
		return task, fmt.Errorf("task task %s is not running (status=%s)", id, task.Status)
	}
	if actorTask, actorOK, actorErr := m.actors.status(ctx, id); actorErr != nil {
		return TaskSnapshot{}, actorErr
	} else if !actorOK {
		if err := m.actors.record(ctx, taskEventStarted, task); err != nil {
			return TaskSnapshot{}, err
		}
	} else {
		task = actorTask
	}
	stopped, changed, err := m.actors.stop(ctx, id, TaskStopped, "stopped")
	if err != nil {
		return stopped, err
	}
	if changed {
		m.recordTaskFinished(stopped)
	}
	m.notifyTaskFinished(stopped)
	return stopped, nil
}

// StopOwnedTasks interrupts running background tasks launched by one exact
// parent turn. Empty ownership never matches, preserving compatibility with
// tasks created before ParentTurnID was introduced.
func (m *Manager) StopOwnedTasks(ctx context.Context, parentSessionID, parentTurnID, reason string) {
	if m == nil {
		return
	}
	parentSessionID = strings.TrimSpace(parentSessionID)
	parentTurnID = strings.TrimSpace(parentTurnID)
	if parentSessionID == "" || parentTurnID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "interrupted: parent turn ended unexpectedly"
	}

	owned, err := m.actors.owned(ctx, parentSessionID, parentTurnID)
	if err != nil {
		return
	}
	for _, task := range owned {
		stopped, changed, stopErr := m.actors.stop(ctx, task.ID, TaskInterrupted, reason)
		if stopErr != nil || !changed {
			continue
		}
		m.recordTaskFinished(stopped)
		m.notifyTaskFinished(stopped)
	}
}

// SubscribeTaskUpdates returns a wakeup stream for task status changes. The
// stream carries no task data; consumers must call ListTasks for a fresh snapshot.
// The cancel function is idempotent and must be called when the consumer exits.
func (m *Manager) SubscribeTaskUpdates() (<-chan struct{}, func()) {
	if m == nil {
		closed := make(chan struct{})
		close(closed)
		return closed, func() {}
	}
	return m.actors.subscribe()
}

// WaitAny 阻塞直到任意一个给定任务进入终态（completed/failed/stopped）或
// timeout 到期。返回所有请求任务的最新快照；超时时 timed_out=true（非错误）。
// 未知 id 标记 not_found 且不构成"完成"条件；全部目标都不存在时立即返回
// （不存在的任务永远不会进入终态，等待必然挂满超时）。ctx 取消时返回错误。
func (m *Manager) WaitAny(ctx context.Context, ids []string, timeout time.Duration) (WaitResult, error) {
	if m == nil {
		return WaitResult{}, fmt.Errorf("task manager is nil")
	}
	if err := ctx.Err(); err != nil {
		return WaitResult{}, err
	}
	seen := make(map[string]bool, len(ids))
	targets := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		targets = append(targets, id)
	}
	if len(targets) == 0 {
		return WaitResult{}, fmt.Errorf("at least one task task id is required")
	}

	deadline := time.Now().Add(timeout)
	updates, cancel := m.actors.subscribe()
	defer cancel()

	remaining := timeout
	for {
		result, err := m.actors.waitSnapshot(ctx, targets)
		if err != nil {
			return WaitResult{}, err
		}
		if result.AnyTerminal || allTasksNotFound(result.Tasks) {
			return result, nil
		}
		if remaining <= 0 {
			result.TimedOut = true
			result.AnyTerminal = false
			return result, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case _, ok := <-updates:
			timer.Stop()
			if !ok {
				return WaitResult{}, fmt.Errorf("task manager is closed")
			}
			// 重新检查状态；循环顶部会重算剩余时间。
			remaining = time.Until(deadline)
			if remaining <= 0 {
				result, err := m.actors.waitSnapshot(ctx, targets)
				if err != nil {
					return WaitResult{}, err
				}
				result.TimedOut = !result.AnyTerminal
				return result, nil
			}
			continue
		case <-ctx.Done():
			timer.Stop()
			return WaitResult{}, ctx.Err()
		case <-timer.C:
			result, err := m.actors.waitSnapshot(ctx, targets)
			if err != nil {
				return WaitResult{}, err
			}
			result.TimedOut = !result.AnyTerminal
			return result, nil
		}
	}
}

// allTasksNotFound 报告所有等待目标都不在注册表投影中：这些 id 永远不会
// 进入终态（投影不会因为等待而凭空出现任务），等待必然挂满超时。
func allTasksNotFound(tasks []TaskSummary) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if !task.NotFound {
			return false
		}
	}
	return true
}

func isTerminalStatus(status TaskStatus) bool {
	switch status {
	case TaskCompleted, TaskFailed, TaskStopped, TaskInterrupted:
		return true
	default:
		return false
	}
}

// RunningTasks 返回 RegistryActor 投影中的全部运行中任务。
func (m *Manager) RunningTasks() []TaskSnapshot {
	if m == nil {
		return nil
	}
	tasks := m.ListTasks()
	out := make([]TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == TaskRunning {
			out = append(out, task)
		}
	}
	return out
}

// ActiveTasks returns running tasks that still have a live process owned by
// this manager. The UI uses this liveness view so a lagging durable projection
// cannot leave the transient task card visible after the process has ended.
func (m *Manager) ActiveTasks() []TaskSnapshot {
	if m == nil || m.actors == nil {
		return nil
	}
	tasks := m.ListTasks()
	out := make([]TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == TaskRunning && m.actors.hasActiveTask(task.ID) {
			out = append(out, task)
		}
	}
	return out
}

func (m *Manager) Status(id string) (TaskSnapshot, bool) {
	if m == nil {
		return TaskSnapshot{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return TaskSnapshot{}, false
	}
	task, ok, err := m.actors.status(context.Background(), id)
	if err != nil {
		return TaskSnapshot{}, false
	}
	return task, ok
}

func (m *Manager) ListTasks() []TaskSnapshot {
	if m == nil {
		return nil
	}
	tasks, err := m.actors.list(context.Background())
	if err != nil {
		return nil
	}
	return tasks
}

// TotalTaskTokens returns the sum of UsedTokens for all completed tasks
// whose ParentSessionID matches the given session ID.
func (m *Manager) TotalTaskTokens(parentSessionID string) int {
	if m == nil || parentSessionID == "" {
		return 0
	}
	total, err := m.actors.totalTokens(context.Background(), parentSessionID)
	if err != nil {
		return 0
	}
	return total
}

func (m *Manager) normalizeRequest(req Request) Request {
	cfg := settings.DefaultConfig()
	if m != nil && m.settings != nil {
		cfg = m.settings.CurrentSettings()
	}
	if req.ContextMode == "" {
		req.ContextMode = cfg.Task.DefaultContextMode
	}
	if req.RunMode == "" {
		req.RunMode = cfg.Task.DefaultRunMode
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
		return fmt.Errorf("task manager is nil")
	}
	if m.launcher == nil {
		return fmt.Errorf("task launcher is nil")
	}
	if m.store == nil {
		return fmt.Errorf("task store is nil")
	}
	if strings.TrimSpace(m.root) == "" {
		return fmt.Errorf("task root is empty")
	}
	return nil
}

func (m *Manager) validateStreaming() error {
	if m == nil {
		return fmt.Errorf("task manager is nil")
	}
	if m.model == nil {
		return fmt.Errorf("task streaming model is nil")
	}
	if m.store == nil {
		return fmt.Errorf("task store is nil")
	}
	if strings.TrimSpace(m.root) == "" {
		return fmt.Errorf("task root is empty")
	}
	return nil
}

func (m *Manager) startTask(ctx context.Context, req Request) (TaskSnapshot, Process, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	depth := m.depth + 1
	if depth > m.maxDepth {
		return TaskSnapshot{}, nil, fmt.Errorf("task depth limit exceeded: %d > %d", depth, m.maxDepth)
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
		ParentTurnID:    strings.TrimSpace(req.ParentTurnID),
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
		ParentTurnID:    strings.TrimSpace(req.ParentTurnID),
		ParentTaskID:    task.ParentTaskID,
		Prompt:          req.Prompt,
		Description:     req.Description,
		DisableTools:    req.DisableTools,
		ContextMode:     req.ContextMode,
		RunMode:         req.RunMode,
		Depth:           task.Depth,
		MaxDepth:        m.maxDepth,
	}
	if !req.DisableTools && m.mcpBroker != nil {
		workerReq.MCPSnapshot = m.mcpBroker.Snapshot()
	}

	if err := m.actors.record(ctx, taskEventCreated, task); err != nil {
		return TaskSnapshot{}, nil, err
	}

	process, err := m.launcher.Start(ctx, workerReq)
	if err != nil {
		task.Status = TaskFailed
		task.Error = err.Error()
		now := time.Now().UTC()
		exitCode := 1
		task.FinishedAt = &now
		task.ExitCode = &exitCode
		_ = m.registry.saveOutput(ctx, task.ID, WorkerResult{
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Error:     task.Error,
			ExitCode:  exitCode,
		})
		_, _, _ = m.actors.transition(ctx, taskEventFailed, task)
		return TaskSnapshot{}, nil, err
	}
	task.PID = process.PID()
	// worker 具名角色：若进程承载者是具名池 worker，任务名/色采用执行它的
	// worker 角色（任务剥离 persona）；无具名 worker（in-process/streaming）
	// 时保留 assignPersona 分配的角色名。
	if source, ok := process.(WorkerRoleSource); ok {
		if name, color := source.WorkerRole(); name != "" {
			task.Name = name
			task.Color = color
		}
	}
	m.actors.bind(task.ID, process)
	if err := m.actors.record(ctx, taskEventStarted, task); err != nil {
		m.actors.release(task.ID)
		_ = process.Stop()
		now := time.Now().UTC()
		exitCode := 1
		task.Status = TaskFailed
		task.Error = err.Error()
		task.FinishedAt = &now
		task.ExitCode = &exitCode
		_ = m.registry.saveOutput(ctx, task.ID, WorkerResult{
			TaskID:    task.ID,
			SessionID: task.SessionID,
			Error:     task.Error,
			ExitCode:  exitCode,
		})
		_, _, _ = m.actors.transition(ctx, taskEventFailed, task)
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
		return TaskSnapshot{}, fmt.Errorf("task depth limit exceeded: %d > %d", depth, m.maxDepth)
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
		ParentTurnID:    strings.TrimSpace(req.ParentTurnID),
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

	if err := m.actors.record(ctx, taskEventCreated, task); err != nil {
		return TaskSnapshot{}, err
	}
	if err := m.actors.record(ctx, taskEventStarted, task); err != nil {
		return TaskSnapshot{}, err
	}
	m.actors.track(task.ID)
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
			_ = sendModelStreamEvent(ctx, events, model.StreamEvent{Err: err})
		}
	}
	m.finishTask(context.Background(), task.ID, result, err)
}

func (m *Manager) waitBackground(taskID string, process Process) {
	result, err := process.Wait()
	task, changed := m.finishTask(context.Background(), taskID, result, err)
	if changed {
		m.notifyTaskFinished(task)
	}
}

func (m *Manager) finishTask(ctx context.Context, taskID string, result WorkerResult, err error) (TaskSnapshot, bool) {
	m.actors.release(taskID)
	now := time.Now().UTC()
	task, found, statusErr := m.actors.status(ctx, taskID)
	if statusErr != nil || !found || task.Status != TaskRunning {
		return task, false
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
	_ = m.registry.saveOutput(ctx, taskID, result)
	transitioned, changed, transitionErr := m.actors.transition(ctx, taskEventForStatus(task.Status), task)
	if transitionErr != nil || !changed {
		return transitioned, false
	}
	task = transitioned
	m.submitTaskContext(task)
	m.recordTaskFinished(task)
	return task, true
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
				Task:            true,
			}); err != nil {
				return "", err
			}
		default:
			if _, err := m.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID, Task: true}); err != nil {
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
			Task:            true,
		}); err != nil {
			return "", err
		}
	default:
		if _, err := m.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID, Task: true}); err != nil {
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
	store, ok := m.store.(*session.JSONLStore)
	if !ok {
		return "", 0, nil, fmt.Errorf("task session actor requires JSONL session store")
	}
	engine := loop.NewEngineWithInstructionRoot(m.model, usageUI, m.toolRegistry(disableTools), m.store, sessionID, m.root)
	host, err := sessionactor.NewHost(engine, store, sessionID)
	if err != nil {
		return "", 0, nil, err
	}
	defer host.Close()
	msg, err := host.RunTurn(ctx, prompt)
	usedTokens := engine.ContextStats(1<<30, "").UsedTokens
	usage := usageUI.Usage()
	if err != nil {
		return "", usedTokens, usage, err
	}
	return strings.TrimSpace(msg.Content), usedTokens, usage, nil
}

func (m *Manager) runStreamingSession(ctx context.Context, sessionID, prompt, systemPrompt string, disableTools bool, events chan<- model.StreamEvent) (string, int, *tokentracer.Usage, bool, error) {
	streamUI := &streamingUI{ctx: ctx, events: events}
	store, ok := m.store.(*session.JSONLStore)
	if !ok {
		return "", 0, nil, false, fmt.Errorf("task session actor requires JSONL session store")
	}
	engine := loop.NewEngineWithInstructionRoot(m.model, streamUI, m.toolRegistry(disableTools), m.store, sessionID, m.root)
	engine.SetSystemSupplement(systemPrompt)
	if isStreamMASystemPrompt(systemPrompt) {
		engine.SetCompactToolPrompt(true)
	}
	host, err := sessionactor.NewHost(engine, store, sessionID)
	if err != nil {
		return "", 0, nil, false, err
	}
	defer host.Close()
	msg, err := host.RunTurn(ctx, prompt)
	usedTokens := engine.ContextStats(1<<30, "").UsedTokens
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
	tracer.RecordEvent("task_start", map[string]any{
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
	tracer.RecordEvent("task_end", data)
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
	// 与 SubmitSupplement 注入父会话的块一致：TUI 识别 <task> 块并渲染为框线卡片。
	_ = m.notifier.OnSystemMessage(ui.SystemEvent{
		Title: "task",
		Body:  renderTaskCompletionBlock(task),
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
	update := renderTaskCompletionBlock(task)
	if strings.TrimSpace(update) == "" {
		return
	}
	_ = m.contextSink.SubmitSupplement(update)
}

// renderTaskCompletionBlock 把后台任务完成事件渲染成结构化 <task> 块。
// 该块注入父会话上下文（模型侧可见原始 XML），同时被 TUI 识别为框线块。
func renderTaskCompletionBlock(task TaskSnapshot) string {
	state := string(task.Status)
	if state != string(TaskCompleted) && state != string(TaskFailed) && state != string(TaskStopped) && state != string(TaskInterrupted) {
		state = string(TaskCompleted)
	}
	var attrs strings.Builder
	attrs.WriteString(`id="` + escapeTaskAttr(task.ID) + `"`)
	attrs.WriteString(` state="` + state + `"`)
	if name := strings.TrimSpace(task.Name); name != "" {
		attrs.WriteString(` name="` + escapeTaskAttr(name) + `"`)
	}
	if task.FinishedAt != nil && !task.StartedAt.IsZero() {
		durationMs := task.FinishedAt.Sub(task.StartedAt).Milliseconds()
		if durationMs >= 0 {
			attrs.WriteString(fmt.Sprintf(` duration_ms="%d"`, durationMs))
		}
	}
	if size := outputFileSize(task.OutputPath); size >= 0 {
		attrs.WriteString(fmt.Sprintf(` output_size="%d"`, size))
	}

	var body strings.Builder
	content := strings.TrimSpace(task.Content)
	if content != "" {
		body.WriteString("summary: ")
		body.WriteString(truncateForParentContext(content, parentContextResultMaxRunes, task.OutputPath))
		body.WriteString("\n")
	}
	if strings.TrimSpace(task.Error) != "" {
		body.WriteString("error: " + strings.TrimSpace(task.Error) + "\n")
	}
	if strings.TrimSpace(task.TranscriptPath) != "" {
		body.WriteString("transcript: " + strings.TrimSpace(task.TranscriptPath) + "\n")
	}
	if strings.TrimSpace(task.OutputPath) != "" {
		body.WriteString("output: " + strings.TrimSpace(task.OutputPath) + "\n")
	}
	return "<task " + attrs.String() + ">\n" + strings.TrimRight(body.String(), "\n") + "\n</task>"
}

// escapeTaskAttr 转义 XML 属性中的特殊字符，防止注入破坏 <task> 块结构。
func escapeTaskAttr(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

// outputFileSize 返回 output 文件字节数；读取失败返回 -1（调用方省略属性）。
func outputFileSize(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return -1
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return -1
	}
	return info.Size()
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
	registry := newBaseToolRegistry(root)
	if m != nil && m.mcpBroker != nil {
		_ = registry.ReplaceNamespace("mcp", mcpTools(m.mcpBroker, disableTools))
	}
	return registry
}

func mcpTools(broker coremcp.Broker, disableTools bool) []tool.Tool {
	if disableTools || broker == nil {
		return nil
	}
	tools := toolmcp.NewTools(broker)
	result := make([]tool.Tool, 0, len(tools))
	for _, item := range tools {
		result = append(result, item)
	}
	return result
}

func newBaseToolRegistry(root string) *tool.Registry {
	registry := tool.NewRegistry()
	readRoots := skill.DefaultRoots(root)
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
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

type taskTool struct {
	manager         *Manager
	parentSessionID string
}

type statusTool struct {
	manager *Manager
}

type stopTool struct {
	manager *Manager
}

type waitTool struct {
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

type waitInput struct {
	TaskIDs   []string `json:"task_ids"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
}

func NewTool(manager *Manager, parentSessionID string) tool.Tool {
	return &taskTool{manager: manager, parentSessionID: parentSessionID}
}

func NewStatusTool(manager *Manager) tool.Tool {
	return &statusTool{manager: manager}
}

func NewStopTool(manager *Manager) tool.Tool {
	return &stopTool{manager: manager}
}

func NewWaitTool(manager *Manager) tool.Tool {
	return &waitTool{manager: manager}
}

func (t *taskTool) Name() string {
	return "Task"
}

func (t *taskTool) Description() string {
	return "Launch a focused task. Use context_mode empty for a self-contained spec, or fork to inherit committed parent session history. run_mode defaults to background and returns a task id immediately; explicit sync waits for the result."
}

func (t *taskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"description":{"type":"string"},"context_mode":{"type":"string","enum":["empty","fork"]},"run_mode":{"type":"string","enum":["sync","background"],"default":"background"}},"required":["prompt"]}`)
}

func (t *taskTool) buildRequest(raw json.RawMessage) (Request, error) {
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

func (t *taskTool) IsConcurrencySafe(raw json.RawMessage) bool {
	req, err := t.buildRequest(raw)
	return err == nil && req.RunMode == settings.RunModeBackground
}

func (t *taskTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	req, err := t.buildRequest(raw)
	if err != nil {
		return "", err
	}
	if owner, ok := loop.TurnOwnerFromContext(ctx); ok {
		req.ParentSessionID = owner.SessionID
		req.ParentTurnID = owner.TurnID
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
	return "TaskStatus"
}

func (t *statusTool) Description() string {
	return "Summarize task tasks. Without id, lists only running tasks. Results are not included — read the task's output_path. Never poll — use TaskWait instead."
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
			return "", fmt.Errorf("task task not found: %s", in.ID)
		}
		return marshalResult(summarizeTask(task)), nil
	}
	running := t.manager.RunningTasks()
	summaries := make([]TaskSummary, 0, len(running))
	for _, task := range running {
		summaries = append(summaries, summarizeTask(task))
	}
	return marshalResult(summaries), nil
}

func (t *stopTool) Name() string {
	return "TaskStop"
}

func (t *stopTool) Description() string {
	return "Stop a running background task task by id."
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

func (t *waitTool) Name() string {
	return "TaskWait"
}

func (t *waitTool) Description() string {
	return "Block until any of the given task tasks finishes (completed, failed, or stopped). Returns the latest snapshot of all requested tasks with timed_out=false once at least one finishes; on timeout returns timed_out=true with the current snapshot (not an error). Do not poll TaskStatus — use TaskWait instead."
}

func (t *waitTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"task_ids":{"type":"array","items":{"type":"string"},"minItems":1},"timeout_ms":{"type":"number"}},"required":["task_ids"]}`)
}

func (t *waitTool) IsConcurrencySafe(json.RawMessage) bool {
	return t != nil && t.manager != nil
}

func (t *waitTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in waitInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	if len(in.TaskIDs) == 0 {
		return "", fmt.Errorf("task_ids is required (at least one task task id)")
	}
	timeout := time.Duration(in.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(t.manager.defaultWaitTimeout()) * time.Millisecond
	}
	result, err := t.manager.WaitAny(ctx, in.TaskIDs, timeout)
	if err != nil {
		return "", err
	}
	return marshalResult(result), nil
}

// defaultWaitTimeout 返回 settings.task.wait_timeout_ms 的默认等待上限。
func (m *Manager) defaultWaitTimeout() int {
	if m == nil || m.settings == nil {
		return settings.DefaultTaskWaitTimeoutMs
	}
	return m.settings.CurrentSettings().Task.WaitTimeoutMs
}

func marshalResult(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func TranscriptPath(root, sessionID string) string {
	return filepath.Join(root, ".paw", "sessions", sessionID, "transcript.jsonl")
}
