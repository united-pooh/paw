package model

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ResponsesStreamDiagnostics contains only bounded metadata, never stream content.
type ResponsesStreamDiagnostics struct {
	FailureStage         string `json:"failure_stage"`
	Cause                string `json:"cause,omitempty"`
	HTTPProtocol         string `json:"http_protocol"`
	ContentEncoding      string `json:"content_encoding,omitempty"`
	TransferEncoding     string `json:"transfer_encoding,omitempty"`
	Uncompressed         bool   `json:"uncompressed"`
	BodyBytes            int64  `json:"body_bytes"` // Application bytes, after any HTTP transport decompression.
	LastByteAgeMS        int64  `json:"last_byte_age_ms"`
	MaxReadWaitMS        int64  `json:"max_read_wait_ms"`
	ReadEnd              string `json:"read_end,omitempty"`
	MaxConsumerPauseMS   int64  `json:"max_consumer_pause_ms"`
	WatchdogTimedOut     bool   `json:"watchdog_timed_out"`
	ContextCanceled      bool   `json:"context_canceled"`
	ContextDeadline      bool   `json:"context_deadline"`
	UnterminatedSSEBytes int64  `json:"unterminated_sse_bytes"`
}

type responsesDiagnosticBody struct {
	io.ReadCloser
	trace                 *requestTrace
	event                 RequestEvent
	data                  ResponsesStreamDiagnostics
	lastByte, lastReadEnd time.Time
	lineBytes             int64
	afterCR, boundaryCR   bool
	finished              bool
}

func newResponsesDiagnosticBody(ctx context.Context, resp *http.Response) *responsesDiagnosticBody {
	trace, _ := ctx.Value(requestTraceKey{}).(*requestTrace)
	if trace == nil {
		return nil
	}
	trace.mu.Lock()
	event := trace.base
	trace.mu.Unlock()
	event.HTTPStatus = resp.StatusCode
	return &responsesDiagnosticBody{ReadCloser: resp.Body, trace: trace, event: event, data: ResponsesStreamDiagnostics{
		HTTPProtocol: diagnosticProtocol(resp.Proto), ContentEncoding: diagnosticEncoding(resp.Header.Get("Content-Encoding")),
		TransferEncoding: diagnosticEncoding(strings.Join(resp.TransferEncoding, ",")), Uncompressed: resp.Uncompressed, LastByteAgeMS: -1,
	}}
}

func (b *responsesDiagnosticBody) Read(p []byte) (int, error) {
	start := time.Now()
	if !b.lastReadEnd.IsZero() {
		b.data.MaxConsumerPauseMS = max(b.data.MaxConsumerPauseMS, start.Sub(b.lastReadEnd).Milliseconds())
	}
	n, err := b.ReadCloser.Read(p)
	end := time.Now()
	b.data.MaxReadWaitMS = max(b.data.MaxReadWaitMS, end.Sub(start).Milliseconds())
	b.lastReadEnd = end
	if err != nil {
		_, b.data.ReadEnd = responsesDiagnosticFailure(err)
	}
	if n > 0 {
		b.lastByte = end
		b.data.BodyBytes += int64(n)
		for _, v := range p[:n] {
			b.data.UnterminatedSSEBytes++
			if b.afterCR {
				b.afterCR = false
				if v == '\n' {
					if b.boundaryCR {
						b.data.UnterminatedSSEBytes = 0
					}
					continue
				}
			}
			if v == '\r' || v == '\n' {
				b.boundaryCR = b.lineBytes == 0
				if b.boundaryCR {
					b.data.UnterminatedSSEBytes = 0
				}
				b.lineBytes = 0
				b.afterCR = v == '\r'
			} else {
				b.lineBytes++
			}
		}
	}
	return n, err
}

func (b *responsesDiagnosticBody) finish(ctx context.Context, err error, timedOut bool) {
	if b == nil || b.finished {
		return
	}
	b.finished = true
	end := time.Now()
	if !b.lastByte.IsZero() {
		b.data.LastByteAgeMS = end.Sub(b.lastByte).Milliseconds()
	}
	if !b.lastReadEnd.IsZero() {
		b.data.MaxConsumerPauseMS = max(b.data.MaxConsumerPauseMS, end.Sub(b.lastReadEnd).Milliseconds())
	}
	b.data.FailureStage, b.data.Cause = responsesDiagnosticFailure(err)
	b.data.WatchdogTimedOut = timedOut
	b.data.ContextCanceled = errors.Is(ctx.Err(), context.Canceled)
	b.data.ContextDeadline = errors.Is(ctx.Err(), context.DeadlineExceeded)
	event := b.event
	event.Kind, event.Timestamp = "attempt_stream_end", end.UTC()
	event.Status = "completed"
	if err != nil {
		event.Status = requestEndStatus(err)
	}
	data := b.data
	event.StreamDiagnostics = &data
	b.trace.mu.Lock()
	defer b.trace.mu.Unlock()
	if b.trace.base.Attempt == event.Attempt {
		event.ProviderResponseID = b.trace.base.ProviderResponseID
		event.UsageFinality = b.trace.base.UsageFinality
	}
	b.trace.observer(event)
}

func diagnosticProtocol(value string) string {
	switch value {
	case "HTTP/1.0", "HTTP/1.1", "HTTP/2.0", "HTTP/3.0":
		return value
	default:
		return "other"
	}
}

func diagnosticEncoding(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "identity", "gzip", "deflate", "br", "zstd", "chunked":
		return value
	default:
		return "other"
	}
}

func responsesDiagnosticFailure(err error) (string, string) {
	if err == nil {
		return "completed", ""
	}
	stage := "consumer_error"
	var failure *responsesStreamFailure
	if errors.As(err, &failure) {
		switch failure.EventType {
		case "decode", "stream_idle", "stream_read", "unexpected_eof", "frame_limit", "completion_mismatch", "response.failed", "response.incomplete", "error":
			stage = failure.EventType
		default:
			stage = "response_error"
		}
		err = failure.cause
	} else if errors.Is(err, context.Canceled) {
		stage = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		stage = "deadline"
	}
	cause := ""
	var syntax *json.SyntaxError
	var types *json.UnmarshalTypeError
	var network net.Error
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		cause = "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		cause = "context_deadline"
	case errors.Is(err, io.ErrUnexpectedEOF):
		cause = "unexpected_eof"
	case errors.Is(err, io.EOF):
		cause = "eof"
	case errors.Is(err, bufio.ErrTooLong):
		cause = "frame_limit"
	case errors.As(err, &syntax):
		cause = "json_syntax"
	case errors.As(err, &types):
		cause = "json_type"
	case errors.As(err, &network):
		if network.Timeout() {
			cause = "network_timeout"
		} else {
			cause = "network_error"
		}
	default:
		cause = "other"
	}
	return stage, cause
}
