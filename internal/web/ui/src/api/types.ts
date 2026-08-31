export interface RecentWorkspace {
  id: string;
  path: string;
  name: string;
  last_opened_at: string;
}

export interface BootstrapResponse {
  schema_version: number;
  recent_workspaces: RecentWorkspace[];
  loaded_runtimes: number;
}

export interface MessagePart {
  role: string;
  content?: string;
}

export interface TurnProjection {
  turn_id: string;
  messages: MessagePart[];
  status?: string;
  error?: string;
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

export interface SessionPage {
  items: SessionSummary[];
  next_cursor?: string;
}

export interface SessionSnapshot {
  session_id: string;
  session_version: number;
  turns: TurnProjection[];
  earlier_cursor?: string;
  active_turn_id?: string;
  queue?: unknown[];
  pending?: unknown[];
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
export interface ResetPayload { reason: string; current_stream_id: string; latest_sequence: number }

export type KnownAppEvent =
  | AppEventBase<'assistant.part.started', PartStartedPayload>
  | AppEventBase<'assistant.delta', DeltaPayload>
  | AppEventBase<'assistant.part.completed', PartCompletedPayload>
  | AppEventBase<'reasoning.started', PartStartedPayload>
  | AppEventBase<'reasoning.delta', DeltaPayload>
  | AppEventBase<'reasoning.completed', PartCompletedPayload>
  | AppEventBase<'event.reset_required', ResetPayload>;

export type AppEvent = KnownAppEvent | AppEventBase<string, unknown>;
