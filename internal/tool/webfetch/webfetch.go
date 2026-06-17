package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeoutSeconds = 30
	maxResponseBytes      = 32 * 1024
)

type Tool struct {
	Client *http.Client
}

type input struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *Tool) Name() string {
	return "WebFetch"
}

func (t *Tool) Description() string {
	return "通过 HTTP(S) 抓取网页内容，并返回状态、内容类型和响应体文本"
}

func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1}},"required":["url"]}`)
}

func (t *Tool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.URL) == "" {
		return "", fmt.Errorf("url is required")
	}

	parsedURL, err := url.Parse(in.URL)
	if err != nil {
		return "", err
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", err
	}

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var body limitedBuffer
	if _, err := io.Copy(&body, resp.Body); err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString("status: ")
	out.WriteString(resp.Status)
	out.WriteByte('\n')
	out.WriteString("content-type: ")
	out.WriteString(resp.Header.Get("Content-Type"))
	out.WriteByte('\n')
	out.WriteString("body:\n")
	out.WriteString(body.String())
	return out.String(), nil
}

type limitedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxResponseBytes - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, err := b.buf.Write(p)
	return len(p), err
}

func (b *limitedBuffer) String() string {
	out := b.buf.String()
	if !b.truncated {
		return out
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + "[response truncated]"
}
