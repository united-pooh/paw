package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"paw/internal/message"
	"strings"
	"sync"
	"time"
)

// Client 封装模型调用的 HTTP 客户端。
// 这一层只负责请求发送、响应解析、错误格式化。
type Client struct {
	httpClient *http.Client
	mu         sync.RWMutex
	cfg        Config
}

// NewClient 创建模型客户端。
func NewClient(cfg Config) *Client {
	cfg = CloneConfig(fillConfigDefaults(cfg))
	return &Client{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
	}
}

const (
	requestRetryBaseDelay = 200 * time.Millisecond
	requestRetryMaxDelay  = 2 * time.Second
)

// doRequestWithRetry executes only the request-establishment phase with retry.
// Once a streaming response has been accepted, the caller owns the body and
// retries are deliberately not attempted: replaying a partially consumed
// stream could duplicate assistant text or tool calls.
func (c *Client) doRequestWithRetry(ctx context.Context, cfg Config, stream bool, buildRequest func() (*http.Request, error)) (*http.Response, error) {
	attempts := cfg.RetryCount + 1
	if attempts < 1 {
		attempts = 1
	}

	client := c.httpClient
	if stream {
		client = c.streamHTTPClient()
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := buildRequest()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err == nil {
			if !isRetryableHTTPStatus(resp.StatusCode) || attempt == attempts-1 {
				return resp, nil
			}
			_ = drainAndClose(resp.Body)
			lastErr = fmt.Errorf("model endpoint returned retryable status %d", resp.StatusCode)
		} else {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if !isRetryableRequestError(ctx, err) || attempt == attempts-1 {
				return nil, err
			}
			lastErr = err
		}

		if err := waitForRequestRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func isRetryableHTTPStatus(status int) bool {
	switch {
	case status == http.StatusRequestTimeout,
		status == http.StatusTooEarly,
		status == http.StatusTooManyRequests:
		return true
	case status >= 500 && status <= 599:
		return true
	default:
		return false
	}
}

func isRetryableRequestError(ctx context.Context, err error) bool {
	if err == nil || ctx == nil {
		return err != nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func waitForRequestRetry(ctx context.Context, attempt int) error {
	shift := attempt
	if shift > 3 {
		shift = 3
	}
	delay := requestRetryBaseDelay * time.Duration(1<<shift)
	if delay > requestRetryMaxDelay {
		delay = requestRetryMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainAndClose(body io.ReadCloser) error {
	if body == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, body)
	closeErr := body.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (c *Client) streamHTTPClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.httpClient == nil {
		return &http.Client{}
	}
	client := *c.httpClient
	client.Timeout = 0
	return &client
}

func (c *Client) setRequestHeaders(req *http.Request) {
	cfg := c.CurrentModelConfig()
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
}

func (c *Client) CurrentModelConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CloneConfig(c.cfg)
}

func (c *Client) ApplyModelConfig(cfg Config) error {
	cfg = fillConfigDefaults(cfg)
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		cfg.APIKey = loadAPIKeyByEnvName(cfg.APIKeyEnvName, nil)
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg = CloneConfig(cfg)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	c.httpClient.Timeout = cfg.Timeout
	return nil
}

func (c *Client) SaveModelConfig(cfg Config) error {
	return SaveModelConfig(cfg)
}

// RunMessage 执行一次最小“输入 -> 输出”调用。
// 当前支持消息列表输入。
func (c *Client) RunMessage(ctx context.Context, messages []message.Message) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("messages 不能为空")
	}

	cfg := c.CurrentModelConfig()
	apiMessages, err := buildOpenAIMessages(messages)
	if err != nil {
		return "", fmt.Errorf("构造 OpenAI 请求消息失败: %w", err)
	}

	reqBody := ChatCompletionsRequest{
		Model:    cfg.Model,
		Messages: apiMessages,
	}

	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return "", fmt.Errorf("校验请求体配置失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			cfg.APIBaseURL+cfg.APIPath,
			bytes.NewReader(bodyBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(resp.Body)

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var parsed ChatCompletionsResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return "", fmt.Errorf("解析响应 JSON 失败: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("模型接口未返回任何 choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("模型接口返回了空内容")
	}

	return content, nil
}
