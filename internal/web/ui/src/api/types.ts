export interface RecentWorkspace {
  id: string;
  path: string;
  name: string;
  last_opened_at: string;
}

export interface BootstrapResponse {
  schema_version: number;
  recent_workspaces: RecentWorkspace[];
  loaded_workspaces?: RecentWorkspace[];
  loaded_runtimes: number;
}

export interface WorkspaceResponse {
id: string;
path: string;
name: string;
loaded: boolean;
}

export interface PickWorkspaceResponse {
path?: string;
cancelled?: boolean;
}

export interface CompletionItem {
label: string;
detail?: string;
dir?: boolean;
}

export interface CompletionResponse {
items: CompletionItem[];
}

/** 输入框上方卡片堆选择器所需的模型目录与推理强度状态。 */
export interface ModelOption {
id: string;
name: string;
provider: string;
source: string;
reasoning_capable: boolean;
effort?: string;
}

export interface ModelOptionsResponse {
active_model_id: string;
models: ModelOption[];
effort_options: string[];
}

export interface SessionMutationResult {
  session_id: string;
  session_version: number;
}

export interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
}

export interface ToolResult {
  tool_use_id: string;
  content?: string;
  is_error?: boolean;
}

export interface ReasoningPart {
  text?: string;
  redacted?: boolean;
}

export interface AssistantPart {
  type: 'reasoning' | 'text' | 'tool_call';
  status?: string;
  text?: { text: string };
  reasoning?: ReasoningPart;
  tool_call?: ToolCall;
}

export interface MessagePart {
  role: string;
  content?: string;
  assistant_parts?: AssistantPart[] | null;
  tool_use?: ToolCall | null;
  tool_uses?: ToolCall[] | null;
  tool_result?: ToolResult | null;
  tool_results?: ToolResult[] | null;
}

export interface TurnProjection {
  turn_id: string;
  messages: MessagePart[];
  status?: string;
  error?: string;
  /** started_at 为回合开始时间（ISO 字符串）；token 字段为本轮增量用量。 */
  started_at?: string;
  response_at?: string;
  duration_ms?: number;
  input_tokens?: number;
  output_tokens?: number;
}

export interface StreamingPart {
  part_id: string;
  session_id: string;
  turn_id: string;
  kind: string;
  text: string;
  completed?: boolean;
}

export interface SessionSummary {
  session_id: string;
  created_at: string;
  last_used_at: string;
  title?: string;
  transcript_size: number;
}

export interface CommandReceipt {
  command_id: string;
  status: string;
  resource_id: string;
  session_version: number;
}

export interface SessionPage {
  items: SessionSummary[];
  next_cursor?: string;
}

export interface PendingInteraction {
  request_id: string;
  session_id: string;
  turn_id?: string;
  kind: 'question' | 'permission';
}

export interface SessionSnapshot {
  session_id: string;
  session_version: number;
  turns: TurnProjection[];
  earlier_cursor?: string;
  active_turn_id?: string;
  queue?: unknown[];
  pending?: PendingInteraction[];
  stream_id: string;
  event_sequence: number;
  parts?: StreamingPart[];
}

export interface AppEventBase<TType extends string, TPayload> {
  schema_version: number;
  stream_id: string;
  sequence: number;
  workspace_id: string;
  session_id?: string;
  turn_id?: string;
  type: TType;
  time: string;
  entity_version?: number;
  payload: TPayload;
}

export interface DeltaPayload { part_id: string; offset: number; text: string }
export interface PartStartedPayload { part_id: string; part_index: number; kind?: string; redacted?: boolean }
export interface PartCompletedPayload { part_id: string; final_length: number }
export interface ToolStartedPayload { tool_use_id: string; name: string; target?: string; args_summary?: string; started_at?: string }
export interface ToolCompletedPayload { tool_use_id: string; name: string; result_summary?: string; detail_id?: string; finished_at?: string; duration_ms?: number }
export interface ToolFailedPayload { tool_use_id: string; name: string; error_code?: string; message?: string; detail_id?: string; finished_at?: string }

/** 工具调用的前端聚合状态（来自 tool.started/completed/failed 事件流） */
export interface ToolCallState {
  tool_use_id: string;
  name: string;
  target?: string;
  args_summary?: string;
  result_summary?: string;
  error_code?: string;
  error_message?: string;
  detail_id?: string;
  duration_ms?: number;
  status: 'running' | 'completed' | 'failed';
}
export interface ResetPayload { reason: string; current_stream_id: string; latest_sequence: number }
export interface TurnPayload { turn_id: string; status?: string }
export interface QueueUpdatedPayload { items: unknown[]; session_version: number }
export interface QuestionOption { id: string; label: string; description?: string }
export interface QuestionRequestedPayload { request_id: string; prompt: string; mode: string; options: QuestionOption[]; created_at: string }
export interface QuestionResolvedPayload { request_id: string; answer?: { cancelled?: boolean; selected_options?: QuestionOption[] }; resolved_at: string }
export interface PermissionRequestedPayload { request_id: string; operation: string; canonical_target: string; created_at: string }
export interface PermissionResolvedPayload { request_id: string; decision: 'allow_once' | 'deny'; resolved_at: string }
export interface InteractionExpiredPayload { request_id: string; kind: string; reason: string; expired_at: string }

export type KnownAppEvent =
  | AppEventBase<'question.requested', QuestionRequestedPayload>
  | AppEventBase<'question.resolved', QuestionResolvedPayload>
  | AppEventBase<'permission.requested', PermissionRequestedPayload>
  | AppEventBase<'permission.resolved', PermissionResolvedPayload>
  | AppEventBase<'interaction.expired', InteractionExpiredPayload>
  | AppEventBase<'turn.started', TurnPayload>
  | AppEventBase<'turn.completed', TurnPayload>
  | AppEventBase<'turn.failed', TurnPayload>
  | AppEventBase<'turn.cancelled', TurnPayload>
  | AppEventBase<'queue.updated', QueueUpdatedPayload>
  | AppEventBase<'assistant.part.started', PartStartedPayload>
  | AppEventBase<'assistant.delta', DeltaPayload>
  | AppEventBase<'assistant.part.completed', PartCompletedPayload>
  | AppEventBase<'reasoning.started', PartStartedPayload>
  | AppEventBase<'reasoning.delta', DeltaPayload>
  | AppEventBase<'reasoning.completed', PartCompletedPayload>
  | AppEventBase<'tool.started', ToolStartedPayload>
  | AppEventBase<'tool.completed', ToolCompletedPayload>
  | AppEventBase<'tool.failed', ToolFailedPayload>
  | AppEventBase<'event.reset_required', ResetPayload>;

export type AppEvent = KnownAppEvent | AppEventBase<string, unknown>;
