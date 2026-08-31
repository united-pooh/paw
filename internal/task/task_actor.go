package task

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"paw/internal/actor"
	"paw/internal/es"
)

const (
	taskActorType = "task"

	taskActorReplace = "task.replace"
	taskActorGet     = "task.get"
	taskActorStop    = "task.stop"

	taskEventCreated     = "task.created"
	taskEventStarted     = "task.started"
	taskEventCompleted   = "task.completed"
	taskEventFailed      = "task.failed"
	taskEventStopped     = "task.stopped"
	taskEventInterrupted = "task.interrupted"
)

const taskActorAskTimeout = 5 * time.Second

type taskActorMutation struct {
	Event string       `json:"event"`
	Task  TaskSnapshot `json:"task"`
}

type taskActorReply struct {
	Task    TaskSnapshot `json:"task"`
	Found   bool         `json:"found"`
	Changed bool         `json:"changed,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type taskActorStopCommand struct {
	Status TaskStatus `json:"status"`
	Reason string     `json:"reason"`
	At     time.Time  `json:"at"`
}

type taskActorState struct {
	Task  TaskSnapshot `json:"task"`
	Found bool         `json:"found"`
}

type taskActor struct {
	id        actor.ActorID
	registry  taskRegistry
	processes *taskProcessTable
	state     taskActorState
}

func newTaskActor(id actor.ActorID, registry taskRegistry, processes *taskProcessTable) *taskActor {
	return &taskActor{id: id, registry: registry, processes: processes}
}

func (a *taskActor) ID() actor.ActorID { return a.id }

func (a *taskActor) Receive(ctx *actor.Context, msg actor.Msg) {
	switch msg.Kind {
	case taskActorReplace:
		var mutation taskActorMutation
		if err := decodeTaskActorPayload(msg.Payload, &mutation); err != nil {
			a.reply(ctx, taskActorReply{Error: err.Error()})
			return
		}
		mutation.Event = strings.TrimSpace(mutation.Event)
		if mutation.Event == "" {
			a.reply(ctx, taskActorReply{Error: "task event is required"})
			return
		}
		if mutation.Task.ID == "" {
			mutation.Task.ID = a.id.Key
		}
		if mutation.Task.ID != a.id.Key {
			a.reply(ctx, taskActorReply{Error: "task id does not match actor id"})
			return
		}
		if isTerminalStatus(mutation.Task.Status) && a.state.Found && a.state.Task.Status != TaskRunning {
			a.reply(ctx, taskActorReply{Task: a.state.Task, Found: true})
			return
		}
		if err := ctx.Persist(mutation.Event, mutation, actor.Durable); err != nil {
			a.reply(ctx, taskActorReply{Error: err.Error()})
			return
		}
		a.state = taskActorState{Task: mutation.Task, Found: true}
		if err := a.publishTask(ctx, mutation.Task); err != nil {
			a.reply(ctx, taskActorReply{Task: mutation.Task, Found: true, Error: err.Error()})
			return
		}
		a.reply(ctx, taskActorReply{Task: mutation.Task, Found: true, Changed: true})
	case taskActorGet:
		a.reply(ctx, taskActorReply{Task: a.state.Task, Found: a.state.Found})
	case taskActorStop:
		var command taskActorStopCommand
		if err := decodeTaskActorPayload(msg.Payload, &command); err != nil {
			a.reply(ctx, taskActorReply{Error: err.Error()})
			return
		}
		if !a.state.Found {
			a.reply(ctx, taskActorReply{Error: fmt.Sprintf("task task not found: %s", a.id.Key)})
			return
		}
		if a.state.Task.Status != TaskRunning {
			task := a.state.Task
			if err := a.publishTask(ctx, task); err != nil {
				a.reply(ctx, taskActorReply{Task: task, Found: true, Error: err.Error()})
				return
			}
			a.reply(ctx, taskActorReply{Task: task, Found: true})
			return
		}
		if command.Status != TaskStopped && command.Status != TaskInterrupted {
			command.Status = TaskInterrupted
		}
		if command.At.IsZero() {
			command.At = time.Now().UTC()
		}
		exitCode := -1
		task := a.state.Task
		task.Status = command.Status
		task.FinishedAt = &command.At
		task.ExitCode = &exitCode
		task.Error = command.Reason
		mutation := taskActorMutation{Event: taskEventForStatus(task.Status), Task: task}
		if err := ctx.Persist(mutation.Event, mutation, actor.Durable); err != nil {
			a.reply(ctx, taskActorReply{Task: a.state.Task, Found: true, Error: err.Error()})
			return
		}
		a.state = taskActorState{Task: task, Found: true}
		if process := a.processes.take(a.id.Key); process != nil {
			_ = process.Stop()
		}
		_ = a.registry.saveOutput(context.Background(), task.ID, WorkerResult{TaskID: task.ID, SessionID: task.SessionID, Error: command.Reason, ExitCode: exitCode})
		if err := a.publishTask(ctx, task); err != nil {
			a.reply(ctx, taskActorReply{Task: task, Found: true, Changed: true, Error: err.Error()})
			return
		}
		a.reply(ctx, taskActorReply{Task: task, Found: true, Changed: true})
	}
}

func (a *taskActor) publishTask(ctx *actor.Context, task TaskSnapshot) error {
	if err := a.registry.saveTask(context.Background(), task); err != nil {
		return err
	}
	return ctx.Send(taskRegistryActorID, actor.Msg{
		Kind:       taskRegistryUpsert,
		Payload:    taskRegistryUpdate{Task: task},
		Durability: actor.Durable,
	})
}

func (a *taskActor) reply(ctx *actor.Context, reply taskActorReply) {
	ctx.Reply(actor.Msg{Kind: taskActorGet, Payload: reply, Durability: actor.Ephemeral})
}

func (a *taskActor) Fold(env es.Envelope) error {
	switch env.Type {
	case taskEventCreated, taskEventStarted, taskEventCompleted, taskEventFailed, taskEventStopped, taskEventInterrupted:
		var mutation taskActorMutation
		if err := json.Unmarshal(env.Payload, &mutation); err != nil {
			return fmt.Errorf("decode task actor event %s: %w", env.Type, err)
		}
		if mutation.Task.ID == "" {
			mutation.Task.ID = a.id.Key
		}
		a.state = taskActorState{Task: mutation.Task, Found: true}
	}
	return nil
}

func (a *taskActor) Snapshot() (json.RawMessage, error) {
	return json.Marshal(a.state)
}

func (a *taskActor) Restore(state json.RawMessage) error {
	if len(state) == 0 {
		return nil
	}
	return json.Unmarshal(state, &a.state)
}

func (a *taskActor) State() any { return a.state }

type taskActorHost struct {
	system    *actor.System
	processes *taskProcessTable
	updates   *taskUpdateBroker
	closeOnce sync.Once
}

func newTaskActorHost(root string, registry taskRegistry) *taskActorHost {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	processes := newTaskProcessTable()
	updates := newTaskUpdateBroker()
	system := actor.NewSystem(filepath.Join(root, ".paw", "actors"), actor.WithShards(1))
	system.Register(taskActorType, func(id actor.ActorID) actor.Actor {
		return newTaskActor(id, registry, processes)
	})
	system.Register(taskRegistryActorType, func(id actor.ActorID) actor.Actor {
		return newTaskIndexActor(id, updates)
	})
	return &taskActorHost{system: system, processes: processes, updates: updates}
}

func (h *taskActorHost) bind(id string, process Process) {
	if h != nil {
		h.processes.bind(id, process)
	}
}

func (h *taskActorHost) track(id string) {
	if h != nil {
		h.processes.track(id)
	}
}

func (h *taskActorHost) release(id string) {
	if h != nil {
		_ = h.processes.take(id)
	}
}

func (h *taskActorHost) hasActiveTask(id string) bool {
	return h != nil && h.processes.contains(id)
}

func (h *taskActorHost) runningProcesses() []Process {
	if h == nil {
		return nil
	}
	return h.processes.snapshot()
}

func (h *taskActorHost) stop(ctx context.Context, id string, status TaskStatus, reason string) (TaskSnapshot, bool, error) {
	if h == nil || h.system == nil {
		return TaskSnapshot{}, false, fmt.Errorf("task actor host is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(actor.ActorID{Type: taskActorType, Key: id}).Ask(ctx, actor.Msg{
		Kind: taskActorStop,
		Payload: taskActorStopCommand{
			Status: status,
			Reason: reason,
			At:     time.Now().UTC(),
		},
		Durability: actor.Durable,
	}, taskActorAskTimeout)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	decoded, err := decodeTaskActorReply(reply.Payload)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	if decoded.Error != "" {
		return decoded.Task, decoded.Changed, fmt.Errorf("%s", decoded.Error)
	}
	h.system.Drain()
	return decoded.Task, decoded.Changed, nil
}

func (h *taskActorHost) record(ctx context.Context, event string, task TaskSnapshot) error {
	_, _, err := h.transition(ctx, event, task)
	return err
}

func (h *taskActorHost) transition(ctx context.Context, event string, task TaskSnapshot) (TaskSnapshot, bool, error) {
	if h == nil || h.system == nil {
		return TaskSnapshot{}, false, fmt.Errorf("task actor host is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(actor.ActorID{Type: taskActorType, Key: task.ID}).Ask(ctx, actor.Msg{
		Kind:       taskActorReplace,
		Payload:    taskActorMutation{Event: event, Task: task},
		Durability: actor.Durable,
	}, taskActorAskTimeout)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	decoded, err := decodeTaskActorReply(reply.Payload)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	if decoded.Error != "" {
		return decoded.Task, decoded.Changed, fmt.Errorf("record task actor state: %s", decoded.Error)
	}
	h.system.Drain()
	return decoded.Task, decoded.Changed, nil
}

func (h *taskActorHost) list(ctx context.Context) ([]TaskSnapshot, error) {
	if h == nil || h.system == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(taskRegistryActorID).Ask(ctx, actor.Msg{
		Kind:       taskRegistryList,
		Durability: actor.Ephemeral,
	}, taskActorAskTimeout)
	if err != nil {
		return nil, err
	}
	var decoded taskRegistryReply
	if err := decodeTaskActorPayload(reply.Payload, &decoded); err != nil {
		return nil, err
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("list task actor registry: %s", decoded.Error)
	}
	return decoded.Tasks, nil
}

func (h *taskActorHost) owned(ctx context.Context, parentSessionID, parentTurnID string) ([]TaskSnapshot, error) {
	if h == nil || h.system == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(taskRegistryActorID).Ask(ctx, actor.Msg{
		Kind: taskRegistryOwned,
		Payload: taskRegistryOwnedQuery{
			ParentSessionID: parentSessionID,
			ParentTurnID:    parentTurnID,
		},
		Durability: actor.Ephemeral,
	}, taskActorAskTimeout)
	if err != nil {
		return nil, err
	}
	var decoded taskRegistryReply
	if err := decodeTaskActorPayload(reply.Payload, &decoded); err != nil {
		return nil, err
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("query task actor ownership: %s", decoded.Error)
	}
	return decoded.Tasks, nil
}

func (h *taskActorHost) waitSnapshot(ctx context.Context, ids []string) (WaitResult, error) {
	if h == nil || h.system == nil {
		return WaitResult{}, fmt.Errorf("task actor host is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(taskRegistryActorID).Ask(ctx, actor.Msg{
		Kind:       taskRegistryWait,
		Payload:    taskRegistryWaitQuery{IDs: ids},
		Durability: actor.Ephemeral,
	}, taskActorAskTimeout)
	if err != nil {
		return WaitResult{}, err
	}
	var decoded taskRegistryWaitReply
	if err := decodeTaskActorPayload(reply.Payload, &decoded); err != nil {
		return WaitResult{}, err
	}
	if decoded.Error != "" {
		return WaitResult{}, fmt.Errorf("read task wait snapshot: %s", decoded.Error)
	}
	decoded.Result.AnyTerminal = decoded.AnyTerminal
	return decoded.Result, nil
}

func (h *taskActorHost) totalTokens(ctx context.Context, parentSessionID string) (int, error) {
	if h == nil || h.system == nil {
		return 0, fmt.Errorf("task actor host is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(taskRegistryActorID).Ask(ctx, actor.Msg{
		Kind:       taskRegistryTokens,
		Payload:    taskRegistryTokensQuery{ParentSessionID: parentSessionID},
		Durability: actor.Ephemeral,
	}, taskActorAskTimeout)
	if err != nil {
		return 0, err
	}
	var decoded taskRegistryTokensReply
	if err := decodeTaskActorPayload(reply.Payload, &decoded); err != nil {
		return 0, err
	}
	if decoded.Error != "" {
		return 0, fmt.Errorf("read task token projection: %s", decoded.Error)
	}
	return decoded.Total, nil
}

func (h *taskActorHost) subscribe() (<-chan struct{}, func()) {
	if h == nil {
		closed := make(chan struct{})
		close(closed)
		return closed, func() {}
	}
	return h.updates.subscribe()
}

func (h *taskActorHost) status(ctx context.Context, id string) (TaskSnapshot, bool, error) {
	if h == nil || h.system == nil {
		return TaskSnapshot{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reply, err := h.system.Ref(actor.ActorID{Type: taskActorType, Key: id}).Ask(ctx, actor.Msg{
		Kind:       taskActorGet,
		Durability: actor.Ephemeral,
	}, taskActorAskTimeout)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	decoded, err := decodeTaskActorReply(reply.Payload)
	if err != nil {
		return TaskSnapshot{}, false, err
	}
	if decoded.Error != "" {
		return TaskSnapshot{}, false, fmt.Errorf("read task actor state: %s", decoded.Error)
	}
	return decoded.Task, decoded.Found, nil
}

func (h *taskActorHost) close() {
	if h == nil || h.system == nil {
		return
	}
	h.closeOnce.Do(func() {
		for _, id := range h.processes.activeIDs() {
			_, _, _ = h.stop(context.Background(), id, TaskInterrupted, "interrupted: task manager closed")
		}
		for _, process := range h.processes.stopAll() {
			_ = process.Stop()
		}
		h.updates.close()
		h.system.Stop()
	})
}

func decodeTaskActorReply(payload any) (taskActorReply, error) {
	var reply taskActorReply
	if err := decodeTaskActorPayload(payload, &reply); err != nil {
		return taskActorReply{}, err
	}
	return reply, nil
}

func decodeTaskActorPayload(payload, target any) error {
	if raw, ok := payload.(json.RawMessage); ok {
		return json.Unmarshal(raw, target)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func taskEventForStatus(status TaskStatus) string {
	switch status {
	case TaskCompleted:
		return taskEventCompleted
	case TaskFailed:
		return taskEventFailed
	case TaskStopped:
		return taskEventStopped
	case TaskInterrupted:
		return taskEventInterrupted
	default:
		return taskEventStarted
	}
}
