package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const tasksDirName = "tasks"

type taskRegistry struct {
	root string
}

func newTaskRegistry(root string) taskRegistry {
	return taskRegistry{root: root}
}

func (r taskRegistry) outputPath(taskID string) string {
	return filepath.Join(r.taskDir(taskID), "output.json")
}

func (r taskRegistry) saveTask(ctx context.Context, task TaskSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("task id is empty")
	}
	if task.OutputPath == "" {
		task.OutputPath = r.outputPath(task.ID)
	}
	if err := os.MkdirAll(r.taskDir(task.ID), 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(r.metaPath(task.ID), data, 0o600); err != nil {
		return fmt.Errorf("write task meta: %w", err)
	}
	return nil
}

func (r taskRegistry) saveOutput(ctx context.Context, taskID string, result WorkerResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task id is empty")
	}
	if err := os.MkdirAll(r.taskDir(taskID), 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task output: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(r.outputPath(taskID), data, 0o600); err != nil {
		return fmt.Errorf("write task output: %w", err)
	}
	return nil
}

func (r taskRegistry) loadTask(ctx context.Context, taskID string) (TaskSnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return TaskSnapshot{}, false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return TaskSnapshot{}, false, fmt.Errorf("task id is empty")
	}
	data, err := os.ReadFile(r.metaPath(taskID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TaskSnapshot{}, false, nil
		}
		return TaskSnapshot{}, false, err
	}
	var task TaskSnapshot
	if err := json.Unmarshal(data, &task); err != nil {
		return TaskSnapshot{}, false, fmt.Errorf("parse task meta: %w", err)
	}
	if task.ID == "" {
		task.ID = taskID
	}
	if task.OutputPath == "" {
		task.OutputPath = r.outputPath(task.ID)
	}
	return task, true, nil
}

func (r taskRegistry) listTasks(ctx context.Context) ([]TaskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.tasksDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	tasks := make([]TaskSnapshot, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		task, ok, err := r.loadTask(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func (r taskRegistry) tasksDir() string {
	return filepath.Join(r.root, ".paw", tasksDirName)
}

func (r taskRegistry) taskDir(taskID string) string {
	return filepath.Join(r.tasksDir(), taskID)
}

func (r taskRegistry) metaPath(taskID string) string {
	return filepath.Join(r.taskDir(taskID), "meta.json")
}
