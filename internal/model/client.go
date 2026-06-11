package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/message"
	"io"
	"net/http"
	"strings"
	"sync"
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
	return &Client{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
	}
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
	return c.cfg
}

func (c *Client) ApplyModelConfig(cfg Config) error {
	cfg = fillConfigDefaults(cfg)
	if strings.TrimSpace(cfg.APIKey) == "" {
		cfg.APIKey = loadAPIKeyByEnvName(cfg.APIKeyEnvName, nil)
	}
	cfg.APIKey = sanitizeAPIKey(cfg.Provider, cfg.APIKey)

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

	reqBody := ChatCompletionsRequest{
		Model:    cfg.Model,
		Messages: messages,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.APIBaseURL+cfg.APIPath,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	c.setRequestHeaders(req)

	resp, err := c.httpClient.Do(req)
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
