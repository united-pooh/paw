package model

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestResponsesConsumerBackpressureDoesNotCloseReadableBody(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		name := "completion_waiting_in_body"
		if buffered {
			name = "completion_already_in_scanner_buffer"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
				completed := "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello done\"}]}]}}\n\n"
				chunks := []string{first, completed}
				if buffered {
					chunks = []string{first + completed}
				}
				body := newWatchdogTestBody()
				body.read = func(p []byte) (int, error) {
					select {
					case <-body.closed:
						return 0, io.ErrClosedPipe
					default:
					}
					if len(chunks) == 0 {
						return 0, io.EOF
					}
					n := copy(p, chunks[0])
					chunks[0] = chunks[0][n:]
					if chunks[0] == "" {
						chunks = chunks[1:]
					}
					return n, nil
				}
				resp := &http.Response{Body: body, Header: make(http.Header)}
				var text strings.Builder
				done, closedDuringConsumer := false, false
				result := (&Client{}).consumeResponsesAttempt(context.Background(), resp, func(event StreamEvent) error {
					text.WriteString(event.Delta)
					if event.Delta == "hello" {
						time.Sleep(defaultStreamIdleTimeout + time.Second)
						closedDuringConsumer = body.closeCalls.Load() != 0
					}
					done = done || event.Done
					return nil
				}, defaultStreamIdleTimeout)
				if result.err != nil || !done || text.String() != "hello done" || closedDuringConsumer {
					t.Fatalf("err=%v done=%v text=%q closed_during_consumer=%v; consumer processing must not count as blocked network Read", result.err, done, text.String(), closedDuringConsumer)
				}
			})
		})
	}
}
