package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"paw/internal/model"
)

func TestProcessPoolReusesWorkerAndKeepsRequestsIsolated(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolHelperProcess"},
		Env:           []string{"PAW_task_POOL_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 2,
	}
	defer launcher.Close()

	first, err := launcher.Start(context.Background(), WorkerRequest{
		TaskID:    "pool-task-1",
		SessionID: "pool-session-1",
		Prompt:    "first prompt",
	})
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := launcher.Start(context.Background(), WorkerRequest{
		TaskID:    "pool-task-2",
		SessionID: "pool-session-2",
		Prompt:    "second prompt",
	})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	firstResult, err := first.Wait()
	if err != nil {
		t.Fatalf("first Wait() error = %v result=%#v", err, firstResult)
	}
	secondResult, err := second.Wait()
	if err != nil {
		t.Fatalf("second Wait() error = %v result=%#v", err, secondResult)
	}
	if firstResult.Content != "first prompt|pool-session-1" {
		t.Fatalf("first result = %#v, want isolated first request", firstResult)
	}
	if secondResult.Content != "second prompt|pool-session-2" {
		t.Fatalf("second result = %#v, want isolated second request", secondResult)
	}
	if first.PID() <= 0 || second.PID() <= 0 {
		t.Fatalf("pool process PIDs = %d/%d, want live worker PID", first.PID(), second.PID())
	}
	if first.PID() != second.PID() {
		t.Fatalf("worker PID changed across sequential jobs: %d -> %d", first.PID(), second.PID())
	}
}

func TestProcessPoolReusesWorkerAfterTaskFailure(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolFailThenSucceedHelperProcess"},
		Env:           []string{"PAW_TASK_POOL_FAIL_THEN_SUCCEED_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	failed, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "fail-first", Prompt: "fail"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.Wait(); err == nil || !strings.Contains(err.Error(), "expected task failure") {
		t.Fatalf("failed Wait() error = %v", err)
	}
	failedPID := failed.PID()

	succeeded, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "succeed-second", Prompt: "succeed"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := succeeded.Wait()
	if err != nil || result.Content != "recovered" {
		t.Fatalf("succeeded Wait() = %#v, %v", result, err)
	}
	if succeeded.PID() != failedPID {
		t.Fatalf("worker PID changed after task failure: %d -> %d", failedPID, succeeded.PID())
	}
}

func TestTaskPoolFailThenSucceedHelperProcess(t *testing.T) {
	if os.Getenv("PAW_TASK_POOL_FAIL_THEN_SUCCEED_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(NewWorkerReadyMessage())
		case WorkerMessageStart:
			if message.Prompt == "fail" {
				_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{TaskID: message.TaskID, Error: "expected task failure", ExitCode: 1}))
			} else {
				_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{TaskID: message.TaskID, Content: "recovered", ExitCode: 0}))
			}
		case WorkerMessageCancel, WorkerMessageShutdown:
			return
		}
	}
}

func TestProcessPoolHonorsMaxWorkersAfterReusingIdleWorker(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolDelayedHelperProcess"},
		Env:           []string{"PAW_TASK_POOL_DELAYED_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 2,
	}
	defer launcher.Close()

	processes := make([]Process, 0, 3)
	for i := 0; i < 3; i++ {
		process, err := launcher.Start(context.Background(), WorkerRequest{
			TaskID: fmt.Sprintf("max-worker-%d", i),
			Prompt: fmt.Sprintf("job-%d", i),
		})
		if err != nil {
			t.Fatalf("Start(%d) error = %v", i, err)
		}
		processes = append(processes, process)
	}

	var pid int
	for i, process := range processes {
		if _, err := process.Wait(); err != nil {
			t.Fatalf("Wait(%d) error = %v", i, err)
		}
		if i == 0 {
			pid = process.PID()
		} else if process.PID() != pid {
			t.Fatalf("job %d ran on PID %d, want single MaxWorkers=1 PID %d", i, process.PID(), pid)
		}
	}
}

func TestTaskPoolDelayedHelperProcess(t *testing.T) {
	if os.Getenv("PAW_TASK_POOL_DELAYED_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(NewWorkerReadyMessage())
		case WorkerMessageStart:
			time.Sleep(100 * time.Millisecond)
			_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{
				TaskID: message.TaskID, Content: message.Prompt, ExitCode: 0,
			}))
		case WorkerMessageCancel, WorkerMessageShutdown:
			return
		}
	}
}

func TestProcessPoolCloseRejectsNewJobs(t *testing.T) {
	launcher := NewProcessPoolLauncher(os.Args[0], "")
	launcher.Args = []string{"-test.run=TestTaskPoolHelperProcess"}
	launcher.Env = []string{"PAW_task_POOL_HELPER=1"}
	if err := launcher.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := launcher.Start(context.Background(), WorkerRequest{Prompt: "after close"}); err == nil || !strings.Contains(err.Error(), "shut down") {
		t.Fatalf("Start() error = %v, want shut down error", err)
	}
}

func TestTaskPoolHelperProcess(t *testing.T) {
	if os.Getenv("PAW_task_POOL_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(WorkerMessage{Protocol: workerProtocolV2, Type: WorkerMessageReady})
		case WorkerMessageStart:
			_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{
				TaskID:    message.TaskID,
				SessionID: message.SessionID,
				Content:   message.Prompt + "|" + message.SessionID,
				ExitCode:  0,
			}))
		case WorkerMessageCancel, WorkerMessageShutdown:
			return
		}
	}
}

func TestProcessPoolReportsWorkerStderrOnUnexpectedExit(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolStderrHelperProcess"},
		Env:           []string{"PAW_TASK_POOL_STDERR_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	process, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "stderr-task", SessionID: "stderr-session"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, waitErr := process.Wait()
	if waitErr == nil || !strings.Contains(waitErr.Error(), "worker stderr sentinel") {
		t.Fatalf("Wait() = %#v, %v, want captured stderr", result, waitErr)
	}
}

func TestTaskPoolStderrHelperProcess(t *testing.T) {
	if os.Getenv("PAW_TASK_POOL_STDERR_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	var message WorkerMessage
	if err := decoder.Decode(&message); err != nil {
		return
	}
	_ = encoder.Encode(NewWorkerReadyMessage())
	if err := decoder.Decode(&message); err != nil {
		return
	}
	_, _ = os.Stderr.WriteString("worker stderr sentinel\n")
	os.Exit(23)
}

func TestProcessPoolPreservesPartialEventsWhenWorkerReturnsFailure(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolPartialFailureHelperProcess"},
		Env:           []string{"PAW_TASK_POOL_PARTIAL_FAILURE_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	process, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "partial-failure", SessionID: "partial-session"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, waitErr := process.Wait()
	if waitErr == nil || !strings.Contains(waitErr.Error(), "provider stream failed") {
		t.Fatalf("Wait() = %#v, %v, want provider failure", result, waitErr)
	}
	if result.Content != "partial answer" || result.UsedTokens != 23 || result.Usage == nil {
		t.Fatalf("partial failure result = %#v", result)
	}
}

func TestTaskPoolPartialFailureHelperProcess(t *testing.T) {
	if os.Getenv("PAW_TASK_POOL_PARTIAL_FAILURE_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(NewWorkerReadyMessage())
		case WorkerMessageStart:
			_ = encoder.Encode(NewWorkerEventMessage(message.TaskID, WorkerStreamEvent{Delta: "partial answer"}))
			_ = encoder.Encode(NewWorkerEventMessage(message.TaskID, WorkerStreamEvent{Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 3}}))
			_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{
				TaskID: message.TaskID, SessionID: message.SessionID, Error: "provider stream failed", ExitCode: 1,
			}))
		case WorkerMessageCancel, WorkerMessageShutdown:
			return
		}
	}
}

func TestProcessPoolPreservesPartialEventsWhenStopped(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolPartialBlockingHelperProcess"},
		Env:           []string{"PAW_TASK_POOL_PARTIAL_BLOCKING_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	process, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "partial-stop", SessionID: "partial-session"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		source, ok := process.(ProcessPartialResultSource)
		if !ok {
			t.Fatal("process does not expose partial result")
		}
		if source.PartialResult().Content == "partial before stop" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial event was not received")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	result, waitErr := process.Wait()
	if waitErr == nil || !strings.Contains(waitErr.Error(), "canceled") {
		t.Fatalf("Wait() = %#v, %v, want cancellation", result, waitErr)
	}
	if result.Content != "partial before stop" {
		t.Fatalf("stopped result = %#v, want partial content", result)
	}
}

func TestTaskPoolPartialBlockingHelperProcess(t *testing.T) {
	if os.Getenv("PAW_TASK_POOL_PARTIAL_BLOCKING_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(NewWorkerReadyMessage())
		case WorkerMessageStart:
			_ = encoder.Encode(NewWorkerEventMessage(message.TaskID, WorkerStreamEvent{Delta: "partial before stop"}))
		case WorkerMessageCancel, WorkerMessageShutdown:
			return
		}
	}
}

func TestPoolJobRecordsPartialEvents(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	job := &poolJob{ctx: ctx, cancel: cancel, req: WorkerRequest{TaskID: "partial-task", SessionID: "partial-session"}}
	job.recordEvent(WorkerStreamEvent{Delta: "first "})
	job.recordEvent(WorkerStreamEvent{Delta: "second"})
	job.recordEvent(WorkerStreamEvent{Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 3}})

	result := job.partialResult()
	if result.Content != "first second" || result.UsedTokens != 23 || result.Usage == nil {
		t.Fatalf("partial result = %#v", result)
	}
}

func TestProcessPoolCanceledQueuedJobReturnsPromptly(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolBlockingHelperProcess"},
		Env:           []string{"PAW_task_POOL_BLOCKING_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	first, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "blocking-1", Prompt: "first"})
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "blocking-2", Prompt: "second"})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := second.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		_, err := second.Wait()
		waitDone <- err
	}()
	select {
	case err := <-waitDone:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("second Wait() error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued job did not finish promptly")
	}
	_ = first.Stop()
}

func TestTaskPoolBlockingHelperProcess(t *testing.T) {
	if os.Getenv("PAW_task_POOL_BLOCKING_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Type {
		case WorkerMessageHello:
			_ = encoder.Encode(WorkerMessage{Protocol: workerProtocolV2, Type: WorkerMessageReady})
		case WorkerMessageStart:
			// Keep the worker occupied until the parent cancels/kills it.
			for {
				if err := decoder.Decode(&message); err != nil {
					return
				}
				if message.Type == WorkerMessageCancel || message.Type == WorkerMessageShutdown {
					return
				}
			}
		}
	}
}

func TestProcessPoolCloseCancelsQueuedJobs(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolBlockingHelperProcess"},
		Env:           []string{"PAW_task_POOL_BLOCKING_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}

	first, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "close-1", Prompt: "first"})
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "close-2", Prompt: "second"})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- launcher.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return promptly")
	}

	for name, process := range map[string]struct {
		process Process
	}{
		"first":  {process: first},
		"second": {process: second},
	} {
		waitDone := make(chan error, 1)
		go func() {
			_, err := process.process.Wait()
			waitDone <- err
		}()
		select {
		case err := <-waitDone:
			if err == nil || (!strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "task process pool closed")) {
				t.Errorf("%s Wait() error = %v, want cancellation cause", name, err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s Wait() did not return promptly", name)
		}
	}
}

var _ = time.Second

func TestRandomPersonaOrderIsPermutation(t *testing.T) {
	order := randomPersonaOrder(12)
	if len(order) != 12 {
		t.Fatalf("randomPersonaOrder() len = %d, want 12: %#v", len(order), order)
	}
	seen := make(map[int]bool, len(order))
	for _, index := range order {
		if index < 0 || index >= len(order) || seen[index] {
			t.Fatalf("randomPersonaOrder() is not a permutation: %#v", order)
		}
		seen[index] = true
	}
}

func TestNextWorkerRoleUsesRandomizedOrderWithoutDuplicates(t *testing.T) {
	previous := defaultPersonas
	defaultPersonas = []persona{
		{Name: "角色A", Color: "#111111"},
		{Name: "角色B", Color: "#222222"},
		{Name: "角色C", Color: "#333333"},
	}
	t.Cleanup(func() { defaultPersonas = previous })

	launcher := &ProcessPoolLauncher{roleOrder: []int{2, 0, 1}}
	got := make([]string, 0, len(defaultPersonas))
	for range defaultPersonas {
		got = append(got, launcher.nextWorkerRoleLocked().Name)
	}
	want := []string{"角色C", "角色A", "角色B"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("worker roles = %#v, want %#v", got, want)
		}
	}
}

func TestPopIdleWorkerUsesChosenRandomIndex(t *testing.T) {
	first := &poolWorker{roleName: "first"}
	second := &poolWorker{roleName: "second"}
	third := &poolWorker{roleName: "third"}

	worker, remaining := popIdleWorker([]*poolWorker{first, second, third}, 1)
	if worker != second {
		t.Fatalf("popIdleWorker() = %v, want second worker", worker)
	}
	if len(remaining) != 2 || remaining[0] != first || remaining[1] != third {
		t.Fatalf("remaining workers = %#v, want first/third", remaining)
	}
}

func TestPoolWorkersGetUniqueNamedRoles(t *testing.T) {
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolHelperProcess"},
		Env:           []string{"PAW_task_POOL_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 1,
	}
	defer launcher.Close()

	first, err := launcher.newPoolWorker()
	if err != nil {
		t.Fatalf("first newPoolWorker() error = %v", err)
	}
	defer first.stop()
	second, err := launcher.newPoolWorker()
	if err != nil {
		t.Fatalf("second newPoolWorker() error = %v", err)
	}
	defer second.stop()

	if first.roleName == "" || first.roleColor == "" {
		t.Fatalf("first worker role = %q/%q, want non-empty", first.roleName, first.roleColor)
	}
	if first.roleName == second.roleName {
		t.Fatalf("pool workers share role %q, want unique per worker", first.roleName)
	}
	known := false
	for _, persona := range defaultPersonas {
		if persona.Name == first.roleName {
			known = true
			break
		}
	}
	if !known {
		t.Fatalf("worker role %q is not from the persona pool", first.roleName)
	}

	// WorkerRole 经 poolJobProcess 透传给任务。
	job := &poolJob{ctx: context.Background(), cancel: func(error) {}, req: WorkerRequest{TaskID: "unique-role-1"}}
	job.setWorker(first)
	process := &poolJobProcess{job: job}
	if name, color := process.WorkerRole(); name != first.roleName || color != first.roleColor {
		t.Fatalf("WorkerRole() = %q/%q, want %q/%q", name, color, first.roleName, first.roleColor)
	}
}

func TestPoolJobProcessWorkerRoleWaitsForWorkerBinding(t *testing.T) {
	job := &poolJob{ctx: context.Background(), cancel: func(error) {}, req: WorkerRequest{TaskID: "pending-binding"}}
	process := &poolJobProcess{job: job}

	done := make(chan struct{})
	go func() {
		name, _ := process.WorkerRole()
		if name != "高松灯" {
			t.Errorf("WorkerRole() = %q, want 高松灯", name)
		}
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	job.setWorker(&poolWorker{roleName: "高松灯", roleColor: "#CC0033"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WorkerRole() did not resolve after worker binding")
	}
}
