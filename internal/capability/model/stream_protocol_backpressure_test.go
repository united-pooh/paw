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

func TestOtherStreamingProtocolsPreserveBodyDuringConsumerPause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   string
		last    string
		consume func(*Client, context.Context, *http.Response, chan<- StreamEvent, time.Duration)
	}{
		{
			name:    "chat_completions",
			first:   "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n",
			last:    "data: {\"choices\":[{\"delta\":{\"content\":\" done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			consume: (*Client).consumeStream,
		},
		{
			name:    "anthropic",
			first:   "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			last:    "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" done\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
			consume: (*Client).consumeAnthropicStream,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				chunks := []string{tc.first, tc.last}
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
				events := make(chan StreamEvent)
				go tc.consume(&Client{}, context.Background(), &http.Response{Body: body, Header: make(http.Header)}, events, defaultStreamIdleTimeout)
				synctest.Wait()
				time.Sleep(defaultStreamIdleTimeout + time.Second)
				closedWhilePaused := body.closeCalls.Load() != 0
				var text strings.Builder
				done := false
				var streamErr error
				for event := range events {
					text.WriteString(event.Delta)
					if event.AssistantPart != nil && event.AssistantPart.Type == "text" {
						text.WriteString(event.AssistantPart.Delta)
					}
					done = done || event.Done
					if event.Err != nil {
						streamErr = event.Err
					}
				}
				if closedWhilePaused || streamErr != nil || !done || text.String() != "hello done" {
					t.Fatalf("closed_while_paused=%v err=%v done=%v text=%q, want intact completion after consumer resumes", closedWhilePaused, streamErr, done, text.String())
				}
			})
		})
	}
}
