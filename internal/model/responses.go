package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"sort"
	"strings"
)

const (
	responsesProviderTransport = "openai-responses"
	responsesProviderVersion   = 1
)

// responsesProviderData 是固定在消息 ProviderData 上的不透明信封。
// transport/version 校验由 decodeResponsesProviderData 完成，其余字段透传。
type responsesProviderData struct {
	Transport   string            `json:"transport"`
	Version     int               `json:"version"`
	OutputItems []json.RawMessage `json:"output_items"`
}

// encodeResponsesProviderData 将原始 output items 打包进 provider 信封。
func encodeResponsesProviderData(items []json.RawMessage) (json.RawMessage, error) {
	if err := validateResponsesOutputItems(items); err != nil {
		return nil, err
	}
	envelope := responsesProviderData{
		Transport:   responsesProviderTransport,
		Version:     responsesProviderVersion,
		OutputItems: items,
	}
	return json.Marshal(envelope)
}

// decodeResponsesProviderData 校验信封并返回深拷贝的 output items。
// 任何校验失败都返回 false，调用方应回退到通用投影。
func decodeResponsesProviderData(raw json.RawMessage) ([]json.RawMessage, bool) {
	var envelope responsesProviderData
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	if envelope.Transport != responsesProviderTransport || envelope.Version != responsesProviderVersion {
		return nil, false
	}
	if err := validateResponsesOutputItems(envelope.OutputItems); err != nil {
		return nil, false
	}
	items := make([]json.RawMessage, len(envelope.OutputItems))
	for i, item := range envelope.OutputItems {
		items[i] = append(json.RawMessage(nil), item...)
	}
	return items, true
}

// validateResponsesOutputItems 校验每个 output item 是带非空 type 的 JSON object。
// 未知 item type 不拒绝，以便前向兼容。
func validateResponsesOutputItems(items []json.RawMessage) error {
	for i, item := range items {
		if len(bytes.TrimSpace(item)) == 0 || !json.Valid(item) || bytes.TrimSpace(item)[0] != '{' {
			return fmt.Errorf("Responses output item %d 不是有效 JSON object", i)
		}
		var view struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &view); err != nil || strings.TrimSpace(view.Type) == "" {
			return fmt.Errorf("Responses output item %d 缺少非空 type", i)
		}
	}
	return nil
}

// responsesRequest models the OpenAI Responses API without coupling the rest
// of the agent loop to provider-specific input/output item shapes.
type responsesRequest struct {
	Model  string            `json:"model"`
	Input  []json.RawMessage `json:"input"`
	Stream bool              `json:"stream"`
	Tools  []responsesTool   `json:"tools,omitempty"`
}

// responsesItem 用于把通用消息投影为 Responses input item。
type responsesItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesTool 显式序列化 strict:false（不使用 omitempty），确保向服务端
// 声明工具参数不做 strict JSON Schema 校验，兼容宽松的 MCP schema。
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// responsesAPIResponse 的 Output 保留为 raw items，作为权威完成快照，
// 由 completedResponsesEvent 统一验证并生成最终 StreamEvent。
type responsesAPIResponse struct {
	Status string            `json:"status,omitempty"`
	Output []json.RawMessage `json:"output"`
	Usage  *Usage            `json:"usage,omitempty"`
	Error  *responsesError   `json:"error,omitempty"`
}

// responsesOutputItemView 是已知字段的轻量视图，用于从 raw item 中提取
// 文本和函数调用；未知字段原样保留在 raw bytes 中。
type responsesOutputItemView struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
}

type responsesError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

type responsesStreamEvent struct {
	Type        string               `json:"type"`
	Delta       string               `json:"delta,omitempty"`
	ItemID      string               `json:"item_id,omitempty"`
	OutputIndex int                  `json:"output_index,omitempty"`
	Item        json.RawMessage      `json:"item,omitempty"`
	Response    *responsesAPIResponse `json:"response,omitempty"`
	Error       *responsesError      `json:"error,omitempty"`
}

func shouldUseResponsesAPI(cfg Config) bool {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if strings.Contains(transport, "response") {
		return true
	}
	if transport != "" {
		return false
	}
	path := strings.TrimRight(strings.ToLower(strings.TrimSpace(cfg.APIPath)), "/")
	return strings.HasSuffix(path, "/responses")
}

func buildResponsesRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool) (responsesRequest, error) {
	input, err := buildResponsesInput(messages)
	if err != nil {
		return responsesRequest{}, err
	}
	req := responsesRequest{Model: cfg.Model, Input: input, Stream: stream}
	for _, tool := range tools {
		parameters := tool.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		req.Tools = append(req.Tools, responsesTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: false,
		})
	}
	return req, nil
}

func buildResponsesInput(messages []message.Message) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		calls := messageToolCalls(msg)
		results := messageToolResults(msg)

		// 有效 ProviderData 时权威重放原始 output items，跳过通用投影，
		// 避免重复生成 assistant message / function_call。
		if len(results) == 0 && len(msg.ProviderData) != 0 {
			if replayed, ok := decodeResponsesProviderData(msg.ProviderData); ok {
				items = append(items, replayed...)
				continue
			}
		}

		// Keep ordinary text (including assistant reasoning preceding a call) as a
		// message item, then append native function_call/function_call_output items.
		if len(results) == 0 && (strings.TrimSpace(msg.Content) != "" || len(msg.Parts) != 0) {
			content, err := responsesMessageContent(msg)
			if err != nil {
				return nil, err
			}
			item, err := json.Marshal(responsesItem{Role: string(msg.Role), Content: content})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		for _, call := range calls {
			arguments := strings.TrimSpace(string(call.Input))
			if arguments == "" || !json.Valid([]byte(arguments)) {
				arguments = "{}"
			}
			item, err := json.Marshal(responsesItem{
				Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: arguments,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		for _, result := range results {
			item, err := json.Marshal(responsesItem{
				Type: "function_call_output", CallID: result.ToolUseID, Output: result.Content,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func responsesMessageContent(msg message.Message) (any, error) {
	if !hasImagePart(msg.Parts) {
		return msg.Content, nil
	}
	parts := make([]responsesContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case message.ContentPartText:
			parts = append(parts, responsesContentPart{Type: "input_text", Text: part.Text})
		case message.ContentPartImage:
			if part.Image == nil || len(part.Image.Data) == 0 {
				return nil, fmt.Errorf("OpenAI Responses 图片消息缺少已加载的附件")
			}
			mimeType := strings.TrimSpace(part.Image.MIMEType)
			if mimeType == "" {
				return nil, fmt.Errorf("OpenAI Responses 图片消息缺少 MIME 类型")
			}
			parts = append(parts, responsesContentPart{
				Type: "input_image", ImageURL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Image.Data),
			})
		default:
			return nil, fmt.Errorf("OpenAI Responses 消息包含未知内容块类型: %q", part.Type)
		}
	}
	return parts, nil
}

func messageToolCalls(msg message.Message) []message.ToolCall {
	if len(msg.ToolUses) != 0 {
		return msg.ToolUses
	}
	if msg.ToolUse != nil {
		return []message.ToolCall{*msg.ToolUse}
	}
	return nil
}

func messageToolResults(msg message.Message) []message.ToolResult {
	if len(msg.ToolResults) != 0 {
		return msg.ToolResults
	}
	if msg.ToolResult != nil {
		return []message.ToolResult{*msg.ToolResult}
	}
	return nil
}

func (c *Client) runResponsesMessage(ctx context.Context, cfg Config, messages []message.Message) (string, error) {
	reqBody, err := buildResponsesRequest(cfg, messages, nil, false)
	if err != nil {
		return "", fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return "", fmt.Errorf("校验请求体配置失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed responsesAPIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("解析 Responses JSON 失败: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}
	var text strings.Builder
	for _, raw := range parsed.Output {
		var view responsesOutputItemView
		if err := json.Unmarshal(raw, &view); err != nil {
			return "", fmt.Errorf("解析 Responses output item 失败: %w", err)
		}
		if view.Type != "message" {
			continue
		}
		for _, part := range view.Content {
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
	}
	content := strings.TrimSpace(text.String())
	if content == "" {
		return "", fmt.Errorf("模型接口返回了空内容")
	}
	return content, nil
}

func (c *Client) streamResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequest(cfg, messages, tools, true)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, true, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("模型接口返回异常状态 %d，且读取错误响应失败: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	events := make(chan StreamEvent)
	go c.consumeResponsesStream(ctx, resp, events)
	return events, nil
}

func (c *Client) nonStreamingResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequest(cfg, messages, tools, false)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed responsesAPIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 Responses JSON 失败: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}
	if parsed.Status != "" && parsed.Status != "completed" {
		return nil, fmt.Errorf("Responses API 响应未完成 (status=%s)", parsed.Status)
	}
	event, err := completedResponsesEvent(parsed.Output, parsed.Usage)
	if err != nil {
		return nil, err
	}
	events := make(chan StreamEvent, 8)
	if event.Usage != nil {
		events <- StreamEvent{Usage: event.Usage}
	}
	if event.Delta != "" {
		events <- StreamEvent{Delta: event.Delta}
	}
	if len(event.ToolCalls) != 0 {
		events <- StreamEvent{ToolCalls: event.ToolCalls}
	}
	if len(event.ProviderData) != 0 {
		events <- StreamEvent{ProviderData: event.ProviderData}
	}
	events <- StreamEvent{Done: true}
	close(events)
	return events, nil
}

// completedResponsesEvent 从权威完成快照生成最终事件：
// 1. 验证 raw output items；
// 2. 提取所有 message item 文本；
// 3. 原子验证所有 function_call arguments；
// 4. 任一调用非法时返回 error，不返回部分 calls；
// 5. 编码 ProviderData；
// 6. 返回 Done 事件。
func completedResponsesEvent(output []json.RawMessage, usage *Usage) (StreamEvent, error) {
	if err := validateResponsesOutputItems(output); err != nil {
		return StreamEvent{}, err
	}
	var calls []message.ToolCall
	var text strings.Builder
	for _, raw := range output {
		var view responsesOutputItemView
		if err := json.Unmarshal(raw, &view); err != nil {
			return StreamEvent{}, fmt.Errorf("解析 Responses output item 失败: %w", err)
		}
		switch view.Type {
		case "message":
			for _, part := range view.Content {
				if part.Type == "output_text" && part.Text != "" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			call, err := responseToolCall(view)
			if err != nil {
				return StreamEvent{}, err
			}
			calls = append(calls, call)
		}
	}
	providerData, err := encodeResponsesProviderData(output)
	if err != nil {
		return StreamEvent{}, err
	}
	return StreamEvent{
		Delta:        text.String(),
		ToolCalls:    calls,
		ProviderData: providerData,
		Usage:        usage,
		Done:         true,
	}, nil
}

func responseToolCall(view responsesOutputItemView) (message.ToolCall, error) {
	id := view.CallID
	if id == "" {
		id = view.ID
	}
	input, err := decodeToolArguments("Responses API", id, view.Name, []byte(view.Arguments))
	if err != nil {
		return message.ToolCall{}, err
	}
	return message.ToolCall{ID: id, Name: view.Name, Input: input}, nil
}

type activeResponseToolCall struct {
	item json.RawMessage
	args strings.Builder
	view responsesOutputItemView
}

func (c *Client) consumeResponsesStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
	defer close(events)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, streamScannerInitialBufferBytes), streamScannerMaxTokenBytes)
	// 按 output_index 收集 response.output_item.done 的 raw items，作为 completed 快照
	active := make(map[int]json.RawMessage)
	completed := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event responsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("解析 Responses 流式数据失败: %w", err)})
			return
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" && !emitStreamEvent(ctx, events, StreamEvent{Delta: event.Delta}) {
				return
			}
		case "response.output_item.done":
			if len(event.Item) != 0 {
				active[event.OutputIndex] = append(json.RawMessage(nil), event.Item...)
			}
		case "response.completed":
			output := responsesRawOutput(active)
			// 兼容网关未在 completed 事件携带 output 时，使用收集到的 raw items
			if event.Response != nil && len(event.Response.Output) != 0 {
				output = event.Response.Output
			}
			var usage *Usage
			if event.Response != nil {
				usage = event.Response.Usage
			}
			finalEvent, err := completedResponsesEvent(output, usage)
			if err != nil {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
				return
			}
			if !emitFinalResponsesEvent(ctx, events, finalEvent) {
				return
			}
			completed = true
			return
		case "response.failed", "response.incomplete", "error":
			msg := "Responses API 请求失败"
			if event.Error != nil && event.Error.Message != "" {
				msg = event.Error.Message
			} else if event.Response != nil && event.Response.Error != nil && event.Response.Error.Message != "" {
				msg = event.Response.Error.Message
			}
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("模型接口返回错误: %s", msg)})
			return
		}
	}
	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取 Responses 流式响应失败: %w", err)})
		return
	}
	if !completed {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("Responses stream ended before response.completed")})
	}
}

// emitFinalResponsesEvent 把权威完成事件作为单个原子事件投递，保证
// usage、delta、tool calls、provider data 与 Done 在同一事件上出现，
// 与完成的响应快照一一对应。
func emitFinalResponsesEvent(ctx context.Context, events chan<- StreamEvent, ev StreamEvent) bool {
	return emitStreamEvent(ctx, events, ev)
}

// responsesRawOutput 按 output_index 排序收集 raw output items。
func responsesRawOutput(active map[int]json.RawMessage) []json.RawMessage {
	indexes := make([]int, 0, len(active))
	for index := range active {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		if raw := active[index]; len(raw) != 0 {
			output = append(output, raw)
		}
	}
	return output
}
