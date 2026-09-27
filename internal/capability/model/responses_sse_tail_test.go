package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestResponsesTruncatedJSONTailRetainsTransportClassification(t *testing.T) {
	for _, ending := range []string{"unexpected_eof", "stream_read", "stream_idle"} {
		for _, framing := range []struct {
			name       string
			tail       string
			prefix     string
			wantDecode bool
		}{
			{name: "unterminated partial JSON", tail: `data: {"type":"response.output_text.delta","delta":"unfinished`},
			{name: "single line ending is not event boundary", tail: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"unfinished\n"},
			{name: "terminated partial JSON is protocol failure", tail: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"unfinished\n\n", wantDecode: true},
			{name: "unterminated invalid JSON is never dispatched", tail: "data: {invalid}"},
			{name: "prior protocol failure remains authoritative", prefix: "data: {\"type\":true}\n\n", tail: `data: {"type":"response.output_text.delta","delta":"unfinished`, wantDecode: true},
		} {
			t.Run(ending+"/"+framing.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					thinking := strings.Repeat("r", 907)
					payload := strings.NewReader(fmt.Sprintf("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":%q}\n\n", thinking) + framing.prefix + framing.tail)
					body := newWatchdogTestBody()
					body.read = func(p []byte) (int, error) {
						if payload.Len() > 0 {
							return payload.Read(p)
						}
						switch ending {
						case "stream_idle":
							<-body.closed
							return 0, io.ErrClosedPipe
						case "stream_read":
							return 0, io.ErrUnexpectedEOF
						default:
							return 0, io.EOF
						}
					}
					resp := &http.Response{Body: body, Header: http.Header{"X-Request-Id": []string{"tail-interruption"}}}
					var events []StreamEvent
					result := (&Client{}).consumeResponsesAttempt(context.Background(), resp, func(event StreamEvent) error {
						events = append(events, event)
						return nil
					}, 90*time.Second)
					if len(events) != 1 || events[0].Thinking != thinking || events[0].Done || len(events[0].ToolCalls) != 0 {
						t.Fatalf("events=%#v, want only published thinking before the broken frame", events)
					}
					wantKind := ending
					if framing.wantDecode {
						wantKind = "decode"
					}
					var failure *responsesStreamFailure
					if !errors.As(result.err, &failure) || failure.EventType != wantKind || failure.RequestID != "tail-interruption" {
						t.Fatalf("error=%v, want event=%s with original request identity", result.err, wantKind)
					}
					if !framing.wantDecode && ending == "stream_read" && !errors.Is(result.err, io.ErrUnexpectedEOF) {
						t.Fatalf("lost original read error: %v", result.err)
					}
					if framing.prefix != "" {
						var firstError *json.UnmarshalTypeError
						if !errors.As(result.err, &firstError) {
							t.Fatalf("earlier complete-frame failure was replaced: %v", result.err)
						}
					}
					annotated := responsesFailureAfterOutput(result.err, 0, len(thinking))
					if IsRecoverableResponsesInterruption(annotated) == framing.wantDecode {
						t.Fatalf("wrong recovery eligibility for %v", annotated)
					}
				})
			})
		}
	}
}

func TestResponsesUnterminatedCompletedFrameDoesNotCommit(t *testing.T) {
	for _, output := range []string{
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"complete answer"}]}`,
		`{"type":"function_call","call_id":"unfinished-write","name":"Write","arguments":"{}"}`,
	} {
		for _, ending := range []string{"", "\n", "\r", "\r\n"} {
			t.Run(fmt.Sprintf("output_%s/suffix_%q", output, ending), func(t *testing.T) {
				payload := `data: {"type":"response.completed","response":{"output":[` + output + `]}}` + ending
				resp := &http.Response{Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}
				var events []StreamEvent
				result := (&Client{}).consumeResponsesAttempt(context.Background(), resp, func(event StreamEvent) error {
					events = append(events, event)
					return nil
				}, time.Second)
				var failure *responsesStreamFailure
				if !errors.As(result.err, &failure) || failure.EventType != "unexpected_eof" || len(events) != 0 {
					t.Fatalf("events=%#v err=%v, want no dispatch without the SSE event boundary", events, result.err)
				}
			})
		}
	}
}
