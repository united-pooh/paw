package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/message"
	"io"
	"net/http"
	"strings"
)

// StreamEvent 表示一次流式输出事件。
// 一个事件只表达一种状态：
// 1) Delta 非空：收到一段增量文本
// 2) Done=true：流结束
// 3) Err 非空：流中出现错误
type StreamEvent struct {
	Delta string
	Done  bool
	Err   error
	Usage *Usage
}

// chatCompletionsStreamResponse 只建模流式响应里当前需要的字段。
// 不需要的字段故意省略，避免上层依赖供应商完整响应结构。
type chatCompletionsStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// StreamMessage 以 SSE 方式返回模型增量输出。
// 调用方拿到 channel 后持续 range：
// - 处理 Delta
// - 处理 Err
// - 收到 Done 后结束当前轮次
func (c *Client) StreamMessage(ctx context.Context, messages []message.Message) (<-chan StreamEvent, error) {
	// 模型接口要求至少有一条输入消息。
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 不能为空")
	}

	cfg := c.CurrentModelConfig()

	// 启用 stream=true，让服务端按 SSE 增量返回。
	reqBody := ChatCompletionsRequest{
		Model:         cfg.Model,
		Messages:      messages,
		Stream:        true,
		StreamOptions: &StreamOptions{IncludeUsage: true},
	}

	// 组装 JSON 请求体。
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	// 将上层 context 透传到底层 HTTP 请求，支持超时/取消。
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.APIBaseURL+cfg.APIPath,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	// 当前本地网关沿用标准 JSON 请求体和 Bearer 鉴权头。
	c.setRequestHeaders(req)

	// 发起请求；如果失败直接返回同步错误。
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}

	// SSE 流建立失败（非 2xx）时，读取错误体后返回。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("模型接口返回异常状态 %d，且读取错误响应失败: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	// 这里开始转入异步消费：
	// - 调用方立即拿到 channel，不阻塞当前 goroutine
	// - 后台 goroutine 负责解析 SSE 并投递事件
	events := make(chan StreamEvent)
	go c.consumeStream(ctx, resp, events)
	return events, nil
}

func newStreamScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return scanner
}

func (c *Client) handleStreamLine(ctx context.Context, line string, events chan<- StreamEvent) (done bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false, nil
	}
	if !strings.HasPrefix(line, "data:") {
		return false, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	return c.handleStreamPayload(ctx, payload, events)
}

func (c *Client) handleStreamPayload(ctx context.Context, payload string, events chan<- StreamEvent) (done bool, err error) {
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
		return true, nil
	}

	chunk, err := decodeStreamChunk(payload)
	if err != nil {
		return false, fmt.Errorf("解析流式数据失败: %w", err)
	}
	if chunk.Error != nil {
		return false, fmt.Errorf("模型接口返回错误: %s", chunk.Error.Message)
	}

	return emitChunkEvents(ctx, chunk, events), nil
}

func decodeStreamChunk(payload string) (chatCompletionsStreamResponse, error) {
	var chunk chatCompletionsStreamResponse
	err := json.Unmarshal([]byte(payload), &chunk)
	return chunk, err
}

func emitChunkEvents(ctx context.Context, chunk chatCompletionsStreamResponse, events chan<- StreamEvent) (done bool) {
	if chunk.Usage != nil {
		if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Usage}) {
			return true
		}
	}
	if len(chunk.Choices) == 0 {
		return false
	}

	delta := chunk.Choices[0].Delta.Content
	if delta != "" {
		if !emitStreamEvent(ctx, events, StreamEvent{Delta: delta}) {
			return true
		}
	}

	finishReason := chunk.Choices[0].FinishReason
	if finishReason != nil && *finishReason != "" {
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
		return true
	}

	return false
}

// consumeStream 负责把 SSE 文本行转换为 StreamEvent。
func (c *Client) consumeStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
	// 无论以何种路径退出，都要关闭事件通道和响应体。
	defer close(events)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	// SSE 是按行传输，这里使用 Scanner 逐行读取。
	scanner := newStreamScanner(resp.Body)

	for scanner.Scan() {
		done, err := c.handleStreamLine(ctx, scanner.Text(), events)
		if err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
			return
		}
		if done {
			return
		}
	}

	// Scanner 自身出错（含网络中断）时回传错误事件。
	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取流式响应失败: %w", err)})
		return
	}

	// 正常读到 EOF 也视作结束，保证上层一定能收到 Done。
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
}

// emitStreamEvent 在 ctx 取消时主动放弃发送，避免 goroutine 卡在 channel send 上，
// 从而保证 resp.Body 的 defer Close 可以执行。
func emitStreamEvent(ctx context.Context, events chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
