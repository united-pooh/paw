package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestResponsesRetriesInterruptionBeforePublishedOutput(t *testing.T) {
	for _, mode := range []string{"empty", "buffered_tool"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				if requests == 1 {
					if mode == "buffered_tool" {
						fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"partial","call_id":"partial","name":"Write","arguments":"{}"}}`+"\n\n")
					}
					return
				}
				fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"final","name":"Read","arguments":"{}"}]}}`+"\n\n")
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 2, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls []message.ToolCall
			done := 0
			for event := range events {
				if event.Err != nil {
					t.Fatal(event.Err)
				}
				if event.Delta != "" || event.Thinking != "" {
					t.Fatalf("unexpected output: %+v", event)
				}
				calls = append(calls, event.ToolCalls...)
				if event.Done {
					done++
				}
			}
			if requests != 2 || len(calls) != 1 || calls[0].ID != "final" || done != 1 {
				t.Fatalf("requests=%d calls=%v done=%d", requests, calls, done)
			}
		})
	}
}

type responsesChunkReader struct{ chunks []string }

func (r *responsesChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func TestResponsesSSECRLFAcrossReadBoundaries(t *testing.T) {
	scanner := bufio.NewScanner(&responsesChunkReader{chunks: []string{"event: response.completed\r", "\ndata: {\"type\":\r", "\ndata: \"response.completed\"}\r", "\n\r", "\n"}})
	scanner.Split(splitResponsesSSEEvents)
	var payloads []string
	for scanner.Scan() {
		if payload, ok := responsesSSEPayload(scanner.Bytes()); ok {
			payloads = append(payloads, string(payload))
		}
	}
	if scanner.Err() != nil || len(payloads) != 1 || payloads[0] != "{\"type\":\n\"response.completed\"}" {
		t.Fatalf("payloads=%q err=%v", payloads, scanner.Err())
	}
}

func TestResponsesFailureIncludesDiagnosticClassification(t *testing.T) {
	failure := &responsesStreamFailure{EventType: "stream_read", Message: "读取 Responses 流式响应失败", RequestID: "req-test", Retryable: true, cause: io.ErrUnexpectedEOF}
	text := failure.Error()
	for _, want := range []string{"stream_read", "unexpected EOF", "req-test"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if !errors.Is(failure, io.ErrUnexpectedEOF) {
		t.Fatal("lost cause")
	}
}
