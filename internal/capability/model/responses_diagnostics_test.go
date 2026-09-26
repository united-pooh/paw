package model

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/message"
)

func TestResponsesAttemptDiagnosticsDescribeStreamTermination(t *testing.T) {
	const prefix = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"diagnostic-response\"}}\n\n"
	const tail = "data: private unfinished body"
	const malformed = "data: {private malformed frame}\n\n"
	const completed = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"private answer\"}]}]}}\n\n"
	for _, tc := range []struct {
		name, body, stage   string
		block, cancel, idle bool
		tailBytes           int
	}{
		{name: "success", body: prefix + completed, stage: "completed"},
		{name: "remote EOF with unfinished frame", body: prefix + tail, stage: "unexpected_eof", tailBytes: len(tail)},
		{name: "complete malformed frame then EOF", body: prefix + malformed, stage: "decode"},
		{name: "complete malformed frame then idle", body: prefix + malformed, stage: "decode", block: true, idle: true},
		{name: "true idle", body: prefix, stage: "stream_idle", block: true, idle: true},
		{name: "caller cancellation", body: prefix, stage: "canceled", block: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Encoding", "private header encoding")
				w.Header().Set("Authorization", "private response auth")
				fmt.Fprint(w, tc.body)
				w.(http.Flusher).Flush()
				if tc.block {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(WithUsageRequestID(context.Background(), "diagnostic-request"))
			defer cancel()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIKey: "private request auth", Model: "fixture", Stream: true, streamSet: true, RetryCountSet: true, RetryCount: 0, StreamIdleTimeout: 50 * time.Millisecond})
			var collected telemetryCollector
			done := make(chan struct{}, 1)
			client.SetRequestObserver(func(event RequestEvent) {
				collected.observe(event)
				if event.Kind == "attempt_stream_end" {
					done <- struct{}{}
				}
			})
			events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "private user prompt"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				time.AfterFunc(15*time.Millisecond, cancel)
			}
			for range events {
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("missing attempt_stream_end diagnostics")
			}
			var diagnosticEvents []RequestEvent
			for _, event := range collected.snapshot() {
				if event.Kind == "attempt_stream_end" {
					diagnosticEvents = append(diagnosticEvents, event)
				}
			}
			if len(diagnosticEvents) != 1 {
				t.Fatalf("diagnostic events = %d", len(diagnosticEvents))
			}
			event := diagnosticEvents[0]
			if event.Attempt != 1 || event.RequestID != "diagnostic-request" || event.ProviderResponseID != "diagnostic-response" {
				t.Fatalf("attempt identity lost: %+v", event)
			}
			wire, _ := json.Marshal(event)
			var decoded struct {
				Diagnostics struct {
					Stage           string `json:"failure_stage"`
					HTTPProtocol    string `json:"http_protocol"`
					ContentEncoding string `json:"content_encoding"`
					BodyBytes       int64  `json:"body_bytes"`
					LastByteAge     int64  `json:"last_byte_age_ms"`
					MaxReadWait     int64  `json:"max_read_wait_ms"`
					ReadEnd         string `json:"read_end"`
					TimedOut        bool   `json:"watchdog_timed_out"`
					Canceled        bool   `json:"context_canceled"`
					TailBytes       int    `json:"unterminated_sse_bytes"`
				} `json:"stream_diagnostics"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			d := decoded.Diagnostics
			if d.Stage != tc.stage || d.HTTPProtocol != "HTTP/1.1" || d.ContentEncoding != "other" || d.BodyBytes != int64(len(tc.body)) || d.TimedOut != tc.idle || d.Canceled != tc.cancel || d.TailBytes != tc.tailBytes {
				t.Fatalf("diagnostics = %s", wire)
			}
			if tc.idle && (d.MaxReadWait < 30 || d.LastByteAge < 30) {
				t.Fatalf("actual idle not measured: %s", wire)
			}
			if strings.Contains(tc.name, "EOF") && d.ReadEnd != "eof" {
				t.Fatalf("remote EOF lost behind failure stage: %s", wire)
			}
			for _, secret := range []string{"private", "Authorization", "Bearer", "malformed frame", "unfinished body"} {
				if strings.Contains(string(wire), secret) {
					t.Fatalf("diagnostic leaked content: %s", wire)
				}
			}
		})
	}
}

func TestResponsesAttemptDiagnosticsMeasureConsumerPauseAndKeepSinkError(t *testing.T) {
	for _, failSink := range []bool{false, true} {
		t.Run(fmt.Sprint(failSink), func(t *testing.T) {
			client := NewClient(Config{Transport: "openai-responses", Model: "fixture"})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			ctx, trace := client.startRequestTrace(context.Background(), client.CurrentModelConfig(), "sink-request", "openai-responses", nil)
			trace.base.Attempt = 1
			body := &diagnosticChunkBody{chunks: []string{
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"private delta\"}\r\n\r\n",
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"private delta\"}]}]}}\r\n\r\n",
			}}
			sinkErr := errors.New("private sink error")
			result := client.consumeResponsesAttempt(ctx, &http.Response{Proto: "HTTP/2.0", Header: http.Header{}, Body: body}, func(event StreamEvent) error {
				if event.Delta != "" {
					time.Sleep(60 * time.Millisecond)
					if failSink {
						return sinkErr
					}
				}
				return nil
			}, 20*time.Millisecond)
			if (failSink && !errors.Is(result.err, sinkErr)) || (!failSink && result.err != nil) {
				t.Fatalf("consumer error changed: %v", result.err)
			}
			var diagnostics []*ResponsesStreamDiagnostics
			for _, event := range collected.snapshot() {
				if event.Kind == "attempt_stream_end" {
					diagnostics = append(diagnostics, event.StreamDiagnostics)
				}
			}
			if len(diagnostics) != 1 {
				t.Fatalf("diagnostic count=%d", len(diagnostics))
			}
			d := diagnostics[0]
			stage := "completed"
			if failSink {
				stage = "consumer_error"
			}
			if d.FailureStage != stage || d.MaxConsumerPauseMS < 45 || d.WatchdogTimedOut || d.UnterminatedSSEBytes != 0 {
				t.Fatalf("consumer pause misclassified: %+v", d)
			}
			wire, _ := json.Marshal(d)
			if strings.Contains(string(wire), "private") {
				t.Fatal("sink error leaked")
			}
		})
	}
}

type diagnosticChunkBody struct{ chunks []string }

func (b *diagnosticChunkBody) Read(p []byte) (int, error) {
	if len(b.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.chunks[0])
	b.chunks[0] = b.chunks[0][n:]
	if len(b.chunks[0]) == 0 {
		b.chunks = b.chunks[1:]
	}
	return n, nil
}
func (*diagnosticChunkBody) Close() error { return nil }

type diagnosticSlowCloseBody struct {
	io.ReadCloser
	onClose func()
}

func (b *diagnosticSlowCloseBody) Close() error {
	b.onClose()
	time.Sleep(100 * time.Millisecond)
	return b.ReadCloser.Close()
}

func TestResponsesAttemptDiagnosticsSnapshotBeforeCleanup(t *testing.T) {
	client := NewClient(Config{Transport: "openai-responses", Model: "fixture"})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, trace := client.startRequestTrace(ctx, client.CurrentModelConfig(), "cleanup", "openai-responses", nil)
	trace.base.Attempt = 1
	body := &diagnosticSlowCloseBody{ReadCloser: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")), onClose: cancel}
	result := client.consumeResponsesAttempt(ctx, &http.Response{Proto: "HTTP/1.1", Header: http.Header{}, Body: body}, func(StreamEvent) error { return nil }, time.Second)
	if result.err != nil {
		t.Fatal(result.err)
	}
	var diagnostic *ResponsesStreamDiagnostics
	for _, event := range collected.snapshot() {
		if event.Kind == "attempt_stream_end" {
			diagnostic = event.StreamDiagnostics
		}
	}
	if diagnostic == nil || diagnostic.ContextCanceled || diagnostic.FailureStage != "completed" || diagnostic.LastByteAgeMS >= 50 || diagnostic.MaxConsumerPauseMS >= 50 {
		t.Fatalf("cleanup polluted terminal snapshot: %+v", diagnostic)
	}
}

func TestResponsesAttemptDiagnosticsPrecedeDoneAndKeepFinalSinkError(t *testing.T) {
	client := NewClient(Config{Transport: "openai-responses", Model: "fixture"})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	ctx, trace := client.startRequestTrace(context.Background(), client.CurrentModelConfig(), "done-order", "openai-responses", nil)
	trace.base.Attempt = 1
	body := &diagnosticSlowCloseBody{ReadCloser: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")), onClose: func() {}}
	sinkErr := errors.New("private terminal sink error")
	count := func() int {
		n := 0
		for _, event := range collected.snapshot() {
			if event.Kind == "attempt_stream_end" {
				n++
			}
		}
		return n
	}
	result := client.consumeResponsesAttempt(ctx, &http.Response{Proto: "HTTP/1.1", Header: http.Header{}, Body: body}, func(event StreamEvent) error {
		if event.Done {
			if count() != 1 {
				t.Error("Done delivered before stream diagnostics")
			}
			return sinkErr
		}
		return nil
	}, time.Second)
	if !errors.Is(result.err, sinkErr) || count() != 1 {
		t.Fatalf("final sink error changed or duplicated diagnostics: %v, %d", result.err, count())
	}
	for _, event := range collected.snapshot() {
		if event.Kind == "attempt_stream_end" && event.StreamDiagnostics.FailureStage != "completed" {
			t.Fatalf("final delivery overwrote validated terminal: %+v", event.StreamDiagnostics)
		}
	}
}

func TestResponsesAttemptDiagnosticsReportTransportDecompression(t *testing.T) {
	const body = "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, body)
		_ = writer.Close()
	}))
	defer server.Close()
	client := NewClient(Config{Transport: "openai-responses", Model: "fixture", APIBaseURL: server.URL, Stream: true, streamSet: true})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	if err := drainTelemetryStream(client, context.Background()); err != nil {
		t.Fatal(err)
	}
	var diagnostic *ResponsesStreamDiagnostics
	for _, event := range collected.snapshot() {
		if event.Kind == "attempt_stream_end" {
			diagnostic = event.StreamDiagnostics
		}
	}
	if diagnostic == nil || !diagnostic.Uncompressed || diagnostic.BodyBytes != int64(len(body)) {
		t.Fatalf("HTTP decompression lost: %+v", diagnostic)
	}
}

func TestResponsesAttemptDiagnosticsRemainIsolatedAcrossConcurrentRetries(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Fixture-Request")
		mu.Lock()
		counts[id]++
		attempt := counts[id]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":%q}}\n\n", fmt.Sprintf("%s-%d", id, attempt))
		if attempt == 1 {
			fmt.Fprint(w, "data: {broken}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	var collected telemetryCollector
	var wg sync.WaitGroup
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Headers: map[string]string{"X-Fixture-Request": id}, Stream: true, streamSet: true, RetryCount: 1, RetryCountSet: true})
			client.SetRequestObserver(collected.observe)
			if err := drainTelemetryStream(client, WithUsageRequestID(context.Background(), id)); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	diagnostics := map[string]map[int]RequestEvent{}
	for _, event := range collected.snapshot() {
		if event.Kind != "attempt_stream_end" {
			continue
		}
		if diagnostics[event.RequestID] == nil {
			diagnostics[event.RequestID] = map[int]RequestEvent{}
		}
		if _, exists := diagnostics[event.RequestID][event.Attempt]; exists {
			t.Fatal("duplicate stream end")
		}
		diagnostics[event.RequestID][event.Attempt] = event
	}
	for _, id := range []string{"first", "second"} {
		if len(diagnostics[id]) != 2 {
			t.Fatalf("missing attempts for %s: %+v", id, diagnostics)
		}
		for attempt, stage := range map[int]string{1: "decode", 2: "completed"} {
			event := diagnostics[id][attempt]
			if event.StreamDiagnostics.FailureStage != stage || event.ProviderResponseID != fmt.Sprintf("%s-%d", id, attempt) {
				t.Fatalf("crossed attempt diagnostics: %+v", event)
			}
		}
	}
}
