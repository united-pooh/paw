package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

const httpSessionHeader = "Mcp-Session-Id"

type httpSession struct {
	config ServerConfig
	client *http.Client
	url    *url.URL

	nextID atomic.Int64

	mu        sync.RWMutex
	sessionID string
	closed    bool
	done      chan struct{}
	notifyMu  sync.RWMutex

	notifications chan Notification
	closeOnce     sync.Once
}

var _ RPCSession = (*httpSession)(nil)

func newHTTPSession(config ServerConfig) (*httpSession, error) {
	serverURL := strings.TrimSpace(config.URL)
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("MCP server %q has invalid HTTP url %q", config.Name, serverURL)
	}
	return &httpSession{
		config:        config,
		client:        &http.Client{},
		url:           parsed,
		done:          make(chan struct{}),
		notifications: make(chan Notification, 32),
	}, nil
}

func (s *httpSession) Call(ctx context.Context, method string, params, result any) error {
	if s == nil {
		return errors.New("MCP HTTP session is nil")
	}
	if strings.TrimSpace(method) == "" {
		return errors.New("MCP HTTP method is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestID := s.nextID.Add(1)
	messages, status, err := s.send(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  marshalRPCParams(params),
	})
	if err != nil {
		return err
	}
	if len(messages) == 0 && (status == http.StatusAccepted || status == http.StatusNoContent) {
		return errors.New("MCP HTTP call returned no response")
	}
	response, ok, err := s.responseFor(messages, requestID)
	if err != nil {
		return fmt.Errorf("MCP HTTP method %s failed: %w", method, err)
	}
	if !ok {
		return fmt.Errorf("MCP HTTP method %s returned no response for request %d", method, requestID)
	}
	if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode MCP HTTP method %s result: %w", method, err)
	}
	return nil
}

func (s *httpSession) Notify(ctx context.Context, method string, params any) error {
	if s == nil {
		return errors.New("MCP HTTP session is nil")
	}
	if strings.TrimSpace(method) == "" {
		return errors.New("MCP HTTP notification method is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	messages, _, err := s.send(ctx, rpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  marshalRPCParams(params),
	})
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Method != "" {
			s.publishNotification(message)
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("MCP HTTP notification %s failed (%d): %s", method, message.Error.Code, message.Error.Message)
		}
	}
	return nil
}

func (s *httpSession) Notifications() <-chan Notification {
	if s == nil {
		return nil
	}
	return s.notifications
}

func (s *httpSession) PID() int { return 0 }

func (s *httpSession) StderrTail() string { return "" }

func (s *httpSession) WaitError() error {
	if s == nil {
		return nil
	}
	<-s.done
	return nil
}

func (s *httpSession) Close(_ context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)

		s.notifyMu.Lock()
		close(s.notifications)
		s.notifyMu.Unlock()
	})
	return nil
}

func (s *httpSession) send(ctx context.Context, message any) ([]rpcEnvelope, int, error) {
	if err := s.checkOpen(); err != nil {
		return nil, 0, err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, 0, fmt.Errorf("encode MCP HTTP message: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("create MCP HTTP request: %w", err)
	}
	for key, value := range s.config.Headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID := s.currentSessionID(); sessionID != "" {
		request.Header.Set(httpSessionHeader, sessionID)
	}

	response, err := s.client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("MCP HTTP request failed: %w", err)
	}
	defer response.Body.Close()
	if sessionID := strings.TrimSpace(response.Header.Get(httpSessionHeader)); sessionID != "" {
		s.setSessionID(sessionID)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read MCP HTTP response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail := truncateDiagnostic(string(body))
		if detail == "" {
			detail = response.Status
		}
		return nil, response.StatusCode, fmt.Errorf("MCP HTTP server returned %s: %s", response.Status, detail)
	}
	messages, err := decodeHTTPMessages(body, response.Header.Get("Content-Type"))
	if err != nil {
		return nil, response.StatusCode, err
	}
	for _, message := range messages {
		if message.Method != "" {
			s.publishNotification(message)
		}
	}
	return messages, response.StatusCode, nil
}

func (s *httpSession) responseFor(messages []rpcEnvelope, requestID int64) (rpcEnvelope, bool, error) {
	for _, message := range messages {
		if message.Method != "" || len(message.ID) == 0 {
			continue
		}
		var id int64
		if err := json.Unmarshal(message.ID, &id); err != nil {
			return rpcEnvelope{}, false, fmt.Errorf("decode MCP HTTP response id: %w", err)
		}
		if id != requestID {
			continue
		}
		if message.Error != nil {
			return message, true, fmt.Errorf("MCP method failed (%d): %s", message.Error.Code, message.Error.Message)
		}
		return message, true, nil
	}
	return rpcEnvelope{}, false, nil
}

func (s *httpSession) publishNotification(message rpcEnvelope) {
	s.notifyMu.RLock()
	defer s.notifyMu.RUnlock()
	if s.isClosed() {
		return
	}
	notification := Notification{
		Method: message.Method,
		Params: append(json.RawMessage(nil), message.Params...),
	}
	select {
	case s.notifications <- notification:
	default:
	}
}

func (s *httpSession) checkOpen() error {
	if s.isClosed() {
		return errors.New("MCP HTTP session is closed")
	}
	return nil
}

func (s *httpSession) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *httpSession) currentSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *httpSession) setSessionID(sessionID string) {
	s.mu.Lock()
	s.sessionID = sessionID
	s.mu.Unlock()
}

func decodeHTTPMessages(body []byte, contentType string) ([]rpcEnvelope, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytes.HasPrefix(body, []byte("event:")) || bytes.HasPrefix(body, []byte("data:")) {
		return decodeSSEMessages(body)
	}
	var message rpcEnvelope
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, fmt.Errorf("decode MCP HTTP JSON response: %w", err)
	}
	return []rpcEnvelope{message}, nil
}

func decodeSSEMessages(body []byte) ([]rpcEnvelope, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	messages := make([]rpcEnvelope, 0, 1)
	var data strings.Builder
	flush := func() error {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" {
			return nil
		}
		var message rpcEnvelope
		if err := json.Unmarshal([]byte(payload), &message); err != nil {
			return fmt.Errorf("decode MCP HTTP SSE data: %w", err)
		}
		messages = append(messages, message)
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read MCP HTTP SSE data: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return messages, nil
}
