package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paw/internal/message"
)

type capturedModelRequest struct {
	Authorization string
	Marker        string
	Model         string
}

func TestHotReloadKeepsEachRequestOnOneImmutableSnapshot(t *testing.T) {
	oldArrived := make(chan capturedModelRequest, 1)
	releaseOld := make(chan struct{})
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		oldArrived <- capturedModelRequest{Authorization: r.Header.Get("Authorization"), Marker: r.Header.Get("X-Paw-Marker"), Model: body["model"].(string)}
		<-releaseOld
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"old-ok"}}]}`))
	}))
	defer oldServer.Close()

	newArrived := make(chan capturedModelRequest, 1)
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		newArrived <- capturedModelRequest{Authorization: r.Header.Get("Authorization"), Marker: r.Header.Get("X-Paw-Marker"), Model: body["model"].(string)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"new-ok"}}]}`))
	}))
	defer newServer.Close()

	client := NewClient(Config{Provider: "gateway", Transport: "openai-compatible", APIBaseURL: oldServer.URL, APIPath: "/chat/completions", APIKey: "old-key", Headers: map[string]string{"X-Paw-Marker": "old"}, Model: "old-model", Timeout: time.Second, RetryCount: 0, Stream: false, streamSet: true})
	oldResult := make(chan error, 1)
	go func() {
		_, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}})
		oldResult <- err
	}()

	first := <-oldArrived
	if err := client.ApplyModelConfig(Config{Provider: "gateway", Transport: "openai-compatible", APIBaseURL: newServer.URL, APIPath: "/chat/completions", APIKey: "new-key", Headers: map[string]string{"X-Paw-Marker": "new"}, Model: "new-model", Timeout: 10 * time.Millisecond, RetryCount: 0, Stream: false, streamSet: true}); err != nil {
		t.Fatal(err)
	}
	// If ApplyModelConfig mutated the old request's shared HTTP client, the
	// following delay would make it inherit the new 10ms timeout and fail.
	time.Sleep(40 * time.Millisecond)
	close(releaseOld)
	if err := <-oldResult; err != nil {
		t.Fatalf("old in-flight request failed after reload: %v", err)
	}
	if first.Authorization != "Bearer old-key" || first.Marker != "old" || first.Model != "old-model" {
		t.Fatalf("old request mixed snapshots: %#v", first)
	}

	result, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello again"}})
	if err != nil || result != "new-ok" {
		t.Fatalf("new request result=%q err=%v", result, err)
	}
	second := <-newArrived
	if second.Authorization != "Bearer new-key" || second.Marker != "new" || second.Model != "new-model" {
		t.Fatalf("new request mixed snapshots: %#v", second)
	}
}
