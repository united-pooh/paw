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
	"net/url"
	"paw/internal/message"
	"strings"
	"sync"
	"time"
)

// Client 封装模型调用的 HTTP 客户端。
// 这一层只负责请求发送、响应解析、错误格式化。
type Client struct {
	// httpClient is an immutable transport template in production. Tests may
	// replace its Transport; every request still clones it and applies timeout
	// from the same captured Config snapshot.
	httpClient *http.Client
	mu         sync.RWMutex
	cfg        Config

	toolCacheMu  sync.Mutex
	toolCacheKey string
	toolCache    PreparedToolSet
}

// NewClient 创建模型客户端。
func NewClient(cfg Config) *Client {
	cfg = CloneConfig(fillConfigDefaults(cfg))
	return &Client{
		httpClient: httpClientWithProxy(cfg.Proxy),
		cfg:        cfg,
	}
}

// httpClientWithProxy 按代理配置构建带 Transport 的 HTTP 客户端。auto（含
// nil）使用进程环境变量（http.ProxyFromEnvironment），direct 强制直连，
// custom 使用固定代理 URL；custom URL 缺失或非法时回退直连。
func httpClientWithProxy(proxy *ProxyConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	switch NormalizeProxyMode(proxyMode(proxy)) {
	case ProxyModeDirect:
		transport.Proxy = nil
	case ProxyModeCustom:
		if parsed, err := url.Parse(strings.TrimSpace(proxy.URL)); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			transport.Proxy = http.ProxyURL(parsed)
		} else {
			transport.Proxy = nil
		}
	default:
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Transport: transport}
}

func proxyMode(proxy *ProxyConfig) ProxyMode {
	if proxy == nil {
		return ProxyModeAuto
	}
	return proxy.Mode
}

// requestRetryDelays 是可重试失败（EOF/连接重置/429/5xx 等）的固定退避
// 阶梯：默认 RetryCount=3，即三次重试分别等待 10s/15s/30s。provider 网络
// 抖动需要秒级恢复窗口，毫秒级退避只会连续撞墙。
var requestRetryDelays = []time.Duration{10 * time.Second, 15 * time.Second, 30 * time.Second}

// retryAfterCap 限制 provider Retry-After 头的最大生效值，避免异常值让回合
// 挂起过久（等待过程始终可被 ctx 取消）。
const retryAfterCap = 60 * time.Second

// doRequestWithRetry executes only the request-establishment phase with retry.
// Once a streaming response has been accepted, the caller owns the body and
// retries are deliberately not attempted: replaying a partially consumed
// stream could duplicate assistant text or tool calls.
func (c *Client) doRequestWithRetry(ctx context.Context, cfg Config, stream bool, buildRequest func() (*http.Request, error)) (*http.Response, error) {
	attempts := cfg.RetryCount + 1
	if attempts < 1 {
		attempts = 1
	}

	// Derive the transport from the exact configuration captured by the
	// request. Hot reloads can no longer splice a new timeout into old work.
	client := c.httpClientForConfig(cfg, stream)
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
	return waitForRequestRetryAfter(ctx, attempt, 0)
}

func waitForRequestRetryAfter(ctx context.Context, attempt int, retryAfter time.Duration) error {
	delay := requestRetryDelay(attempt, retryAfter)
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

// requestRetryDelay 返回第 attempt 次（0 起）重试前的等待时长：固定阶梯
// 10s/15s/30s，provider Retry-After 更大时从其值，整体封顶 retryAfterCap。
func requestRetryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := requestRetryDelays[len(requestRetryDelays)-1]
	if attempt < len(requestRetryDelays) {
		delay = requestRetryDelays[attempt]
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > retryAfterCap {
		delay = retryAfterCap
	}
	return delay
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

func (c *Client) httpClientForConfig(cfg Config, stream bool) *http.Client {
	c.mu.RLock()
	template := c.httpClient
	c.mu.RUnlock()
	client := &http.Client{}
	if template != nil {
		*client = *template
	}
	client.Timeout = cfg.Timeout
	if stream {
		client.Timeout = 0
	}
	return client
}

func (c *Client) setRequestHeaders(req *http.Request) {
	c.setRequestHeadersForConfig(req, c.CurrentModelConfig())
}

func (c *Client) setRequestHeadersForConfig(req *http.Request, cfg Config) {
	req.Header.Set("Content-Type", "application/json")
	for name, value := range cfg.Headers {
		req.Header.Set(name, value)
	}
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
	c.httpClient = httpClientWithProxy(cfg.Proxy)
	return nil
}

// RunMessage 执行一次最小“输入 -> 输出”调用。
// 当前支持消息列表输入。
func (c *Client) RunMessage(ctx context.Context, messages []message.Message) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("messages 不能为空")
	}

	// 出站前修复工具调用配对，与 StreamMessage 入口保持一致。
	messages, _ = RepairToolCallPairs(messages)

	cfg := c.CurrentModelConfig()
	if shouldUseResponsesAPI(cfg) {
		return c.runResponsesMessage(ctx, cfg, messages)
	}

	adapter := SelectModelAdapter(cfg)
	prepared, err := c.prepareTools(adapter, nil)
	if err != nil {
		return "", fmt.Errorf("准备 %s 工具失败: %w", adapter.Name(), err)
	}
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return "", fmt.Errorf("校验请求体配置失败: %w", err)
	}
	reqBody, err := adapter.BuildChatCompletionsRequest(cfg, messages, prepared, false)
	if err != nil {
		return "", fmt.Errorf("构造 %s 请求失败: %w", adapter.Name(), err)
	}

	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveChatCompletionsExtraRequestBody(cfg))
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
		c.setRequestHeadersForConfig(req, cfg)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, respBytes, err, "模型接口")
	}
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
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
