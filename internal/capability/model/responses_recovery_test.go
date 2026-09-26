package model

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestIsRecoverableResponsesInterruption(t *testing.T) {
	published := func(err error) error { return responsesFailureAfterOutput(err, 0, 1126) }
	for _, test := range []struct {
		name          string
		err           error
		want          bool
		wantTransport bool
	}{
		{name: "nil"},
		{name: "typed nil", err: (*responsesStreamFailure)(nil)},
		{name: "idle without output", err: &responsesStreamFailure{EventType: "stream_idle", Retryable: true}, wantTransport: true},
		{name: "read without output", err: responsesReadFailure(io.ErrUnexpectedEOF, nil), wantTransport: true},
		{name: "EOF before completed without output", err: &responsesStreamFailure{EventType: "unexpected_eof", Retryable: true}, wantTransport: true},
		{name: "wrapped interruption", err: fmt.Errorf("model round: %w", published(&responsesStreamFailure{EventType: "stream_read", cause: io.ErrUnexpectedEOF})), want: true, wantTransport: true},
		{name: "published EOF before completed", err: published(&responsesStreamFailure{EventType: "unexpected_eof", Retryable: true}), want: true, wantTransport: true},
		{name: "published thinking forbids replay but permits continuation", err: responsesFailureAfterOutput(&responsesStreamFailure{EventType: "stream_idle", Retryable: true}, 0, 1126), want: true, wantTransport: true},
		{name: "published text forbids replay but permits continuation", err: responsesFailureAfterOutput(responsesReadFailure(io.ErrUnexpectedEOF, nil), 25, 0), want: true, wantTransport: true},
		{name: "typed network read", err: published(responsesReadFailure(&net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}, nil)), want: true, wantTransport: true},
		{name: "canceled read", err: published(responsesReadFailure(context.Canceled, nil))},
		{name: "deadline read", err: published(responsesReadFailure(context.DeadlineExceeded, nil))},
		{name: "joined cancellation", err: errors.Join(published(&responsesStreamFailure{EventType: "stream_idle"}), context.Canceled)},
		{name: "frame limit", err: published(responsesReadFailure(bufio.ErrTooLong, nil))},
		{name: "wrapped frame limit", err: published(&responsesStreamFailure{EventType: "stream_read", cause: fmt.Errorf("scanner: %w", bufio.ErrTooLong)})},
		{name: "decode", err: published(&responsesStreamFailure{EventType: "decode", Retryable: true})},
		{name: "completion mismatch", err: published(&responsesStreamFailure{EventType: "completion_mismatch"})},
		{name: "replay mismatch", err: published(&responsesStreamFailure{EventType: "replay_mismatch"})},
		{name: "provider failed", err: published(&responsesStreamFailure{EventType: "response.failed", Retryable: true})},
		{name: "provider incomplete", err: published(&responsesStreamFailure{EventType: "response.incomplete", Retryable: true})},
		{name: "provider error", err: published(&responsesStreamFailure{EventType: "error", Retryable: true})},
		{name: "provider HTTP", err: &ProviderHTTPError{StatusCode: 503, readErr: io.ErrUnexpectedEOF}},
		{name: "raw EOF", err: io.ErrUnexpectedEOF},
		{name: "raw network", err: &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}},
		{name: "unstructured event text", err: errors.New("模型错误 (event=stream_idle)")},
		{name: "unknown failure", err: published(&responsesStreamFailure{EventType: "unknown", Retryable: true})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsRecoverableResponsesInterruption(test.err); got != test.want {
				t.Fatalf("recoverable = %v, want %v for %v", got, test.want, test.err)
			}
			if got := IsResponsesTransportInterruption(test.err); got != test.wantTransport {
				t.Fatalf("transport interruption = %v, want %v for %v", got, test.wantTransport, test.err)
			}
		})
	}
}

func TestResponsesInterruptionClassificationPreservesErrorAndReplayPolicy(t *testing.T) {
	failure := &responsesStreamFailure{EventType: "stream_read", Retryable: true, RequestID: "request-1", cause: io.ErrUnexpectedEOF}
	err := responsesFailureAfterOutput(failure, 12, 1126)
	before := err.Error()
	if !IsRecoverableResponsesInterruption(err) {
		t.Fatal("published transport interruption should permit a new continuation round")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) || err.Error() != before {
		t.Fatal("classification changed the error or its cause")
	}
	if canRetryResponsesStream(context.Background(), err, true) {
		t.Fatal("continuation classification enabled replay of published output")
	}
	if failure.publishedTextBytes != 0 || failure.publishedThinkingBytes != 0 || !failure.Retryable {
		t.Fatal("published failure annotation mutated the original failure")
	}
}

func TestResponsesProtocolFailureSurvivesSubsequentInterruption(t *testing.T) {
	for _, ending := range []string{"stream_idle", "stream_read", "unexpected_eof", "canceled"} {
		t.Run(ending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				payload := strings.NewReader("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\ndata: {invalid}\n\n")
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
					case "canceled":
						cancel()
						return 0, context.Canceled
					default:
						return 0, io.EOF
					}
				}
				resp := &http.Response{Body: body, Header: http.Header{"X-Request-Id": []string{"protocol-failure"}}}
				var events []StreamEvent
				result := (&Client{}).consumeResponsesAttempt(ctx, resp, func(event StreamEvent) error {
					events = append(events, event)
					return nil
				}, 90*time.Second)
				if len(events) != 1 || events[0].Thinking != "thinking" {
					t.Fatalf("events = %#v, want only the valid thinking prefix", events)
				}
				if ending == "canceled" {
					if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("error = %v, want cancellation", result.err)
					}
					return
				}
				var failure *responsesStreamFailure
				var syntaxErr *json.SyntaxError
				if !errors.As(result.err, &failure) || failure.EventType != "decode" || failure.RequestID != "protocol-failure" || !errors.As(result.err, &syntaxErr) {
					t.Fatalf("error = %v, want original decode error and request identity", result.err)
				}
				if IsRecoverableResponsesInterruption(responsesFailureAfterOutput(result.err, 0, len(events[0].Thinking))) {
					t.Fatal("protocol error was treated as a recoverable transport interruption")
				}
			})
		})
	}
}

func TestResponsesKeepsFirstDecodeFailure(t *testing.T) {
	resp := &http.Response{
		Body:   io.NopCloser(strings.NewReader("data: {\"type\":true}\n\ndata: {invalid}\n\n")),
		Header: make(http.Header),
	}
	result := (&Client{}).consumeResponsesAttempt(context.Background(), resp, func(StreamEvent) error {
		t.Fatal("malformed frames must not publish events")
		return nil
	}, time.Second)
	var firstCause *json.UnmarshalTypeError
	if !errors.As(result.err, &firstCause) || firstCause.Field != "type" {
		t.Fatalf("error = %v, want first type-field decode failure", result.err)
	}
}
