package tokentracer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/message"
)

func TestLedgerPersistsResponsesStreamDiagnosticsPerAttempt(t *testing.T) {
	home := t.TempDir()
	r, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, raw := range []string{
		`{"kind":"request_start","request_id":"logical"}`,
		`{"kind":"attempt_start","request_id":"logical","attempt":1,"transport":"openai-responses"}`,
		`{"kind":"attempt_stream_end","request_id":"logical","attempt":1,"status":"failed","stream_diagnostics":{"failure_stage":"decode","cause":"json_syntax","http_protocol":"HTTP/1.1","content_encoding":"gzip","transfer_encoding":"chunked","body_bytes":417,"last_byte_age_ms":90,"max_read_wait_ms":90,"max_consumer_pause_ms":30,"watchdog_timed_out":true,"unterminated_sse_bytes":12}}`,
		`{"kind":"attempt_start","request_id":"logical","attempt":2,"transport":"openai-responses"}`,
		`{"kind":"attempt_stream_end","request_id":"logical","attempt":2,"status":"completed","stream_diagnostics":{"failure_stage":"completed","body_bytes":521,"http_protocol":"HTTP/2.0"}}`,
		`{"kind":"request_end","request_id":"logical","attempt":2,"status":"completed"}`,
	} {
		var event model.RequestEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if err := r.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.dir, "events-000001.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"stream_diagnostics"`) != 2 || !strings.Contains(string(data), `"failure_stage":"decode"`) {
		t.Fatalf("JSONL lost diagnostics: %s", data)
	}
	for i := 0; i < 2; i++ {
		detail, err := NewLedgerReader(home).Request(r.InstanceID(), "logical")
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(detail)
		var view struct {
			Attempts []struct {
				Number      int `json:"number"`
				Diagnostics struct {
					Stage     string `json:"failure_stage"`
					BodyBytes int    `json:"body_bytes"`
				} `json:"stream_diagnostics"`
			} `json:"attempts"`
		}
		if err := json.Unmarshal(wire, &view); err != nil {
			t.Fatal(err)
		}
		if len(view.Attempts) != 2 || view.Attempts[0].Number != 1 || view.Attempts[0].Diagnostics.Stage != "decode" || view.Attempts[0].Diagnostics.BodyBytes != 417 || view.Attempts[1].Number != 2 || view.Attempts[1].Diagnostics.Stage != "completed" || view.Attempts[1].Diagnostics.BodyBytes != 521 {
			t.Fatalf("reader lost attempt diagnostics: %s", wire)
		}
	}
	reader := NewLedgerReader(home)
	first, err := reader.Request(r.InstanceID(), "logical")
	if err != nil {
		t.Fatal(err)
	}
	first.Attempts[0].StreamDiagnostics.FailureStage = "caller mutation"
	again, err := reader.Request(r.InstanceID(), "logical")
	if err != nil {
		t.Fatal(err)
	}
	if again.Attempts[0].StreamDiagnostics.FailureStage != "decode" {
		t.Fatal("caller mutated cached diagnostics")
	}
}

func TestLedgerHasResponsesDiagnosticsWhenDoneClosesRecorder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	home := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	client := model.NewClient(model.Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture"})
	client.SetRequestObserver(func(event model.RequestEvent) {
		if err := recorder.Record(event); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(model.WithUsageRequestID(context.Background(), "immediate-close"))
	defer cancel()
	events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "private synthetic prompt"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sawDone := false
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Done {
			sawDone = true
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if !sawDone {
		t.Fatal("missing Done")
	}
	detail, err := NewLedgerReader(home).Request(recorder.InstanceID(), "immediate-close")
	if err != nil {
		t.Fatal(err)
	}
	if detail == nil || len(detail.Attempts) != 1 || detail.Attempts[0].StreamDiagnostics == nil || detail.Attempts[0].StreamDiagnostics.FailureStage != "completed" {
		t.Fatalf("Done consumer lost diagnostics: %+v", detail)
	}
}
