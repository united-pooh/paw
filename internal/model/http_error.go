package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderHTTPError describes a non-success HTTP response returned by a model
// provider while retaining the original response body for diagnostics.
type ProviderHTTPError struct {
	StatusCode int
	Type       string
	Code       string
	Param      string
	Message    string
	Body       string
	RequestID  string
	RetryAfter time.Duration

	prefix  string
	readErr error
}

func (e *ProviderHTTPError) Error() string {
	prefix := strings.TrimSpace(e.prefix)
	if prefix == "" {
		prefix = "模型接口"
	}
	if e.readErr != nil {
		return fmt.Sprintf("%s返回异常状态 %d，且读取错误响应失败: %v", prefix, e.StatusCode, e.readErr)
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		body = strings.TrimSpace(e.Message)
	}
	return fmt.Sprintf("%s返回异常状态 %d: %s", prefix, e.StatusCode, body)
}

func (e *ProviderHTTPError) Unwrap() error {
	return e.readErr
}

func newProviderHTTPError(statusCode int, header http.Header, body []byte, prefix string) *ProviderHTTPError {
	bodyText := strings.TrimSpace(string(body))
	err := &ProviderHTTPError{
		StatusCode: statusCode,
		Body:       bodyText,
		RequestID:  providerRequestID(header),
		RetryAfter: providerRetryAfter(header, time.Now()),
		prefix:     prefix,
	}
	err.Type, err.Code, err.Param, err.Message = parseProviderErrorBody(bodyText)
	return err
}

func newProviderHTTPErrorWithReadError(statusCode int, header http.Header, body []byte, readErr error, prefix string) *ProviderHTTPError {
	err := newProviderHTTPError(statusCode, header, body, prefix)
	err.readErr = readErr
	return err
}

func providerHTTPErrorFromResponse(resp *http.Response, prefix string) *ProviderHTTPError {
	if resp == nil {
		return newProviderHTTPError(0, nil, nil, prefix)
	}
	body, readErr := io.ReadAll(resp.Body)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	return newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, body, readErr, prefix)
}

func parseProviderErrorBody(body string) (errorType, code, param, message string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", "", ""
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return "", "", "", body
	}

	fields := root
	if raw, ok := root["error"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil && nested != nil {
			fields = nested
		} else if text := jsonString(raw); text != "" {
			message = text
		}
	}
	errorType = firstJSONText(fields["type"], root["type"])
	code = firstJSONText(fields["code"], root["code"])
	param = firstJSONText(fields["param"], root["param"])
	message = firstNonEmpty(firstJSONText(fields["message"], root["message"]), message)
	if message == "" {
		message = body
	}
	return errorType, code, param, message
}

func firstJSONText(values ...json.RawMessage) string {
	for _, raw := range values {
		if text := jsonString(raw); text != "" {
			return text
		}
	}
	return ""
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func providerRequestID(header http.Header) string {
	if header == nil {
		return ""
	}
	return firstNonEmpty(
		header.Get("X-Request-ID"),
		header.Get("Request-ID"),
		header.Get("Anthropic-Request-ID"),
	)
}

func providerRetryAfter(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

// IsContextOverflowError reports whether err is a provider HTTP response that
// clearly indicates the request exceeded the model's context capacity.
func IsContextOverflowError(err error) bool {
	var providerErr *ProviderHTTPError
	if !errors.As(err, &providerErr) || providerErr == nil {
		return false
	}
	if providerErr.StatusCode == http.StatusRequestEntityTooLarge {
		return true
	}
	if providerErr.StatusCode != http.StatusBadRequest {
		return false
	}
	for _, value := range []string{providerErr.Code, providerErr.Type} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "context_length_exceeded", "context_too_large", "input_too_large":
			return true
		}
	}
	text := strings.ToLower(strings.Join([]string{
		providerErr.Message,
		providerErr.Body,
	}, " "))
	return hasExplicitContextOverflowText(text)
}

func hasExplicitContextOverflowText(text string) bool {
	for _, subject := range []string{"context", "input", "prompt"} {
		if !strings.Contains(text, subject) {
			continue
		}
		for _, phrase := range []string{
			subject + " length exceeded",
			subject + "_length_exceeded",
			subject + " too long",
			subject + " is too long",
			subject + " too large",
			subject + " is too large",
			"maximum " + subject + " length",
			"max " + subject + " length",
		} {
			if strings.Contains(text, phrase) {
				return true
			}
		}
		if strings.Contains(text, subject+" length") && strings.Contains(text, "exceed") {
			return true
		}
		mentionsTokenCount := strings.Contains(text, subject+" token")
		mentionsCapacity := strings.Contains(text, "maximum") || strings.Contains(text, "max token") ||
			strings.Contains(text, "context window") || strings.Contains(text, "context limit") || strings.Contains(text, "context length")
		if mentionsTokenCount && strings.Contains(text, "exceed") && mentionsCapacity {
			return true
		}
		if strings.Contains(text, subject+" exceed") && mentionsCapacity {
			return true
		}
	}
	return false
}
