package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"paw/internal/plan"
	"paw/internal/session"
	"paw/internal/todo"
	"paw/internal/tool"
	"paw/internal/tool/memory"
	selecttool "paw/internal/tool/select"
	transcripttool "paw/internal/tool/transcript"
)

type Toolset struct {
	todo             *todo.Tool
	searchTranscript *transcripttool.Tool
	memory           *memory.UpdateMemoryTool
	ariadne          *memory.UpdateAriadneTool
	finalize         *plan.FinalizeTool
	question         *selecttool.Tool
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

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve memory home: %w", err)
	}
	memoryPath := filepath.Join(home, ".paw", "memory.md")
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
