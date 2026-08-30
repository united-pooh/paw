package bubble

import taskpkg "paw/internal/task"

func (m appModel) runningTasks() []taskpkg.TaskSnapshot {
	if m.taskController == nil {
		return nil
	}
	if active, ok := m.taskController.(ActiveTaskController); ok {
		return active.ActiveTasks()
	}
	tasks := m.taskController.ListTasks()
	running := make([]taskpkg.TaskSnapshot, 0, len(tasks))
	for _, snapshot := range tasks {
		if snapshot.Status == taskpkg.TaskRunning {
			running = append(running, snapshot)
		}
	}
	return running
}

func (m appModel) hasRunningTasks() bool {
	return len(m.runningTasks()) > 0
}
