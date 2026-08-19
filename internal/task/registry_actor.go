package task

import (
	"encoding/json"
	"fmt"
	"sort"

	"paw/internal/actor"
	"paw/internal/es"
	"paw/internal/settings"
)

const (
	taskRegistryActorType = "task-registry"
	taskRegistryActorKey  = "global"

	taskRegistryUpsert = "task.registry.upsert"
	taskRegistryList   = "task.registry.list"
	taskRegistryOwned  = "task.registry.owned"
	taskRegistryWait   = "task.registry.wait"
	taskRegistryTokens = "task.registry.tokens"

	taskRegistryUpdated = "task.registry.updated"
)

var taskRegistryActorID = actor.ActorID{Type: taskRegistryActorType, Key: taskRegistryActorKey}

type taskRegistryUpdate struct {
	Task TaskSnapshot `json:"task"`
}

type taskRegistryReply struct {
	Tasks []TaskSnapshot `json:"tasks,omitempty"`
	Error string         `json:"error,omitempty"`
}

type taskRegistryOwnedQuery struct {
	ParentSessionID string `json:"parent_session_id"`
	ParentTurnID    string `json:"parent_turn_id"`
}

type taskRegistryWaitQuery struct {
	IDs []string `json:"ids"`
}

type taskRegistryWaitReply struct {
	Result      WaitResult `json:"result"`
	AnyTerminal bool       `json:"any_terminal"`
	Error       string     `json:"error,omitempty"`
}

type taskRegistryTokensQuery struct {
	ParentSessionID string `json:"parent_session_id"`
}

type taskRegistryTokensReply struct {
	Total int    `json:"total"`
	Error string `json:"error,omitempty"`
}

type taskRegistryState struct {
	Tasks map[string]TaskSnapshot `json:"tasks"`
}

type taskIndexActor struct {
	id      actor.ActorID
	updates *taskUpdateBroker
	state   taskRegistryState
}

func newTaskIndexActor(id actor.ActorID, updates *taskUpdateBroker) *taskIndexActor {
	return &taskIndexActor{id: id, updates: updates, state: taskRegistryState{Tasks: make(map[string]TaskSnapshot)}}
}

func (a *taskIndexActor) ID() actor.ActorID { return a.id }

func (a *taskIndexActor) Receive(ctx *actor.Context, msg actor.Msg) {
	switch msg.Kind {
	case taskRegistryUpsert:
		var update taskRegistryUpdate
		if err := decodeTaskActorPayload(msg.Payload, &update); err != nil {
			return
		}
		if update.Task.ID == "" {
			return
		}
		if err := ctx.Persist(taskRegistryUpdated, update, actor.Durable); err != nil {
			return
		}
		a.ensureTasks()[update.Task.ID] = update.Task
		if isTerminalStatus(update.Task.Status) {
			a.updates.publish()
		}
	case taskRegistryList:
		tasks := make([]TaskSnapshot, 0, len(a.state.Tasks))
		for _, task := range a.state.Tasks {
			tasks = append(tasks, task)
		}
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].StartedAt.Before(tasks[j].StartedAt)
		})
		ctx.Reply(actor.Msg{Kind: taskRegistryList, Payload: taskRegistryReply{Tasks: tasks}, Durability: actor.Ephemeral})
	case taskRegistryOwned:
		var query taskRegistryOwnedQuery
		if err := decodeTaskActorPayload(msg.Payload, &query); err != nil {
			ctx.Reply(actor.Msg{Kind: taskRegistryOwned, Payload: taskRegistryReply{Error: err.Error()}, Durability: actor.Ephemeral})
			return
		}
		tasks := make([]TaskSnapshot, 0)
		for _, task := range a.state.Tasks {
			if task.Status == TaskRunning && task.RunMode == settings.RunModeBackground &&
				task.ParentSessionID == query.ParentSessionID && task.ParentTurnID == query.ParentTurnID {
				tasks = append(tasks, task)
			}
		}
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].StartedAt.Before(tasks[j].StartedAt)
		})
		ctx.Reply(actor.Msg{Kind: taskRegistryOwned, Payload: taskRegistryReply{Tasks: tasks}, Durability: actor.Ephemeral})
	case taskRegistryWait:
		var query taskRegistryWaitQuery
		if err := decodeTaskActorPayload(msg.Payload, &query); err != nil {
			ctx.Reply(actor.Msg{Kind: taskRegistryWait, Payload: taskRegistryWaitReply{Error: err.Error()}, Durability: actor.Ephemeral})
			return
		}
		result := WaitResult{Tasks: make([]TaskSummary, 0, len(query.IDs))}
		for _, id := range query.IDs {
			task, ok := a.state.Tasks[id]
			if !ok {
				result.Tasks = append(result.Tasks, TaskSummary{ID: id, Status: TaskNotFound, NotFound: true})
				continue
			}
			result.Tasks = append(result.Tasks, summarizeTask(task))
			if isTerminalStatus(task.Status) {
				result.AnyTerminal = true
			}
		}
		ctx.Reply(actor.Msg{Kind: taskRegistryWait, Payload: taskRegistryWaitReply{Result: result, AnyTerminal: result.AnyTerminal}, Durability: actor.Ephemeral})
	case taskRegistryTokens:
		var query taskRegistryTokensQuery
		if err := decodeTaskActorPayload(msg.Payload, &query); err != nil {
			ctx.Reply(actor.Msg{Kind: taskRegistryTokens, Payload: taskRegistryTokensReply{Error: err.Error()}, Durability: actor.Ephemeral})
			return
		}
		total := 0
		for _, task := range a.state.Tasks {
			if task.ParentSessionID == query.ParentSessionID {
				total += task.UsedTokens
			}
		}
		ctx.Reply(actor.Msg{Kind: taskRegistryTokens, Payload: taskRegistryTokensReply{Total: total}, Durability: actor.Ephemeral})
	}
}

func (a *taskIndexActor) Fold(env es.Envelope) error {
	if env.Type != taskRegistryUpdated {
		return nil
	}
	var update taskRegistryUpdate
	if err := json.Unmarshal(env.Payload, &update); err != nil {
		return fmt.Errorf("decode task registry event: %w", err)
	}
	if update.Task.ID != "" {
		a.ensureTasks()[update.Task.ID] = update.Task
	}
	return nil
}

func (a *taskIndexActor) Snapshot() (json.RawMessage, error) {
	return json.Marshal(a.state)
}

func (a *taskIndexActor) Restore(state json.RawMessage) error {
	if len(state) == 0 {
		return nil
	}
	if err := json.Unmarshal(state, &a.state); err != nil {
		return err
	}
	a.ensureTasks()
	return nil
}

func (a *taskIndexActor) State() any { return a.state }

func (a *taskIndexActor) ensureTasks() map[string]TaskSnapshot {
	if a.state.Tasks == nil {
		a.state.Tasks = make(map[string]TaskSnapshot)
	}
	return a.state.Tasks
}
