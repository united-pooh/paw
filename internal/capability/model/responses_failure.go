package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type responsesStreamFailure struct {
	EventType              string
	Type                   string
	Code                   string
	Message                string
	RequestID              string
	Retryable              bool
	cause                  error
	publishedTextBytes     int
	publishedThinkingBytes int
}

func (e *responsesStreamFailure) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Responses API 请求失败"
	}
	detail := message
	if e.EventType != "" {
		detail += " (event=" + e.EventType + ")"
	}
	if cause := responsesFailureCause(e.cause); cause != "" {
		detail += " (cause=" + cause + ")"
	}
	if e.Code != "" {
		detail += " (code=" + e.Code + ")"
	}
	if e.RequestID != "" {
		detail += " (request_id=" + e.RequestID + ")"
	}
	if e.publishedTextBytes > 0 || e.publishedThinkingBytes > 0 {
		detail += fmt.Sprintf(" (published_text_bytes=%d, published_thinking_bytes=%d)", e.publishedTextBytes, e.publishedThinkingBytes)
		detail += "；已有部分输出，已停止重新生成。可输入“继续”从已完成的步骤接续"
	}
	return "模型接口返回错误: " + detail
}

func responsesFailureAfterOutput(err error, textBytes, thinkingBytes int) error {
	var failure *responsesStreamFailure
	if textBytes+thinkingBytes == 0 || !errors.As(err, &failure) {
		return err
	}
	copy := *failure
	copy.Retryable = false
	copy.publishedTextBytes, copy.publishedThinkingBytes = textBytes, thinkingBytes
	return &copy
}

func canRetryResponsesStream(ctx context.Context, err error, published bool) bool {
	// Re-POST creates a new generation, not a resumable copy of published output.
	return !published && isRetryableResponsesStreamError(ctx, err)
}

// IsRecoverableResponsesInterruption identifies published transport failures
// that can start a bounded continuation episode. Initial empty responses remain
// subject to the adapter retry budget; this never permits replaying the request.
func IsRecoverableResponsesInterruption(err error) bool {
	if !IsResponsesTransportInterruption(err) {
		return false
	}
	var failure *responsesStreamFailure
	return errors.As(err, &failure) && failure != nil && (failure.publishedTextBytes > 0 || failure.publishedThinkingBytes > 0)
}

// IsResponsesTransportInterruption classifies typed transport failures without
// requiring published output. Callers must still enforce their recovery budget.
func IsResponsesTransportInterruption(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, bufio.ErrTooLong) {
		return false
	}
	var failure *responsesStreamFailure
	if !errors.As(err, &failure) || failure == nil {
		return false
	}
	switch failure.EventType {
	case "stream_idle", "stream_read", "unexpected_eof":
		return true
	default:
		return false
	}
}

func (e *responsesStreamFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func isRetryableResponsesStreamError(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var failure *responsesStreamFailure
	if errors.As(err, &failure) {
		return failure.Retryable
	}
	return isRetryableRequestError(ctx, err)
}

func responsesFailureFromEvent(event responsesStreamEvent, header http.Header) *responsesStreamFailure {
	providerErr := event.Error
	if providerErr == nil && event.Response != nil {
		providerErr = event.Response.Error
	}
	failure := &responsesStreamFailure{
		EventType: event.Type,
		RequestID: providerRequestID(header),
		Retryable: event.Type == "response.failed" || event.Type == "response.incomplete",
	}
	if providerErr != nil {
		failure.Type = strings.TrimSpace(providerErr.Type)
		failure.Code = strings.TrimSpace(providerErr.Code)
		failure.Message = strings.TrimSpace(providerErr.Message)
	}
	// Request/auth/schema failures are deterministic and must not be replayed.
	nonRetryable := strings.ToLower(failure.Type + " " + failure.Code)
	for _, marker := range []string{"invalid", "authentication", "authorization", "permission", "not_found", "unsupported", "context_length", "rate_limit"} {
		if strings.Contains(nonRetryable, marker) {
			failure.Retryable = false
			break
		}
	}
	return failure
}

func responsesReadFailure(err error, header http.Header) *responsesStreamFailure {
	kind, retryable := "stream_read", true
	if errors.Is(err, bufio.ErrTooLong) {
		kind, retryable = "frame_limit", false
	}
	return &responsesStreamFailure{EventType: kind, Message: "读取 Responses 流式响应失败", RequestID: providerRequestID(header), Retryable: retryable, cause: err}
}

func responsesFailureCause(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected EOF"
	case errors.Is(err, io.EOF):
		return "EOF"
	case errors.Is(err, bufio.ErrTooLong):
		return "SSE frame exceeds size limit"
	case errors.Is(err, context.Canceled):
		return "context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network timeout"
		}
		return "network read error"
	}
	return fmt.Sprintf("%T", err)
}
