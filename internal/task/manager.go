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
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	toolwebfetch "paw/internal/tool/webfetch"
	"paw/internal/ui"
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

	startMu sync.Mutex
	mu      sync.RWMutex
	tasks   map[string]TaskSnapshot
	running map[string]Process

	// notifyCh 广播任务状态转换。每次有任务进入终态（或启动失败）时在
	// m.mu 内 close 并替换为新 channel，唤醒所有 TaskWait 等待者。
	// WaitAny 每次迭代都在锁内获取引用并先检查终态，保证不丢失唤醒。
	notifyCh chan struct{}

	// taskUpdateSubs 接收任务状态变化通知，供 TUI 等实时消费者唤醒刷新。
	// 通知只负责唤醒，消费者应通过 ListTasks 获取最新快照。
	taskUpdateSubs map[chan struct{}]struct{}

	// diskTaskCache avoids rescanning the task registry on every status poll.
	// In-memory tasks are always merged so running/completed transitions remain
	// immediately visible; disk changes are observed at most one TTL later.
	diskTaskCacheAt time.Time
	diskTaskCache   []TaskSnapshot
	lastMCPSnapshot uint64
	mcpSnapshotSet  bool
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
	taskListCacheTTL      = 500 * time.Millisecond
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
	m := &Manager{
		model:          cfg.Model,
		store:          cfg.Store,
		root:           cfg.Root,
		settings:       cfg.Settings,
		notifier:       cfg.Notifier,
		contextSink:    cfg.Context,
		launcher:       cfg.Launcher,
		tracer:         cfg.Tracer,
		registry:       newTaskRegistry(cfg.Root),
		depth:          cfg.Depth,
		maxDepth:       cfg.MaxDepth,
		parentTaskID:   strings.TrimSpace(cfg.ParentTaskID),
		mcpBroker:      cfg.MCPBroker,
		tasks:          make(map[string]TaskSnapshot),
		running:        make(map[string]Process),
		taskUpdateSubs: make(map[chan struct{}]struct{}),
		notifyCh:       make(chan struct{}),
	}
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
		go m.forwardMCPSnapshots(updates, stopUpdates)
	}
	// 孤儿回收：上一实例异常退出后磁盘上残留 running 的任务（进程已死）
	// 立即转为 interrupted 终态，避免任务卡/活动面板/RunningTasks 把它们
	// 当作运行中，也避免 TaskWait 对僵尸 id 一直等到超时。
	m.reconcileOrphanedTasks(context.Background())
	return m
}

// reconcileOrphanedTasks 把磁盘上 status=running 但 worker 进程已不存在的
// 任务标记为 TaskInterrupted 并写盘。本实例内存中的任务（m.tasks）不受影响；
// PID 存活检查保证多实例场景下不会误杀其他实例仍在运行的任务。
// 回收是尽力而为的：任何磁盘/进程检查失败都跳过，不影响 Manager 启动。
func (m *Manager) reconcileOrphanedTasks(ctx context.Context) {
	if m == nil || m.registry.root == "" {
		return
	}
	diskTasks, err := m.registry.listTasks(ctx)
	if err != nil {
		return
	}
	m.mu.RLock()
	inMemory := make(map[string]bool, len(m.tasks))
	for id := range m.tasks {
		inMemory[id] = true
	}
	m.mu.RUnlock()

	now := time.Now().UTC()
	reconciled := false
	for _, task := range diskTasks {
		if task.Status != TaskRunning || inMemory[task.ID] {
			continue
		}
		// 有存活 PID（可能是其他 paw 实例正在运行的任务）→ 保持 running。
		if task.PID > 0 && processAlive(task.PID) {
			continue
		}
		task.Status = TaskInterrupted
		task.FinishedAt = &now
		exitCode := -1
		task.ExitCode = &exitCode
		task.Error = "interrupted: worker process exited unexpectedly"
		if err := m.registry.saveTask(ctx, task); err != nil {
			continue
		}
		reconciled = true
	}
	if !reconciled {
		return
	}
	m.mu.Lock()
	m.diskTaskCache = nil
	m.diskTaskCacheAt = time.Time{}
	m.mu.Unlock()
}

func (m *Manager) forwardMCPSnapshots(updates <-chan coremcp.Snapshot, stop func()) {
	if stop != nil {
		defer stop()
	}
	for snapshot := range updates {
		if m.snapshotAlreadyForwarded(snapshot) {
			continue
		}
		m.mu.RLock()
		processes := make([]Process, 0, len(m.running))
		for _, process := range m.running {
			processes = append(processes, process)
		}
		m.mu.RUnlock()
		for _, process := range processes {
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

	m.mu.RLock()
	process := m.running[id]
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
		return TaskSnapshot{}, fmt.Errorf("task task not found: %s", id)
	}
	if task.Status != TaskRunning {
		return task, fmt.Errorf("task task %s is not running (status=%s)", id, task.Status)
	}
	if process != nil {
		_ = process.Stop()
	}
	stopped, changed := m.transitionTaskStopped(ctx, id, TaskStopped, "stopped")
	if !changed {
		return stopped, fmt.Errorf("task task %s is not running (status=%s)", id, stopped.Status)
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

	type ownedProcess struct {
		id      string
		process Process
	}
	m.mu.RLock()
	owned := make([]ownedProcess, 0)
	for id, task := range m.tasks {
		if task.Status == TaskRunning && task.RunMode == settings.RunModeBackground &&
			task.ParentSessionID == parentSessionID && task.ParentTurnID == parentTurnID {
			owned = append(owned, ownedProcess{id: id, process: m.running[id]})
		}
	}
	m.mu.RUnlock()

	for _, item := range owned {
		if item.process != nil {
			_ = item.process.Stop()
		}
		if task, changed := m.transitionTaskStopped(ctx, item.id, TaskInterrupted, reason); changed {
			m.notifyTaskFinished(task)
		}
	}
}

func (m *Manager) transitionTaskStopped(ctx context.Context, id string, status TaskStatus, reason string) (TaskSnapshot, bool) {
	now := time.Now().UTC()
	exitCode := -1
	m.mu.Lock()
	task := m.tasks[id]
	if task.Status != TaskRunning {
		delete(m.running, id)
		m.mu.Unlock()
		return task, false
	}
	if status != TaskStopped && status != TaskInterrupted {
		status = TaskInterrupted
	}
	task.Status = status
	task.FinishedAt = &now
	task.ExitCode = &exitCode
	task.Error = reason
	m.tasks[id] = task
	delete(m.running, id)
	m.mu.Unlock()

	_ = m.registry.saveOutput(ctx, id, WorkerResult{TaskID: id, SessionID: task.SessionID, Error: reason, ExitCode: exitCode})
	_ = m.registry.saveTask(ctx, task)
	m.signalTaskUpdate()
	m.recordTaskFinished(task)
	return task, true
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
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	if m.taskUpdateSubs == nil {
		m.taskUpdateSubs = make(map[chan struct{}]struct{})
	}
	m.taskUpdateSubs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if _, ok := m.taskUpdateSubs[ch]; ok {
			delete(m.taskUpdateSubs, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
}

// signalTaskUpdate 唤醒所有阻塞在 WaitAny 上的等待者。必须在任务状态
// 已切换为终态之后调用；close 与替换都在 m.mu 内完成，等待者每次迭代
// 也在锁内获取 channel 引用，因此不会丢失唤醒也不会读到已关闭的旧 channel。
func (m *Manager) signalTaskUpdate() {
	if m == nil {
		return
	}
	m.mu.Lock()
	close(m.notifyCh)
	m.notifyCh = make(chan struct{})
	for ch := range m.taskUpdateSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	m.mu.Unlock()
}

// WaitAny 阻塞直到任意一个给定任务进入终态（completed/failed/stopped）或
// timeout 到期。返回所有请求任务的最新快照；超时时 timed_out=true（非错误）。
// 未知 id 标记 not_found 且不构成"完成"条件。ctx 取消时返回错误。
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

	// 磁盘加载过的任务快照，避免每次迭代读盘。
	loaded := make(map[string]TaskSnapshot, len(targets))
	notFound := make(map[string]bool, len(targets))
	deadline := time.Now().Add(timeout)

	snapshot := func() WaitResult {
		m.mu.RLock()
		defer m.mu.RUnlock()
		tasks := make([]TaskSummary, 0, len(targets))
		anyTerminal := false
		allSettled := true
		for _, id := range targets {
			if task, ok := m.tasks[id]; ok {
				tasks = append(tasks, summarizeTask(task))
				if isTerminalStatus(task.Status) {
					anyTerminal = true
				} else {
					allSettled = false
				}
				continue
			}
			if task, ok := loaded[id]; ok {
				tasks = append(tasks, summarizeTask(task))
				if isTerminalStatus(task.Status) {
					anyTerminal = true
				} else {
					allSettled = false
				}
				continue
			}
			if notFound[id] {
				tasks = append(tasks, TaskSummary{ID: id, Status: TaskNotFound, NotFound: true})
				continue
			}
			// 既不在内存也不在本地缓存：尝试从磁盘加载一次（宽容处理，
			// 例如父进程重启后等待历史任务）。加载失败视为 not_found。
			m.mu.RUnlock()
			task, ok := m.Status(id)
			m.mu.RLock()
			if !ok {
				notFound[id] = true
				tasks = append(tasks, TaskSummary{ID: id, Status: TaskNotFound, NotFound: true})
				continue
			}
			loaded[id] = task
			tasks = append(tasks, summarizeTask(task))
			if isTerminalStatus(task.Status) {
				anyTerminal = true
			} else {
				allSettled = false
			}
		}
		if allSettled {
			anyTerminal = true
		}
		return WaitResult{TimedOut: false, Tasks: tasks, AnyTerminal: anyTerminal}
	}

	remaining := timeout
	for {
		result := snapshot()
		if result.AnyTerminal {
			return result, nil
		}
		if remaining <= 0 {
			result.TimedOut = true
			result.AnyTerminal = false
			return result, nil
		}
		m.mu.RLock()
		ch := m.notifyCh
		m.mu.RUnlock()
		timer := time.NewTimer(remaining)
		select {
		case <-ch:
			timer.Stop()
			// 重新检查状态；循环顶部会重算剩余时间。
			remaining = time.Until(deadline)
			if remaining <= 0 {
				result := snapshot()
				result.TimedOut = true
				return result, nil
			}
			continue
		case <-ctx.Done():
			timer.Stop()
			return WaitResult{}, ctx.Err()
		case <-timer.C:
			result := snapshot()
			result.TimedOut = true
			return result, nil
		}
	}
}

func isTerminalStatus(status TaskStatus) bool {
	switch status {
	case TaskCompleted, TaskFailed, TaskStopped, TaskInterrupted:
		return true
	default:
		return false
	}
}

// RunningTasks 返回当前所有运行中任务的摘要列表（仅内存+磁盘中 running 的任务）。
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
	now := time.Now()
	m.mu.RLock()
	tasksByID := make(map[string]TaskSnapshot, len(m.tasks))
	for _, task := range m.tasks {
		tasksByID[task.ID] = task
	}
	diskTasks := append([]TaskSnapshot(nil), m.diskTaskCache...)
	cacheFresh := !m.diskTaskCacheAt.IsZero() && now.Sub(m.diskTaskCacheAt) < taskListCacheTTL
	m.mu.RUnlock()

	if !cacheFresh {
		if loaded, err := m.registry.listTasks(context.Background()); err == nil {
			diskTasks = loaded
			m.mu.Lock()
			m.diskTaskCache = append([]TaskSnapshot(nil), loaded...)
			m.diskTaskCacheAt = now
			m.mu.Unlock()
		}
	}
	for _, task := range diskTasks {
		if _, ok := tasksByID[task.ID]; !ok {
			tasksByID[task.ID] = task
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

// TotalTaskTokens returns the sum of UsedTokens for all completed tasks
// whose ParentSessionID matches the given session ID.
func (m *Manager) TotalTaskTokens(parentSessionID string) int {
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
		m.signalTaskUpdate()
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
		m.signalTaskUpdate()
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
	// Claim the terminal transition while holding the manager lock. Stop and
	// owner cleanup can no longer race in after Wait has selected a terminal
	// result and overwrite (or be overwritten by) that result.
	m.tasks[taskID] = task
	delete(m.running, taskID)

	// Persist before releasing the transition to observers. This is a short,
	// bounded local write and ensures a consumer awakened by signalTaskUpdate can
	// immediately read the matching output artifact and metadata.
	_ = m.registry.saveOutput(ctx, taskID, result)
	_ = m.registry.saveTask(ctx, task)
	m.mu.Unlock()

	m.signalTaskUpdate()
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
