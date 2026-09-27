package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
)

func TestStreamActivityTracksOnlyIncrementalProviderEvents(t *testing.T) {
	cases := []struct{ name, transport, path, stream string }{
		{"chat", "openai-compatible", "/chat/completions", strings.Join([]string{
			`{"choices":[{"delta":{"reasoning_content":"reason"}}]}`,
			`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"Read","arguments":"{\"path\":\"a\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		}, "\n\ndata: ")},
		{"responses", "openai-responses", "/responses", strings.Join([]string{
			`{"type":"response.reasoning_text.delta","delta":"reason"}`,
			`{"type":"response.output_text.delta","delta":"answer"}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"item","type":"function_call","call_id":"call","name":"Read","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"a\"}"}`,
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},{"id":"item","type":"function_call","call_id":"call","name":"Read","arguments":"{\"path\":\"a\"}"}]}}`,
		}, "\n\ndata: ")},
		{"anthropic", "anthropic-compatible", "/messages", strings.Join([]string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call","name":"Read","input":{}}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}`,
			`{"type":"content_block_stop","index":2}`,
			`{"type":"message_stop"}`,
		}, "\n\ndata: ")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: "+tc.stream+"\n\n")
			}))
			defer srv.Close()
			client := NewClient(Config{Provider: "fixture", Transport: tc.transport, APIBaseURL: srv.URL, APIPath: tc.path, Model: "fixture", Timeout: time.Second})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var text, reasoning, arguments string
			var last time.Time
			for ev := range events {
				if ev.Err != nil {
					t.Fatal(ev.Err)
				}
				if ev.Activity == nil {
					continue
				}
				a := ev.Activity
				if a.At.IsZero() || a.At.Before(last) || ev.ReceivedAt.IsZero() {
					t.Fatalf("invalid arrival timestamp: %+v", ev)
				}
				last = a.At
				text += a.Text
				reasoning += a.Reasoning
				arguments += a.ToolArguments
			}
			if text != "answer" || reasoning != "reason" || arguments != `{"path":"a"}` {
				t.Fatalf("incremental activity text=%q reasoning=%q arguments=%q", text, reasoning, arguments)
			}
		})
	}
}

func TestStreamActivityDoesNotCountCompletedSnapshotFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer srv.Close()
	client := NewClient(Config{Provider: "fixture", Transport: "openai-responses", APIBaseURL: srv.URL, APIPath: "/responses", Model: "fixture", Timeout: time.Second})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for ev := range events {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Activity != nil {
			t.Fatal("completed snapshot fabricated streaming activity")
		}
		text += ev.Delta
	}
	if text != "answer" {
		t.Fatalf("fallback display lost: %q", text)
	}
}

func TestStreamActivityArrivesBeforeProviderCompletion(t *testing.T) {
	finish := make(chan struct{})
	released := false
	release := func() {
		if !released {
			close(finish)
			released = true
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"type":"response.reasoning_text.delta","delta":"still reasoning"}`+"\n\n")
		w.(http.Flusher).Flush()
		<-finish
		fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"still reasoning"}]}]}}`+"\n\n")
	}))
	defer srv.Close()
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := NewClient(Config{Provider: "fixture", Transport: "openai-responses", APIBaseURL: srv.URL, APIPath: "/responses", Model: "fixture", Timeout: time.Second})
	events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var arrived time.Time
	for arrived.IsZero() {
		select {
		case ev, ok := <-events:
			if !ok || ev.Err != nil || ev.Done {
				t.Fatalf("stream ended before live activity: %+v", ev)
			}
			if ev.Activity != nil {
				if ev.Activity.Reasoning != "still reasoning" {
					t.Fatalf("activity: %+v", ev.Activity)
				}
				arrived = ev.Activity.At
			}
		case <-ctx.Done():
			t.Fatal("reasoning activity waited for final response")
		}
	}
	release()
	sawDone := false
	for ev := range events {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Activity != nil {
			t.Fatal("completion duplicated activity")
		}
		if ev.Done {
			sawDone = true
			if ev.ReceivedAt.Before(arrived) {
				t.Fatal("end timestamp preceded activity")
			}
		}
	}
	if !sawDone {
		t.Fatal("missing normal completion")
	}
}
