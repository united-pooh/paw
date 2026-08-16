package task

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
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
			if err == nil || !strings.Contains(err.Error(), "canceled") {
				t.Errorf("%s Wait() error = %v, want cancellation", name, err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s Wait() did not return promptly", name)
		}
	}
}

var _ = time.Second
