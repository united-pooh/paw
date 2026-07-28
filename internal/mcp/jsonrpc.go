package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Notification struct {
	Method string
	Params json.RawMessage
}

type jsonRPCSession struct {
	reader io.Reader
	writer io.Writer

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse

	notifications chan Notification
	done          chan struct{}
	doneOnce      sync.Once

	errMu sync.RWMutex
	err   error

	closeReader func() error
	closeWriter func() error
}

func newJSONRPCSession(reader io.Reader, writer io.Writer) *jsonRPCSession {
	return newJSONRPCSessionWithClosers(reader, writer, nil, nil)
}

func newJSONRPCSessionWithClosers(reader io.Reader, writer io.Writer, closeReader, closeWriter func() error) *jsonRPCSession {
	session := &jsonRPCSession{
		reader:        reader,
		writer:        writer,
		pending:       make(map[int64]chan rpcResponse),
		notifications: make(chan Notification, 32),
		done:          make(chan struct{}),
		closeReader:   closeReader,
		closeWriter:   closeWriter,
	}
	go session.readLoop()
	return session
}

var _ RPCSession = (*jsonRPCSession)(nil)

func (s *jsonRPCSession) Call(ctx context.Context, method string, params, result any) error {
	if s == nil {
		return errors.New("MCP JSON-RPC session is nil")
	}
	if strings.TrimSpace(method) == "" {
		return errors.New("MCP JSON-RPC method is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestID := s.nextID.Add(1)
	responseCh := make(chan rpcResponse, 1)
	s.pendingMu.Lock()
	select {
	case <-s.done:
		s.pendingMu.Unlock()
		return s.sessionError()
	default:
	}
	s.pending[requestID] = responseCh
	s.pendingMu.Unlock()

	request := rpcRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  marshalRPCParams(params),
	}
	if err := s.writeMessage(request); err != nil {
		s.removePending(requestID)
		return err
	}

	select {
	case response := <-responseCh:
		if response.Error != nil {
			return fmt.Errorf("MCP method %s failed (%d): %s", method, response.Error.Code, response.Error.Message)
		}
		if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode MCP method %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		s.removePending(requestID)
		return ctx.Err()
	case <-s.done:
		s.removePending(requestID)
		return s.sessionError()
	}
}

func (s *jsonRPCSession) Notify(ctx context.Context, method string, params any) error {
	if s == nil {
		return errors.New("MCP JSON-RPC session is nil")
	}
	if strings.TrimSpace(method) == "" {
		return errors.New("MCP JSON-RPC notification method is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.sessionError()
	default:
	}
	return s.writeMessage(rpcNotification{JSONRPC: "2.0", Method: method, Params: marshalRPCParams(params)})
}

func (s *jsonRPCSession) Notifications() <-chan Notification {
	if s == nil {
		return nil
	}
	return s.notifications
}

func (s *jsonRPCSession) Close(_ context.Context) error {
	if s == nil {
		return nil
	}
	s.setError(errors.New("MCP JSON-RPC session closed"))
	var firstErr error
	if s.closeWriter != nil {
		if err := s.closeWriter(); err != nil && !isClosedError(err) {
			firstErr = err
		}
	}
	if s.closeReader != nil {
		if err := s.closeReader(); err != nil && firstErr == nil && !isClosedError(err) {
			firstErr = err
		}
	}
	return firstErr
}

func (s *jsonRPCSession) readLoop() {
	reader := bufio.NewReader(s.reader)
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			if decodeErr := s.handleMessage(line); decodeErr != nil {
				s.setError(decodeErr)
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.setError(fmt.Errorf("read MCP JSON-RPC message: %w", err))
			} else {
				s.setError(io.EOF)
			}
			return
		}
	}
}

func (s *jsonRPCSession) handleMessage(data []byte) error {
	var envelope rpcEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode MCP JSON-RPC message: %w", err)
	}
	if envelope.JSONRPC != "2.0" {
		return fmt.Errorf("unsupported MCP JSON-RPC version %q", envelope.JSONRPC)
	}
	if envelope.Method != "" {
		if len(envelope.ID) > 0 {
			return fmt.Errorf("unsupported MCP server request %q", envelope.Method)
		}
		select {
		case s.notifications <- Notification{Method: envelope.Method, Params: append(json.RawMessage(nil), envelope.Params...)}:
		default:
			return fmt.Errorf("MCP notification buffer is full")
		}
		return nil
	}
	if len(envelope.ID) == 0 {
		return errors.New("MCP JSON-RPC message has no method or id")
	}
	var requestID int64
	if err := json.Unmarshal(envelope.ID, &requestID); err != nil {
		return fmt.Errorf("decode MCP response id: %w", err)
	}
	response := rpcResponse{
		JSONRPC: envelope.JSONRPC,
		ID:      requestID,
		Result:  append(json.RawMessage(nil), envelope.Result...),
		Error:   envelope.Error,
	}
	s.pendingMu.Lock()
	responseCh, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	s.pendingMu.Unlock()
	if !ok {
		return fmt.Errorf("MCP response id %d has no pending request", requestID)
	}
	responseCh <- response
	return nil
}

func (s *jsonRPCSession) writeMessage(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode MCP JSON-RPC message: %w", err)
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writer.Write(data); err != nil {
		s.setError(fmt.Errorf("write MCP JSON-RPC message: %w", err))
		return s.sessionError()
	}
	return nil
}

func (s *jsonRPCSession) removePending(requestID int64) {
	s.pendingMu.Lock()
	delete(s.pending, requestID)
	s.pendingMu.Unlock()
}

func (s *jsonRPCSession) setError(err error) {
	s.doneOnce.Do(func() {
		s.errMu.Lock()
		s.err = err
		s.errMu.Unlock()
		close(s.done)
	})
}

func (s *jsonRPCSession) sessionError() error {
	s.errMu.RLock()
	err := s.err
	s.errMu.RUnlock()
	if err == nil {
		return errors.New("MCP JSON-RPC session closed")
	}
	return err
}

func marshalRPCParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...)
	}
	data, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}

func isClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}
