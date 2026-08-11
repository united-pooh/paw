package main

import (
	"bytes"
	"encoding/json"
	configv2 "paw/internal/config"
	"paw/internal/subagent"
	"strings"
	"testing"
)

func TestReadSubagentWorkerStartBuildsWorkerRuntimeContext(t *testing.T) {
	tests := []struct {
		name     string
		depth    int
		maxDepth int
	}{
		{name: "first-level worker", depth: 1, maxDepth: 4},
		{name: "delegated worker", depth: 3, maxDepth: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantReq := subagent.WorkerRequest{
				TaskID:    "task-1",
				SessionID: "session-1",
				Depth:     tt.depth,
				MaxDepth:  tt.maxDepth,
			}
			decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, wantReq)))
			start, gotReq, subCtx, err := readSubagentWorkerStart(decoder)
			if err != nil {
				t.Fatalf("readSubagentWorkerStart: %v", err)
			}
			if start.Type != subagent.WorkerMessageStart {
				t.Fatalf("message type = %q, want %q", start.Type, subagent.WorkerMessageStart)
			}
			if gotReq.Depth != wantReq.Depth || gotReq.MaxDepth != wantReq.MaxDepth {
				t.Fatalf("decoded depth = %d/%d, want %d/%d", gotReq.Depth, gotReq.MaxDepth, wantReq.Depth, wantReq.MaxDepth)
			}
			if !subCtx.workerMode {
				t.Fatal("valid worker start did not set workerMode")
			}
			if subCtx.depth != wantReq.Depth || subCtx.maxDepth != wantReq.MaxDepth {
				t.Fatalf("runtime depth = %d/%d, want %d/%d", subCtx.depth, subCtx.maxDepth, wantReq.Depth, wantReq.MaxDepth)
			}
			if subCtx.parentTaskID != wantReq.TaskID {
				t.Fatalf("parentTaskID = %q, want %q", subCtx.parentTaskID, wantReq.TaskID)
			}
			if !subCtx.disableMainTodo {
				t.Fatal("worker runtime context did not disable the main todo tool")
			}
		})
	}
}

func TestReadSubagentWorkerStartRejectsInvalidDepth(t *testing.T) {
	tests := []struct {
		name        string
		depth       int
		maxDepth    int
		wantMessage string
	}{
		{name: "zero max depth", depth: 1, maxDepth: 0, wantMessage: "max_depth must be at least 1"},
		{name: "negative max depth", depth: 1, maxDepth: -1, wantMessage: "max_depth must be at least 1"},
		{name: "zero depth", depth: 0, maxDepth: 4, wantMessage: "depth must satisfy"},
		{name: "negative depth", depth: -1, maxDepth: 4, wantMessage: "depth must satisfy"},
		{name: "too deep", depth: 5, maxDepth: 4, wantMessage: "depth must satisfy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := subagent.WorkerRequest{
				TaskID:    "task-1",
				SessionID: "session-1",
				Depth:     tt.depth,
				MaxDepth:  tt.maxDepth,
			}
			decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, req)))
			_, _, _, err := readSubagentWorkerStart(decoder)
			if err == nil {
				t.Fatal("readSubagentWorkerStart succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantMessage)
			}
		})
	}
}

func TestReadSubagentWorkerStartRejectsMalformedDepth(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"type":"worker.start","task_id":"task-1","session_id":"session-1","depth":"one","max_depth":4}`))
	_, _, _, err := readSubagentWorkerStart(decoder)
	if err == nil {
		t.Fatal("readSubagentWorkerStart succeeded")
	}
	if !strings.Contains(err.Error(), "decode subagent worker.start") || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerStartToConfigOpenOptionsProductionPath(t *testing.T) {
	wantReq := subagent.WorkerRequest{
		TaskID:    "delegated-task",
		SessionID: "delegated-session",
		Depth:     2,
		MaxDepth:  4,
	}
	decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, wantReq)))
	_, _, subCtx, err := readSubagentWorkerStart(decoder)
	if err != nil {
		t.Fatalf("readSubagentWorkerStart: %v", err)
	}
	paths := configv2.Paths{Home: "/tmp/paw", WorkspaceRoot: "/tmp/workspace"}
	options := configOpenOptions(paths, subCtx)
	if options.Paths != paths {
		t.Fatalf("paths changed: got %#v want %#v", options.Paths, paths)
	}
	if !options.DisableModelDiscovery {
		t.Fatal("production worker start path left discovery enabled")
	}
}

func mustWorkerStartJSON(t *testing.T, req subagent.WorkerRequest) []byte {
	t.Helper()
	payload, err := json.Marshal(subagent.NewWorkerStartMessage(req, req.MCPSnapshot))
	if err != nil {
		t.Fatalf("marshal worker start: %v", err)
	}
	return append(payload, '\n')
}
