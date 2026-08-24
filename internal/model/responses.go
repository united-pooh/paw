package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"sort"
	"strings"
	"time"
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
	Model     string            `json:"model"`
	Input     []json.RawMessage `json:"input"`
	Stream    bool              `json:"stream"`
	Reasoning RequestBody       `json:"reasoning,omitempty"`
	Tools     []responsesTool   `json:"tools,omitempty"`
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
}

type responsesFunctionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesTool always serializes strict explicitly. Adapter preparation owns
// the value and the wire schema, independently from the Responses transport.
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
// 文本、reasoning summary 和函数调用；未知字段原样保留在 raw bytes 中。
type responsesOutputItemView struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Summary   json.RawMessage `json:"summary,omitempty"`
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
	Type        string                `json:"type"`
	Delta       string                `json:"delta,omitempty"`
	Text        string                `json:"text,omitempty"`
	ItemID      string                `json:"item_id,omitempty"`
	OutputIndex int                   `json:"output_index,omitempty"`
	CallID      string                `json:"call_id,omitempty"`
	Name        string                `json:"name,omitempty"`
	Item        json.RawMessage       `json:"item,omitempty"`
	Response    *responsesAPIResponse `json:"response,omitempty"`
	Error       *responsesError       `json:"error,omitempty"`
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

func responsesReasoningSummary(view responsesOutputItemView) string {
	var structured []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if json.Unmarshal(view.Summary, &structured) == nil {
		parts := make([]string, 0, len(structured))
		for _, summary := range structured {
			if summary.Type != "summary_text" {
				continue
			}
			if text := strings.TrimSpace(summary.Text); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}
	var summaries []string
	if json.Unmarshal(view.Summary, &summaries) != nil {
		return ""
	}
	parts := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if text := strings.TrimSpace(summary); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func responsesReasoningText(view responsesOutputItemView) string {
	if summary := responsesReasoningSummary(view); summary != "" {
		return summary
	}
	parts := make([]string, 0, len(view.Content))
	for _, content := range view.Content {
		if content.Type != "reasoning_text" {
			continue
		}
		if text := strings.TrimSpace(content.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func isGPTResponsesConfig(cfg Config) bool {
	adapter := strings.ToLower(strings.TrimSpace(cfg.Adapter))
	if adapter == "gpt" {
		return true
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	model := strings.ToLower(strings.TrimSpace(cfg.Model))
	return provider == "gpt" || provider == "openai" || strings.HasPrefix(model, "gpt-")
}

func defaultResponsesReasoning(cfg Config) RequestBody {
	if !isGPTResponsesConfig(cfg) {
		return nil
	}
	return RequestBody{"summary": "auto"}
}

func effectiveResponsesExtraRequestBody(cfg Config) RequestBody {
	body := CloneRequestBody(EffectiveExtraRequestBody(cfg))
	if body == nil {
		body = make(RequestBody)
	}

	if effort, ok := body["reasoning_effort"]; ok {
		reasoning, reasoningOK := jsonObject(body["reasoning"])
		if !reasoningOK {
			reasoning = make(RequestBody)
		}
		if _, exists := reasoning["effort"]; !exists {
			reasoning["effort"] = cloneJSONValue(effort)
		}
		body["reasoning"] = reasoning
		delete(body, "reasoning_effort")
	}
	// thinking is an Anthropic Messages field and is not valid Responses input.
	delete(body, "thinking")
	return body
}

func buildResponsesRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (responsesRequest, error) {
	input, err := buildResponsesInput(messages)
	if err != nil {
		return responsesRequest{}, err
	}
	// wire 层兜底：隔离孤儿 function_call_output、给悬空 function_call 补合成
	// output（覆盖 ProviderData 重放与结构化字段不一致的崩溃场景）。
	input, _ = repairResponsesInputItems(input)
	if err := validateResponsesInputItems(input); err != nil {
		return responsesRequest{}, err
	}
	req := responsesRequest{
		Model:     cfg.Model,
		Input:     input,
		Stream:    stream,
		Reasoning: defaultResponsesReasoning(cfg),
	}
	for _, tool := range tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		req.Tools = append(req.Tools, responsesTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict,
		})
	}
	return req, nil
}

func validateResponsesInputItems(items []json.RawMessage) error {
	for i, raw := range items {
		var view struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return fmt.Errorf("Responses input item %d 不是有效 JSON: %w", i, err)
		}
		if view.Type != "function_call_output" {
			continue
		}

		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return fmt.Errorf("Responses function_call_output item %d 不是有效 JSON object: %w", i, err)
		}
		var callID string
		callIDRaw, ok := item["call_id"]
		if !ok || json.Unmarshal(callIDRaw, &callID) != nil || strings.TrimSpace(callID) == "" {
			return fmt.Errorf("Responses function_call_output item %d 缺少非空 call_id", i)
		}
		outputRaw, ok := item["output"]
		if !ok {
			return fmt.Errorf("Responses function_call_output item %d 缺少 output", i)
		}
		var output string
		if err := json.Unmarshal(outputRaw, &output); err != nil {
			return fmt.Errorf("Responses function_call_output item %d 的 output 必须是字符串", i)
		}
	}
	return nil
}

// responsesInputReplayStrippedFields 是 provider 在 output item 上返回、但
// Responses input 端不接受的字段。status 由捕获方 provider 合法产出，
// 但跨 provider 切换模型时（如 deepseek 产生的历史切到 GPT 系网关），
// 严格校验的端点对 item 级未知字段直接报 unknown_parameter
// （input[N].status），导致带思考/工具历史的请求全部 400。
var responsesInputReplayStrippedFields = []string{"status"}

// stripResponsesOutputOnlyFields 清洗回放 item 中仅 output 侧合法的字段。
// 解析或重编码失败时原样返回：宁可让 provider 报错也不静默改写内容。
func stripResponsesOutputOnlyFields(item json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item, &fields); err != nil {
		return item
	}
	changed := false
	for _, key := range responsesInputReplayStrippedFields {
		if _, ok := fields[key]; ok {
			delete(fields, key)
			changed = true
		}
	}
	if !changed {
		return item
	}
	cleaned, err := json.Marshal(fields)
	if err != nil {
		return item
	}
	return cleaned
}

func buildResponsesInput(messages []message.Message) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		calls := messageToolCalls(msg)
		results := messageToolResults(msg)

		// 有效 ProviderData 时权威重放原始 output items，跳过通用投影，
		// 避免重复生成 assistant message / function_call。回放前剥离仅
		// output 侧合法的字段（status），兼容严格校验入参的端点（如跨
		// provider 切换后的 GPT 系网关）。
		if len(results) == 0 && len(msg.ProviderData) != 0 {
			if replayed, ok := decodeResponsesProviderData(msg.ProviderData); ok {
				for _, item := range replayed {
					items = append(items, stripResponsesOutputOnlyFields(item))
				}
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
			item, err := json.Marshal(responsesFunctionCallOutputItem{
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
	if len(msg.AssistantParts) > 0 {
		calls := make([]message.ToolCall, 0)
		for _, part := range msg.AssistantParts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
		return calls
	}
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
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, nil, false)
	if err != nil {
		return "", fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return "", fmt.Errorf("校验请求体配置失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, data, err, "模型接口")
	}
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
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

func (c *Client) streamResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, tools, true)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	requestAttempts := 0
	buildRequest := func() (*http.Request, error) {
		requestAttempts++
		req, buildErr := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if buildErr != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", buildErr)
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	}
	initialResp, err := c.doRequestWithRetry(ctx, cfg, true, buildRequest)
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	if initialResp.StatusCode < 200 || initialResp.StatusCode >= 300 {
		return nil, providerHTTPErrorFromResponse(initialResp, "模型接口")
	}

	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		attempts := cfg.RetryCount + 2 - requestAttempts
		if attempts < 1 {
			attempts = 1
		}
		client := c.httpClientForConfig(cfg, true)
		for attempt := 0; attempt < attempts; attempt++ {
			resp := initialResp
			var requestErr error
			if attempt > 0 {
				var req *http.Request
				req, requestErr = buildRequest()
				if requestErr == nil {
					resp, requestErr = client.Do(req)
				}
			}
			if requestErr != nil {
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				if attempt < attempts-1 && isRetryableRequestError(ctx, requestErr) {
					if waitErr := waitForRequestRetry(ctx, attempt); waitErr == nil {
						continue
					} else {
						requestErr = waitErr
					}
				}
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("调用模型接口失败: %w", requestErr)})
				return
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				providerErr := providerHTTPErrorFromResponse(resp, "模型接口")
				if attempt < attempts-1 && isRetryableHTTPStatus(providerErr.StatusCode) {
					if waitErr := waitForRequestRetryAfter(ctx, attempt, providerErr.RetryAfter); waitErr == nil {
						continue
					} else {
						_ = emitStreamEvent(ctx, events, StreamEvent{Err: waitErr})
						return
					}
				}
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: providerErr})
				return
			}

			result := c.consumeResponsesStream(ctx, resp, events, streamIdleTimeout(cfg))
			if result.err == nil {
				return
			}
			if attempt < attempts-1 && !result.madeProgress && isRetryableResponsesStreamError(ctx, result.err) {
				if waitErr := waitForRequestRetryAfter(ctx, attempt, result.retryAfter); waitErr == nil {
					continue
				} else {
					_ = emitStreamEvent(ctx, events, StreamEvent{Err: waitErr})
					return
				}
			}
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: result.err})
			return
		}
	}()
	return events, nil
}

func (c *Client) nonStreamingResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, tools, false)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, data, err, "模型接口")
	}
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
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
	if event.Thinking != "" {
		events <- StreamEvent{Thinking: event.Thinking}
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
//  1. 验证 raw output items；
//  2. 提取所有 message item 文本；流式调用仅在没有文本 delta 时回退使用；
//  3. 提取所有 function_call arguments；非法参数记录在对应 ToolCall，
//     由 Runner 生成可重试错误结果，不影响其他调用；
//  5. 编码 ProviderData；
//  6. 返回 Done 事件。
func completedResponsesEvent(output []json.RawMessage, usage *Usage) (StreamEvent, error) {
	if err := validateResponsesOutputItems(output); err != nil {
		return StreamEvent{}, err
	}
	var calls []message.ToolCall
	var thinking strings.Builder
	var text strings.Builder
	for _, raw := range output {
		var view responsesOutputItemView
		if err := json.Unmarshal(raw, &view); err != nil {
			return StreamEvent{}, fmt.Errorf("解析 Responses output item 失败: %w", err)
		}
		switch view.Type {
		case "reasoning":
			if reasoning := responsesReasoningText(view); reasoning != "" {
				if thinking.Len() > 0 {
					thinking.WriteString("\n\n")
				}
				thinking.WriteString(reasoning)
			}
		case "message":
			for _, part := range view.Content {
				if part.Type == "output_text" && part.Text != "" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			call, err := responseToolCall(view)
			if err != nil {
				call.InputError = fmt.Sprintf("Responses API 工具参数无法解析，可修正参数后重试: %v", err)
			}
			calls = append(calls, call)
		}
	}
	providerData, err := encodeResponsesProviderData(output)
	if err != nil {
		return StreamEvent{}, err
	}
	return StreamEvent{
		Thinking:     thinking.String(),
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
		return message.ToolCall{ID: id, Name: view.Name, Input: json.RawMessage(`{}`)}, err
	}
	return message.ToolCall{ID: id, Name: view.Name, Input: input}, nil
}

type activeResponseToolCall struct {
	item   json.RawMessage
	args   strings.Builder
	id     string
	callID string
	name   string
}

type responsesStreamFailure struct {
	EventType string
	Type      string
	Code      string
	Message   string
	RequestID string
	Retryable bool
	cause     error
}

func (e *responsesStreamFailure) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Responses API 请求失败"
	}
	detail := message
	if e.Code != "" {
		detail += " (code=" + e.Code + ")"
	}
	if e.RequestID != "" {
		detail += " (request_id=" + e.RequestID + ")"
	}
	return "模型接口返回错误: " + detail
}

func (e *responsesStreamFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type responsesStreamResult struct {
	err          error
	madeProgress bool
	retryAfter   time.Duration
}

func isRetryableResponsesStreamError(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var failure *responsesStreamFailure
	if errors.As(err, &failure) {
		return failure.Retryable
	}
	return isRetryableRequestError(ctx, err)
}

func responsesFailureFromEvent(event responsesStreamEvent, header http.Header) *responsesStreamFailure {
	providerErr := event.Error
	if providerErr == nil && event.Response != nil {
		providerErr = event.Response.Error
	}
	failure := &responsesStreamFailure{
		EventType: event.Type,
		RequestID: providerRequestID(header),
		Retryable: event.Type == "response.failed" || event.Type == "response.incomplete",
	}
	if providerErr != nil {
		failure.Type = strings.TrimSpace(providerErr.Type)
		failure.Code = strings.TrimSpace(providerErr.Code)
		failure.Message = strings.TrimSpace(providerErr.Message)
	}
	// Request/auth/schema failures are deterministic and must not be replayed.
	nonRetryable := strings.ToLower(failure.Type + " " + failure.Code)
	for _, marker := range []string{"invalid", "authentication", "authorization", "permission", "not_found", "unsupported", "context_length", "rate_limit"} {
		if strings.Contains(nonRetryable, marker) {
			failure.Retryable = false
			break
		}
	}
	return failure
}

func (c *Client) consumeResponsesStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent, idleTimeout time.Duration) responsesStreamResult {
	body := newStreamIdleWatchdog(ctx, resp.Body, idleTimeout)
	defer func() {
		_ = body.Close()
	}()
	result := responsesStreamResult{retryAfter: providerRetryAfter(resp.Header, time.Now())}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, streamScannerInitialBufferBytes), streamScannerMaxTokenBytes)
	active := make(map[int]*activeResponseToolCall)
	sawOutputTextDelta := false
	sawReasoningDelta := false

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
			result.err = &responsesStreamFailure{EventType: "decode", Message: "解析 Responses 流式数据失败", RequestID: providerRequestID(resp.Header), Retryable: true, cause: err}
			return result
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				result.madeProgress = true
				if !emitStreamEvent(ctx, events, StreamEvent{Delta: event.Delta}) {
					result.err = ctx.Err()
					return result
				}
				sawOutputTextDelta = true
			}
		case "response.reasoning_summary_text.delta", "response.reasoning.delta", "response.reasoning_text.delta":
			// reasoning_summary_text.delta / reasoning.delta 是 OpenAI 官方事件；
			// reasoning_text.delta 是 DeepSeek Responses API 的逐 token CoT 事件。
			// 旧实现漏掉后者，DeepSeek 的全部思考 delta 被静默丢弃，只能从
			// response.completed 一次性回放——表现为思考内容瞬间全量输出。
			if event.Delta != "" {
				result.madeProgress = true
				if !emitStreamEvent(ctx, events, StreamEvent{Thinking: event.Delta}) {
					result.err = ctx.Err()
					return result
				}
				sawReasoningDelta = true
			}
		case "response.reasoning_text.done":
			// DeepSeek 的全量 CoT 兜底：仅在一条流式 delta 都没收到时回放全文，
			// 避免与已播放的增量重复（received 时 completed 兜底会被抑制）。
			if !sawReasoningDelta && event.Text != "" {
				result.madeProgress = true
				if !emitStreamEvent(ctx, events, StreamEvent{Thinking: event.Text}) {
					result.err = ctx.Err()
					return result
				}
				sawReasoningDelta = true
			}
		case "response.output_item.added":
			if len(event.Item) != 0 {
				result.madeProgress = true
				call := &activeResponseToolCall{item: append(json.RawMessage(nil), event.Item...)}
				var view responsesOutputItemView
				if json.Unmarshal(event.Item, &view) == nil && view.Type == "function_call" {
					call.id, call.callID, call.name = view.ID, view.CallID, view.Name
					call.args.WriteString(view.Arguments)
					active[event.OutputIndex] = call
				}
			}
		case "response.function_call_arguments.delta":
			result.madeProgress = true
			call := active[event.OutputIndex]
			if call == nil {
				call = &activeResponseToolCall{id: event.ItemID, callID: event.CallID, name: event.Name}
				active[event.OutputIndex] = call
			} else {
				if event.ItemID != "" {
					call.id = event.ItemID
				}
				if event.CallID != "" {
					call.callID = event.CallID
				}
				if event.Name != "" {
					call.name = event.Name
				}
			}
			call.args.WriteString(event.Delta)
			if call.item == nil {
				call.item = json.RawMessage(fmt.Sprintf(`{"type":"function_call","id":%q,"call_id":%q,"name":%q}`, call.id, call.callID, call.name))
			}
		case "response.output_item.done":
			if len(event.Item) != 0 {
				result.madeProgress = true
				call := active[event.OutputIndex]
				if call == nil {
					call = &activeResponseToolCall{}
					active[event.OutputIndex] = call
				}
				call.item = append(json.RawMessage(nil), event.Item...)
			}
		case "response.completed":
			output := responsesRawOutputWithDeltas(active)
			if event.Response != nil && len(event.Response.Output) != 0 {
				output = mergeResponsesOutputWithDeltas(event.Response.Output, active)
			}
			var usage *Usage
			if event.Response != nil {
				usage = event.Response.Usage
			}
			finalEvent, err := completedResponsesEvent(output, usage)
			if err != nil {
				result.err = err
				return result
			}
			if sawOutputTextDelta {
				finalEvent.Delta = ""
			}
			if sawReasoningDelta {
				finalEvent.Thinking = ""
			}
			if !emitFinalResponsesEvent(ctx, events, finalEvent) {
				result.err = ctx.Err()
				return result
			}
			return result
		case "response.failed", "response.incomplete", "error":
			result.err = responsesFailureFromEvent(event, resp.Header)
			return result
		}
	}
	if body.TimedOut() {
		result.err = &responsesStreamFailure{EventType: "stream_idle", Message: fmt.Sprintf("模型流 %s 无任何数据，连接已中断（可能是 provider 网络抖动）", idleTimeout), RequestID: providerRequestID(resp.Header), Retryable: true}
		return result
	}
	if err := scanner.Err(); err != nil {
		if ctx != nil && ctx.Err() != nil {
			result.err = ctx.Err()
		} else {
			result.err = &responsesStreamFailure{EventType: "stream_read", Message: "读取 Responses 流式响应失败", RequestID: providerRequestID(resp.Header), Retryable: true, cause: err}
		}
		return result
	}
	result.err = &responsesStreamFailure{EventType: "unexpected_eof", Message: "Responses stream ended before response.completed", RequestID: providerRequestID(resp.Header), Retryable: true}
	return result
}

func responsesRawOutputWithDeltas(active map[int]*activeResponseToolCall) []json.RawMessage {
	indexes := make([]int, 0, len(active))
	for index := range active {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		call := active[index]
		if call == nil || len(call.item) == 0 {
			continue
		}
		if len(call.args.String()) != 0 {
			call.item = responseItemWithArguments(call.item, call.args.String())
		}
		output = append(output, call.item)
	}
	return output
}

func mergeResponsesOutputWithDeltas(output []json.RawMessage, active map[int]*activeResponseToolCall) []json.RawMessage {
	merged := append([]json.RawMessage(nil), output...)
	for index, call := range active {
		if call == nil || call.args.Len() == 0 {
			continue
		}
		if index >= 0 && index < len(merged) {
			merged[index] = responseItemWithArguments(merged[index], call.args.String())
		} else if len(call.item) != 0 {
			merged = append(merged, responseItemWithArguments(call.item, call.args.String()))
		}
	}
	return merged
}

func responseItemWithArguments(raw json.RawMessage, arguments string) json.RawMessage {
	var item map[string]any
	if json.Unmarshal(raw, &item) != nil {
		return raw
	}
	item["arguments"] = arguments
	encoded, err := json.Marshal(item)
	if err != nil {
		return raw
	}
	return encoded
}

// emitFinalResponsesEvent 把权威完成事件作为单个原子事件投递。文本已通过
// 流式 delta 投递时不再重放完成快照；usage、tool calls、provider data 与
// Done 仍保持在同一完成事件上。
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
