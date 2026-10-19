package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"time"
)

type chatStreamFailure struct {
	stage     string
	message   string
	requestID string
	cause     error
	published bool
}

func (e *chatStreamFailure) Error() string {
	detail := e.message + " (event=" + e.stage + ")"
	if cause := responsesFailureCause(e.cause); cause != "" {
		detail += " (cause=" + cause + ")"
	}
	if e.requestID != "" {
		detail += " (request_id=" + e.requestID + ")"
	}
	return detail
}

func (e *chatStreamFailure) Unwrap() error { return e.cause }

// IsRecoverableStreamInterruption permits continuation after published output,
// never replay of a partially delivered generation.
func IsRecoverableStreamInterruption(err error) bool {
	if IsRecoverableResponsesInterruption(err) {
		return true
	}
	var failure *chatStreamFailure
	return IsStreamTransportInterruption(err) && errors.As(err, &failure) && failure.published
}

// IsStreamTransportInterruption also classifies empty interrupted attempts
// within an existing, bounded continuation episode.
func IsStreamTransportInterruption(err error) bool {
	if IsResponsesTransportInterruption(err) {
		return true
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, bufio.ErrTooLong) {
		return false
	}
	var failure *chatStreamFailure
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.stage {
	case "stream_idle", "stream_read", "unexpected_eof":
		return true
	default:
		return false
	}
}

func chatFailureAfterOutput(err error, published bool) error {
	var failure *chatStreamFailure
	if !published || !errors.As(err, &failure) {
		return err
	}
	copy := *failure
	copy.published = true
	return &copy
}

func chatStreamReadFailure(err error, requestID string) error {
	stage := "stream_read"
	if errors.Is(err, bufio.ErrTooLong) {
		stage = "frame_limit"
	}
	return &chatStreamFailure{stage: stage, message: "读取流式响应失败", requestID: requestID, cause: err}
}

func chatStreamIdleFailure(timeout time.Duration, requestID string) error {
	return &chatStreamFailure{stage: "stream_idle", message: fmt.Sprintf("模型流 %s 无任何数据，连接已中断", timeout), requestID: requestID}
}
