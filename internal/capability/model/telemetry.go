package model

import (
	"context"
	"errors"
	"net/http"
	"paw/internal/message"
	"sync"
	"time"
)

type RequestScope struct {
	SessionID       string `json:"session_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	ParentTaskID    string `json:"parent_task_id,omitempty"`
	Purpose         string `json:"purpose,omitempty"`
}

type RequestEvent struct {
	Kind               string                      `json:"kind"`
	RequestID          string                      `json:"request_id"`
	Timestamp          time.Time                   `json:"timestamp"`
	Scope              RequestScope                `json:"scope"`
	Provider           string                      `json:"provider,omitempty"`
	ProviderResponseID string                      `json:"provider_response_id,omitempty"`
	UsageFinality      string                      `json:"usage_finality,omitempty"`
	Model              string                      `json:"model,omitempty"`
	Transport          string                      `json:"transport,omitempty"`
	Attempt            int                         `json:"attempt,omitempty"`
	Status             string                      `json:"status,omitempty"`
	HTTPStatus         int                         `json:"http_status,omitempty"`
	Usage              *Usage                      `json:"usage,omitempty"`
	Atoms              []InputAtom                 `json:"atoms,omitempty"`
	AtomsState         string                      `json:"atoms_state,omitempty"`
	RequestBodyBytes   int64                       `json:"request_body_bytes,omitempty"`
	StreamDiagnostics  *ResponsesStreamDiagnostics `json:"stream_diagnostics,omitempty"`
}

type requestScopeKey struct{}
type requestTraceKey struct{}

func WithRequestScope(ctx context.Context, scope RequestScope) context.Context {
	return context.WithValue(ctx, requestScopeKey{}, scope)
}

func RequestScopeFromContext(ctx context.Context) RequestScope {
	scope, _ := ctx.Value(requestScopeKey{}).(RequestScope)
	return scope
}

func (c *Client) SetRequestObserver(observer func(RequestEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestObserver = observer
}

type requestTrace struct {
	mu             sync.Mutex
	base           RequestEvent
	observer       func(RequestEvent)
	usage          Usage
	usageSeen      bool
	responseStatus string
	ended          bool
	messages       []message.Message
}

func (c *Client) startRequestTrace(ctx context.Context, cfg Config, requestID, transport string, messages []message.Message) (context.Context, *requestTrace) {
	c.mu.RLock()
	observer := c.requestObserver
	c.mu.RUnlock()
	if observer == nil {
		return ctx, nil
	}
	scope, _ := ctx.Value(requestScopeKey{}).(RequestScope)
	t := &requestTrace{observer: observer, messages: messages, base: RequestEvent{RequestID: requestID, Provider: cfg.Provider, Model: cfg.Model, Transport: transport, Scope: scope}}
	t.emitLocked("request_start", "", 0, nil)
	return context.WithValue(ctx, requestTraceKey{}, t), t
}

func (t *requestTrace) emitLocked(kind, status string, httpStatus int, usage *Usage) {
	event := t.base
	event.Kind, event.Status, event.HTTPStatus = kind, status, httpStatus
	event.Timestamp, event.Usage = time.Now().UTC(), usage
	t.observer(event)
}

func (t *requestTrace) finish(status string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	t.ended = true
	if t.responseStatus == "" && t.usageSeen {
		t.observeResponseLocked("", status)
	}
	t.emitLocked("request_end", status, 0, nil)
}

func requestEndStatus(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	if err != nil {
		return "failed"
	}
	return "incomplete"
}

func (t *requestTrace) wrap(ctx context.Context, input <-chan StreamEvent) <-chan StreamEvent {
	if t == nil {
		return input
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		defer func() { t.finish(requestEndStatus(ctx.Err())) }()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-input:
				if !ok {
					return
				}
				if event.Err != nil {
					t.finish(requestEndStatus(event.Err))
				} else if event.Done {
					t.finish("completed")
				}
				if !sendStreamEvent(ctx, out, event) {
					return
				}
			}
		}
	}()
	return out
}

func observeProviderUsage(ctx context.Context, usage *Usage) {
	trace, _ := ctx.Value(requestTraceKey{}).(*requestTrace)
	if trace == nil || usage == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	current := *usage
	current.RequestID = trace.base.RequestID
	current.Protocol = UsageProtocolOpenAI
	if trace.base.Transport == "anthropic-compatible" {
		current.Protocol = UsageProtocolAnthropic
	}
	merged := MergeUsageSnapshot(trace.usage, current)
	if trace.usageSeen && merged == trace.usage {
		return
	}
	trace.usage = merged
	trace.usageSeen = true
	if trace.base.UsageFinality == "" {
		trace.base.UsageFinality = "partial"
	}
	snapshot := trace.usage
	trace.emitLocked("attempt_usage", "", 0, &snapshot)
}

func observeProviderResponse(ctx context.Context, responseID, status string) {
	trace, _ := ctx.Value(requestTraceKey{}).(*requestTrace)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.observeResponseLocked(responseID, status)
}

func (t *requestTrace) observeResponseLocked(responseID, status string) {
	changed := false
	if responseID != "" && responseID != t.base.ProviderResponseID {
		t.base.ProviderResponseID = responseID
		changed = true
	}
	finality := ""
	switch status {
	case "completed":
		finality = "final"
	case "failed", "incomplete", "canceled":
		finality = "partial"
	default:
		status = ""
	}
	if status != "" && (t.responseStatus != status || t.base.UsageFinality != finality) {
		t.responseStatus = status
		t.base.UsageFinality = finality
		changed = true
	}
	if changed {
		t.emitLocked("attempt_response", status, 0, nil)
	}
}

type telemetryTransport struct{ base http.RoundTripper }

func (t telemetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	trace, _ := req.Context().Value(requestTraceKey{}).(*requestTrace)
	if trace == nil {
		return t.base.RoundTrip(req)
	}
	trace.mu.Lock()
	trace.base.Attempt++
	if req.Header.Get("Anthropic-Version") != "" {
		trace.base.Transport = "anthropic-compatible"
	} else if trace.base.Transport != "openai-responses" {
		trace.base.Transport = "openai-compatible"
	}
	trace.usage = Usage{}
	trace.usageSeen = false
	trace.responseStatus = ""
	trace.base.ProviderResponseID = ""
	trace.base.UsageFinality = ""
	trace.base.Atoms, trace.base.AtomsState = requestInputAtoms(req, trace.messages, trace.base.Transport)
	trace.base.RequestBodyBytes = req.ContentLength
	trace.emitLocked("attempt_start", "", 0, nil)
	trace.base.Atoms, trace.base.AtomsState, trace.base.RequestBodyBytes = nil, "", 0
	trace.mu.Unlock()
	response, err := t.base.RoundTrip(req)
	trace.mu.Lock()
	defer trace.mu.Unlock()
	status, httpStatus := "headers", 0
	if err != nil {
		status = "network_error"
	}
	if response != nil {
		httpStatus = response.StatusCode
	}
	trace.emitLocked("attempt_headers", status, httpStatus, nil)
	return response, err
}
