package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type responsesEventSink func(StreamEvent) error

func (c *Client) consumeResponsesStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent, idleTimeout time.Duration) responsesStreamResult {
	return c.consumeResponsesAttempt(ctx, resp, func(event StreamEvent) error {
		if !emitStreamEvent(ctx, events, event) {
			return ctx.Err()
		}
		return nil
	}, idleTimeout)
}

func (c *Client) consumeResponsesAttempt(ctx context.Context, resp *http.Response, emit responsesEventSink, idleTimeout time.Duration) (result responsesStreamResult) {
	reader := resp.Body
	diagnostics := newResponsesDiagnosticBody(ctx, resp)
	if diagnostics != nil {
		reader = diagnostics
	}
	body := newStreamIdleWatchdog(ctx, reader, idleTimeout)
	defer func() {
		diagnostics.finish(ctx, result.err, body.TimedOut())
		_ = body.Close()
	}()
	result = responsesStreamResult{retryAfter: providerRetryAfter(resp.Header, time.Now())}

	scanner := bufio.NewScanner(body)
	scanner.Split(splitResponsesSSEEvents)
	scanner.Buffer(make([]byte, 0, streamScannerInitialBufferBytes), streamScannerMaxTokenBytes)
	active := make(map[int]*activeResponseToolCall)
	var streamedText strings.Builder
	var streamedThinking strings.Builder
	sawReasoningDelta := false
	var decodeFailure *responsesStreamFailure

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			result.err = err
			return result
		}
		payload, ok := responsesSSEPayload(scanner.Bytes())
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var event responsesStreamEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			if decodeFailure == nil {
				decodeFailure = &responsesStreamFailure{
					EventType: "decode",
					Message:   fmt.Sprintf("解析 Responses 流式数据失败（event_bytes=%d）: %v", len(payload), err),
					RequestID: providerRequestID(resp.Header),
					Retryable: true,
					cause:     err,
				}
			}
			continue
		}
		observeResponsesResponse(ctx, event.Response, event.Type)
		switch event.Type {
		case "response.completed", "response.failed", "response.incomplete", "error":
			if event.Response != nil {
				observeProviderUsage(ctx, event.Response.Usage)
			}
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				if decodeFailure == nil {
					streamedText.WriteString(event.Delta)
					if err := emit(withStreamActivity(StreamEvent{Delta: event.Delta}, event.Delta, "", "")); err != nil {
						result.err = err
						return result
					}
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning.delta", "response.reasoning_text.delta":
			// reasoning_summary_text.delta / reasoning.delta 是 OpenAI 官方事件；
			// reasoning_text.delta 是 DeepSeek Responses API 的逐 token CoT 事件。
			// 旧实现漏掉后者，DeepSeek 的全部思考 delta 被静默丢弃，只能从
			// response.completed 一次性回放——表现为思考内容瞬间全量输出。
			if event.Delta != "" {
				if decodeFailure == nil {
					streamedThinking.WriteString(event.Delta)
					if err := emit(withStreamActivity(StreamEvent{Thinking: event.Delta}, "", event.Delta, "")); err != nil {
						result.err = err
						return result
					}
					sawReasoningDelta = true
				}
			}
		case "response.reasoning_text.done":
			// DeepSeek 的全量 CoT 兜底：仅在一条流式 delta 都没收到时回放全文，
			// 避免与已播放的增量重复（received 时 completed 兜底会被抑制）。
			if decodeFailure == nil && !sawReasoningDelta && event.Text != "" {
				streamedThinking.WriteString(event.Text)
				if err := emit(StreamEvent{Thinking: event.Text}); err != nil {
					result.err = err
					return result
				}
				sawReasoningDelta = true
			}
		case "response.output_item.added":
			if len(event.Item) != 0 {
				call := &activeResponseToolCall{item: append(json.RawMessage(nil), event.Item...)}
				var view responsesOutputItemView
				if json.Unmarshal(event.Item, &view) == nil && view.Type == "function_call" {
					call.id, call.callID, call.name = view.ID, view.CallID, view.Name
					call.args.WriteString(view.Arguments)
					active[event.OutputIndex] = call
				}
			}
		case "response.function_call_arguments.delta":
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
			if event.Delta != "" {
				if err := emit(withStreamActivity(StreamEvent{}, "", "", event.Delta)); err != nil {
					result.err = err
					return result
				}
			}
			if call.item == nil {
				call.item = json.RawMessage(fmt.Sprintf(`{"type":"function_call","id":%q,"call_id":%q,"name":%q}`, call.id, call.callID, call.name))
			}
		case "response.output_item.done":
			if len(event.Item) != 0 {
				call := active[event.OutputIndex]
				if call == nil {
					call = &activeResponseToolCall{}
					active[event.OutputIndex] = call
				}
				call.item = append(json.RawMessage(nil), event.Item...)
			}
		case "response.completed":
			if event.Response != nil && (event.Response.Error != nil || (event.Response.Status != "" && event.Response.Status != "completed")) {
				result.err = responsesFailureFromEvent(event, resp.Header)
				return result
			}
			output := responsesRawOutputWithDeltas(active)
			if decodeFailure != nil {
				if event.Response == nil || event.Response.Output == nil {
					result.err = decodeFailure
					return result
				}
				output = event.Response.Output
			} else if event.Response != nil && len(event.Response.Output) != 0 {
				output = mergeResponsesOutputWithDeltas(event.Response.Output, active)
			}
			var usage *Usage
			if event.Response != nil {
				usage = event.Response.Usage
			}
			finalEvent, err := completedResponsesEvent(output, usage, streamedThinking.Len() > 0)
			if err != nil {
				result.err = err
				return result
			}
			if err := reconcileResponsesCompletion(&finalEvent, streamedText.String(), streamedThinking.String(), decodeFailure != nil); err != nil {
				result.err = err
				return result
			}
			// A Done consumer may immediately close its recorder. Persist the
			// validated stream terminal before delivery; delivery cancellation is
			// still reported independently by request_end.
			diagnostics.finish(ctx, nil, body.TimedOut())
			if err := emit(finalEvent); err != nil {
				result.err = err
				return result
			}
			return result
		case "response.failed", "response.incomplete", "error":
			result.err = responsesFailureFromEvent(event, resp.Header)
			return result
		}
	}
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	if decodeFailure != nil {
		result.err = decodeFailure
		return result
	}
	if body.TimedOut() {
		result.err = &responsesStreamFailure{EventType: "stream_idle", Message: fmt.Sprintf("模型流 %s 无任何数据，连接已中断（可能是 provider 网络抖动）", idleTimeout), RequestID: providerRequestID(resp.Header), Retryable: true}
		return result
	}
	if err := scanner.Err(); err != nil {
		if ctx != nil && ctx.Err() != nil {
			result.err = ctx.Err()
		} else {
			result.err = responsesReadFailure(err, resp.Header)
		}
		return result
	}
	result.err = &responsesStreamFailure{EventType: "unexpected_eof", Message: "Responses stream ended before response.completed", RequestID: providerRequestID(resp.Header), Retryable: true}
	return result
}
