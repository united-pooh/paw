package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"paw/internal/capability/tool"
	"paw/internal/capability/tool/memory"
	selecttool "paw/internal/capability/tool/select"
	transcripttool "paw/internal/capability/tool/transcript"
	"paw/internal/message"
	"paw/internal/platform/pawpath"
	"paw/internal/runtime/goal"
	"paw/internal/runtime/plan"
	"paw/internal/storage/session"
	"paw/internal/todo"
)

type Toolset struct {
	todo                *todo.Tool
	searchTranscript    *transcripttool.Tool
	memory              *memory.UpdateMemoryTool
	ariadne             *memory.UpdateAriadneTool
	finalize            *plan.FinalizeTool
	question            *selecttool.Tool
	interactiveRegistry *tool.Registry
}

func NewToolset(broker *todo.Broker) *Toolset {
	return &Toolset{
		todo:             todo.NewTool(broker),
		searchTranscript: transcripttool.New(nil, ""),
		memory:           memory.NewUpdateMemory("", nil),
		ariadne:          memory.NewUpdateAriadne("", nil),
		finalize:         plan.NewFinalizeTool(nil),
	}
}

func (t *Toolset) RegisterMain(registry *tool.Registry) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if t == nil {
		return fmt.Errorf("toolset is nil")
	}
	registry.Register(t.todo)
	registry.Register(t.searchTranscript)
	registry.Register(t.memory)
	registry.Register(t.ariadne)
	return nil
}

func (t *Toolset) RegisterInteractive(registry *tool.Registry, broker *selecttool.Broker) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if broker == nil {
		return fmt.Errorf("selection broker is nil")
	}
	if t == nil {
		return fmt.Errorf("toolset is nil")
	}
	t.question = selecttool.New(broker)
	registry.Register(t.question)
	registry.Register(t.finalize)
	t.interactiveRegistry = registry
	return nil
}

func (a *WorkspaceRuntime) BindGoalController(controller *goal.SessionController) error {
	if a == nil || a.SessionHost == nil || a.Store == nil || a.Toolset == nil || a.Toolset.interactiveRegistry == nil || controller == nil {
		return fmt.Errorf("goal controller requires an interactive runtime and session store")
	}
	controller.SetHistory(func(ctx context.Context) ([]message.Message, error) {
		snapshot, err := a.Store.LoadSnapshot(ctx, a.SessionHost.CurrentSessionID())
		if err != nil {
			return nil, err
		}
		return snapshot.ActiveHistory, nil
	})
	a.Toolset.interactiveRegistry.Register(goal.NewSetMetricTool(controller))
	a.Toolset.interactiveRegistry.Register(goal.NewRecordMetricTool(controller))
	return nil
}

func (t *Toolset) BindSession(store *session.JSONLStore, sessionID, progressPath string) error {
	if t == nil {
		return fmt.Errorf("toolset is nil")
	}
	if store == nil {
		return fmt.Errorf("session store is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session ID is empty")
	}
	var archive *todo.ArchiveWriter
	if strings.TrimSpace(progressPath) != "" {
		var err error
		archive, err = todo.NewArchiveWriter(progressPath)
		if err != nil {
			return err
		}
	}
	t.todo.OnUpsert = func(ctx context.Context, snapshot todo.Snapshot) error {
		if archive != nil {
			_, _ = archive.ArchiveCompleted(ctx, snapshot)
		}
		_, err := store.AppendTodoSnapshot(ctx, sessionID, snapshot)
		return err
	}
	t.searchTranscript.Bind(store, sessionID)

	home, err := pawpath.Home()
	if err != nil {
		return fmt.Errorf("resolve memory home: %w", err)
	}
	memoryPath := filepath.Join(home, "memory.md")
	ariadnePath := filepath.Join(store.Root(), "sessions", sessionID, "ariadne.md")
	record := func(ctx context.Context, kind session.StateEventKind, summary string) error {
		_, err := store.AppendStateEvent(ctx, sessionID, kind, summary)
		return err
	}
	t.memory.Bind(memoryPath, record)
	t.ariadne.Bind(ariadnePath, record)
	return nil
}

func (t *Toolset) Todo() *todo.Tool {
	if t == nil {
		return nil
	}
	return t.todo
}

func (t *Toolset) SearchTranscript() *transcripttool.Tool {
	if t == nil {
		return nil
	}
	return t.searchTranscript
}

func (t *Toolset) Memory() *memory.UpdateMemoryTool {
	if t == nil {
		return nil
	}
	return t.memory
}

func (t *Toolset) Ariadne() *memory.UpdateAriadneTool {
	if t == nil {
		return nil
	}
	return t.ariadne
}

func (t *Toolset) Finalize() *plan.FinalizeTool {
	if t == nil {
		return nil
	}
	return t.finalize
}
