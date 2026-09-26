package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/message"
)

func TestResponsesInterruptedOutputDoesNotRegenerate(t *testing.T) {
	for _, channel := range []string{"output_text", "reasoning_summary_text"} {
		for _, interruption := range []string{"unexpected_eof", "stream_read", "stream_idle"} {
			t.Run(channel+"/"+interruption, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("X-Request-Id", "original-attempt")
					if requests.Add(1) > 1 {
						fmt.Fprintf(w, "data: {\"type\":\"response.%s.delta\",\"delta\":\"regenerated\"}\n\n", channel)
						return
					}
					fmt.Fprintf(w, "data: {\"type\":\"response.%s.delta\",\"delta\":\"已输出\"}\n\n", channel)
					fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"uncommitted\",\"name\":\"Write\",\"arguments\":\"{}\"}}\n\n")
					w.(http.Flusher).Flush()
					switch interruption {
					case "stream_read":
						panic(http.ErrAbortHandler)
					case "stream_idle":
						<-r.Context().Done()
					}
				}))
				defer server.Close()
				client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 2, RetryCountSet: true, StreamIdleTimeout: 100 * time.Millisecond})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				var text, thinking string
				var gotErr error
				for event := range events {
					text += event.Delta
					thinking += event.Thinking
					if event.Done || len(event.ToolCalls) > 0 || len(event.ProviderData) > 0 {
						t.Fatalf("interrupted response committed: %#v", event)
					}
					if event.Err != nil {
						gotErr = event.Err
					}
				}
				var failure *responsesStreamFailure
				if requests.Load() != 1 || !errors.As(gotErr, &failure) || failure.EventType != interruption {
					t.Fatalf("requests=%d error=%v; want one request and original %s", requests.Load(), gotErr, interruption)
				}
				if failure.RequestID != "original-attempt" || failure.Retryable {
					t.Fatalf("failure lost request identity or allows unsafe retry: %+v", failure)
				}
				if interruption == "stream_read" && !errors.Is(gotErr, io.ErrUnexpectedEOF) {
					t.Fatalf("lost original network error cause: %v", gotErr)
				}
				if failure.publishedTextBytes != len(text) || failure.publishedThinkingBytes != len(thinking) {
					t.Fatalf("incorrect published byte counts: %+v", failure)
				}
				if text+thinking != "已输出" || (channel == "output_text" && thinking != "") || (channel == "reasoning_summary_text" && text != "") {
					t.Fatalf("text=%q thinking=%q", text, thinking)
				}
				if !strings.Contains(gotErr.Error(), "继续") || !strings.Contains(gotErr.Error(), "published_") || strings.Contains(gotErr.Error(), "regenerated") {
					t.Fatalf("missing safe recovery diagnostics: %v", gotErr)
				}
			})
		}
	}
}
