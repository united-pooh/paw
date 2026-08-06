package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"testing"
	"time"
)

func TestNewProviderHTTPErrorParsesNestedJSONAndHeaders(t *testing.T) {
	body := []byte(`{"error":{"message":"prompt exceeds limit","type":"invalid_request_error","param":"input","code":"context_length_exceeded"}}`)
	header := http.Header{
		"X-Request-Id": []string{"req_123"},
		"Retry-After":  []string{"7"},
	}

	err := newProviderHTTPError(http.StatusBadRequest, header, body, "模型接口")
	if err.StatusCode != http.StatusBadRequest || err.Type != "invalid_request_error" || err.Code != "context_length_exceeded" || err.Param != "input" || err.Message != "prompt exceeds limit" {
		t.Fatalf("parsed error = %#v", err)
	}
	if err.Body != string(body) || err.RequestID != "req_123" || err.RetryAfter != 7*time.Second {
		t.Fatalf("metadata = body %q requestID %q retryAfter %s", err.Body, err.RequestID, err.RetryAfter)
	}
	if got := err.Error(); got != "模型接口返回异常状态 400: "+string(body) {
		t.Fatalf("Error() = %q", got)
	}
}

func TestNewProviderHTTPErrorParsesPlainText(t *testing.T) {
	err := newProviderHTTPError(http.StatusBadGateway, nil, []byte(" gateway unavailable\n"), "模型接口")
	if err.Message != "gateway unavailable" || err.Body != "gateway unavailable" {
		t.Fatalf("parsed error = %#v", err)
	}
	if err.Type != "" || err.Code != "" || err.Param != "" {
		t.Fatalf("unexpected structured fields = %#v", err)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "payload too large", err: &ProviderHTTPError{StatusCode: http.StatusRequestEntityTooLarge}, want: true},
		{name: "whitelisted code", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Code: "context_length_exceeded"}, want: true},
		{name: "whitelisted type", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Type: "input_too_large"}, want: true},
		{name: "wrapped explicit text", err: fmt.Errorf("request failed: %w", &ProviderHTTPError{StatusCode: http.StatusBadRequest, Message: "Prompt is too long for this model"}), want: true},
		{name: "explicit input length", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Message: "Input length exceeds the maximum allowed length"}, want: true},
		{name: "missing output", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Message: "missing field output at line 1 column 496925"}, want: false},
		{name: "token rate limit text", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Message: "Input tokens exceed the tokens per minute rate limit"}, want: false},
		{name: "unknown bad request", err: &ProviderHTTPError{StatusCode: http.StatusBadRequest, Message: "invalid request"}, want: false},
		{name: "rate limit", err: &ProviderHTTPError{StatusCode: http.StatusTooManyRequests, Code: "context_length_exceeded"}, want: false},
		{name: "ordinary error", err: fmt.Errorf("context length exceeded"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowError(tt.err); got != tt.want {
				t.Fatalf("IsContextOverflowError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestResponsesNon2xxReturnsProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req_responses")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"bad responses request","type":"invalid_request_error","code":"bad_request"}}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses",
		Model: "gpt-test", Timeout: time.Second,
	})
	_, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	assertProviderHTTPError(t, err, http.StatusBadRequest, "bad_request", "req_responses")
}

func TestChatCompletionsNon2xxReturnsProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req_chat")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"bad chat request","type":"invalid_request_error","code":"bad_request"}}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-compatible", APIBaseURL: server.URL, APIPath: "/chat/completions",
		Model: "gpt-test", Timeout: time.Second,
	})
	_, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	assertProviderHTTPError(t, err, http.StatusBadRequest, "bad_request", "req_chat")
}

func TestAnthropicNon2xxReturnsProviderHTTPError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Request-ID", "req_anthropic")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"}}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "anthropic-compatible", APIBaseURL: server.URL, APIPath: "/messages",
		Model: "claude-test", Timeout: time.Second,
	})
	_, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	assertProviderHTTPError(t, err, http.StatusBadRequest, "", "req_anthropic")
	if requests != 1 {
		t.Fatalf("requests = %d, want unknown Anthropic 400 to stop without Chat fallback", requests)
	}
}

func TestNonStreamingNon2xxReadFailureStillReturnsProviderHTTPError(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		apiPath   string
		stream    bool
		call      func(context.Context, *Client) error
	}{
		{
			name: "chat RunMessage", transport: "openai-compatible", apiPath: "/chat/completions",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.RunMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "hello"}})
				return err
			},
		},
		{
			name: "chat non-streaming StreamMessage", transport: "openai-compatible", apiPath: "/chat/completions", stream: false,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
				return err
			},
		},
		{
			name: "Responses RunMessage", transport: "openai-responses", apiPath: "/responses",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.RunMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "hello"}})
				return err
			},
		},
		{
			name: "Responses non-streaming StreamMessage", transport: "openai-responses", apiPath: "/responses", stream: false,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(Config{
				Transport:  tt.transport,
				APIBaseURL: "http://model.invalid",
				APIPath:    tt.apiPath,
				Model:      "test-model",
				Stream:     tt.stream,
				streamSet:  true,
				Timeout:    time.Second,
			})
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				header := make(http.Header)
				header.Set("X-Request-ID", "req_read_failure")
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     header,
					Body:       failingReadCloser{err: errors.New("read failed")},
				}, nil
			})

			err := tt.call(context.Background(), client)
			assertProviderHTTPError(t, err, http.StatusBadRequest, "", "req_read_failure")
		})
	}
}

func assertProviderHTTPError(t *testing.T, err error, wantStatus int, wantCode, wantRequestID string) {
	t.Helper()
	var providerErr *ProviderHTTPError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T %v, want *ProviderHTTPError", err, err)
	}
	if providerErr.StatusCode != wantStatus || providerErr.Code != wantCode || providerErr.RequestID != wantRequestID {
		t.Fatalf("ProviderHTTPError = %#v", providerErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type failingReadCloser struct {
	err error
}

func (body failingReadCloser) Read([]byte) (int, error) {
	return 0, body.err
}

func (failingReadCloser) Close() error {
	return nil
}

var _ io.ReadCloser = failingReadCloser{}
