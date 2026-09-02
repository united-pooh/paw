import type { AppEvent, DeltaPayload, SessionSnapshot, StreamingPart, ToolCallState, ToolCompletedPayload, ToolFailedPayload, ToolStartedPayload } from '../api/types';

export type ConnectionState = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'reset' | 'fatal';

export interface WorkbenchState {
  snapshot: SessionSnapshot | null;
  streamID: string;
  sequence: number;
  parts: Record<string, StreamingPart>;
  tools: Record<string, ToolCallState>;
  connection: ConnectionState;
  interactions: {
    [requestID: string]:
      | { kind: 'question'; question: { prompt: string; mode: string; options: { id: string; label: string; description?: string }[] } }
      | { kind: 'permission'; permission: { operation: string; canonical_target: string } };
  };
  resetReason?: string;
  diagnostic?: string;
}

export type StoreAction =
  | { type: 'snapshot.loaded'; snapshot: SessionSnapshot }
  | { type: 'event.received'; event: AppEvent }
  | { type: 'connection.changed'; connection: ConnectionState }
  | { type: 'workspace.switched' };

export const initialState: WorkbenchState = {
  snapshot: null,
  streamID: '',
  sequence: 0,
  parts: {},
  tools: {},
  connection: 'idle',
  interactions: {}
};

export function reducer(state: WorkbenchState, action: StoreAction): WorkbenchState {
  if (action.type === 'workspace.switched') {
    return { ...initialState, connection: state.connection === 'fatal' ? 'idle' : state.connection };
  }
  if (action.type === 'connection.changed') {
    return { ...state, connection: action.connection };
  }
  if (action.type === 'snapshot.loaded') {
    // 同一会话下，落伍于已应用事件水位的快照是并发刷新的竞态产物；
    // 应用它会把 SSE 已推进的状态回滚，必须丢弃。
    if (
      state.snapshot &&
      state.snapshot.session_id === action.snapshot.session_id &&
      action.snapshot.event_sequence < state.sequence
    ) {
      return state;
    }
    const parts = Object.fromEntries((action.snapshot.parts ?? []).map((part) => [part.part_id, part]));
    const interactions: WorkbenchState['interactions'] = {};
    for (const pending of action.snapshot.pending ?? []) {
      interactions[pending.request_id] = pending.kind === 'question'
        ? { kind: 'question', question: { prompt: '', mode: 'single', options: [] } }
        : { kind: 'permission', permission: { operation: '', canonical_target: '' } };
    }
    return {
      snapshot: action.snapshot,
      streamID: action.snapshot.stream_id,
      sequence: action.snapshot.event_sequence,
      parts,
      // 工具调用属于实时态：同一会话保留，切换会话时随快进快照一并重置。
      tools: state.snapshot?.session_id === action.snapshot.session_id ? state.tools : {},
      interactions,
      connection: 'live'
    };
  }
  return reduceEvent(state, action.event);
}

function reduceEvent(state: WorkbenchState, event: AppEvent): WorkbenchState {
  if (event.schema_version !== 1) {
    return { ...state, connection: 'fatal', diagnostic: `Unsupported schema ${event.schema_version}` };
  }
  if (state.streamID && event.stream_id !== state.streamID) {
    return { ...state, connection: 'reset', resetReason: 'stream_mismatch' };
  }
  if (event.sequence <= state.sequence) {
    return state;
  }
  if (state.sequence > 0 && event.sequence !== state.sequence + 1) {
    return { ...state, connection: 'reset', resetReason: 'sequence_gap' };
  }
  if (event.type === 'event.reset_required') {
    const payload = event.payload as { reason?: string };
    return { ...state, connection: 'reset', resetReason: payload.reason ?? 'reset_required', sequence: event.sequence };
  }
  if (event.type === 'turn.started') {
    return commitSnapshotEvent(state, event, (snapshot) => {
      const turnID = event.turn_id ?? (event.payload as { turn_id?: string }).turn_id ?? '';
      // 乐观写入开始时间：turn 完成前页脚即可显示时间与计时，
      // 快照拉取后以服务端持久化的精确值覆盖。
      const turns = snapshot.turns.some((turn) => turn.turn_id === turnID)
        ? snapshot.turns.map((turn) => turn.turn_id === turnID ? { ...turn, status: 'running', started_at: turn.started_at ?? event.time } : turn)
        : [...snapshot.turns, { turn_id: turnID, messages: [], status: 'running', started_at: event.time }];
      return { ...snapshot, turns, active_turn_id: turnID, session_version: event.entity_version ?? snapshot.session_version };
    });
  }
  if (event.type === 'turn.completed' || event.type === 'turn.failed' || event.type === 'turn.cancelled') {
    return commitSnapshotEvent(state, event, (snapshot) => {
      const turnID = event.turn_id ?? (event.payload as { turn_id?: string }).turn_id ?? '';
      if (snapshot.active_turn_id !== turnID) return snapshot;
      const status = event.type === 'turn.completed' ? 'completed' : (event.type === 'turn.cancelled' ? 'cancelled' : 'failed');
      const turns = snapshot.turns.map((turn) => turn.turn_id === turnID ? { ...turn, status } : turn);
      const next = { ...snapshot, turns, session_version: event.entity_version ?? snapshot.session_version };
      delete next.active_turn_id;
      return next;
    });
  }
  if (event.type === 'queue.updated') {
    const payload = event.payload as { items?: unknown[]; session_version?: number };
    return commitSnapshotEvent(state, event, (snapshot) => ({
      ...snapshot,
      queue: payload.items ?? [],
      session_version: payload.session_version ?? event.entity_version ?? snapshot.session_version
    }));
  }
  if (event.type === 'assistant.part.started' || event.type === 'reasoning.started') {
    const payload = event.payload as { part_id: string; kind?: string };
    const part: StreamingPart = {
      part_id: payload.part_id,
      session_id: event.session_id ?? '',
      turn_id: event.turn_id ?? '',
      kind: payload.kind ?? (event.type.startsWith('reasoning') ? 'reasoning' : 'assistant'),
      text: ''
    };
    return commitEvent(state, event, { ...state.parts, [part.part_id]: part });
  }
  if (event.type === 'assistant.delta' || event.type === 'reasoning.delta') {
    return reduceDelta(state, event, event.payload as DeltaPayload);
  }
  if (event.type === 'assistant.part.completed' || event.type === 'reasoning.completed') {
    const payload = event.payload as { part_id: string };
    const current = state.parts[payload.part_id];
    if (!current) {
      return { ...state, connection: 'reset', resetReason: 'part_missing' };
    }
    return commitEvent(state, event, { ...state.parts, [payload.part_id]: { ...current, completed: true } });
  }
  if (event.type === 'tool.started') {
    const payload = event.payload as ToolStartedPayload;
    if (!payload.tool_use_id) return { ...state, sequence: event.sequence, streamID: event.stream_id };
    const existing = state.tools[payload.tool_use_id];
    const tool: ToolCallState = {
      tool_use_id: payload.tool_use_id,
      name: payload.name ?? existing?.name ?? 'tool',
      target: payload.target ?? existing?.target,
      args_summary: payload.args_summary ?? existing?.args_summary,
      result_summary: existing?.result_summary,
      detail_id: existing?.detail_id,
      status: 'running'
    };
    return { ...state, tools: { ...state.tools, [tool.tool_use_id]: tool }, sequence: event.sequence, streamID: event.stream_id };
  }
  if (event.type === 'tool.completed' || event.type === 'tool.failed') {
    const payload = event.payload as (ToolCompletedPayload & ToolFailedPayload);
    if (!payload.tool_use_id) return { ...state, sequence: event.sequence, streamID: event.stream_id };
    const existing = state.tools[payload.tool_use_id];
    const failed = event.type === 'tool.failed';
    const tool: ToolCallState = {
      tool_use_id: payload.tool_use_id,
      name: payload.name ?? existing?.name ?? 'tool',
      target: existing?.target,
      args_summary: existing?.args_summary,
      result_summary: failed ? (payload.message ?? existing?.result_summary) : (payload.result_summary ?? existing?.result_summary),
      error_code: failed ? payload.error_code : undefined,
      error_message: failed ? payload.message : undefined,
      detail_id: payload.detail_id ?? existing?.detail_id,
      duration_ms: payload.duration_ms ?? existing?.duration_ms,
      status: failed ? 'failed' : 'completed'
    };
    return { ...state, tools: { ...state.tools, [tool.tool_use_id]: tool }, sequence: event.sequence, streamID: event.stream_id };
  }
  if (event.type === 'question.requested') {
    const payload = event.payload as { request_id: string; prompt: string; mode: string; options: { id: string; label: string; description?: string }[] };
    return { ...state, interactions: { ...state.interactions, [payload.request_id]: { kind: 'question', question: { prompt: payload.prompt, mode: payload.mode, options: payload.options } } }, sequence: event.sequence, streamID: event.stream_id };
  }
  if (event.type === 'permission.requested') {
    const payload = event.payload as { request_id: string; operation: string; canonical_target: string };
    return { ...state, interactions: { ...state.interactions, [payload.request_id]: { kind: 'permission', permission: { operation: payload.operation, canonical_target: payload.canonical_target } } }, sequence: event.sequence, streamID: event.stream_id };
  }
  if (event.type === 'question.resolved' || event.type === 'permission.resolved') {
    const payload = event.payload as { request_id: string };
    return removeInteraction(state, event, payload.request_id);
  }
  if (event.type === 'interaction.expired') {
    const payload = event.payload as { request_id: string };
    return removeInteraction(state, event, payload.request_id);
  }
  return { ...state, sequence: event.sequence, streamID: event.stream_id };
}

function removeInteraction(state: WorkbenchState, event: AppEvent, requestID: string): WorkbenchState {
  if (!(requestID in state.interactions)) {
    return { ...state, sequence: event.sequence, streamID: event.stream_id };
  }
  const interactions = { ...state.interactions };
  delete interactions[requestID];
  return { ...state, interactions, sequence: event.sequence, streamID: event.stream_id };
}

function reduceDelta(state: WorkbenchState, event: AppEvent, payload: DeltaPayload): WorkbenchState {
  const current = state.parts[payload.part_id];
  if (!current) {
    return { ...state, connection: 'reset', resetReason: 'part_missing' };
  }
  const byteLength = new TextEncoder().encode(current.text).length;
  if (payload.offset < byteLength) {
    return { ...state, sequence: event.sequence, streamID: event.stream_id };
  }
  if (payload.offset > byteLength) {
    return { ...state, connection: 'reset', resetReason: 'part_offset_gap' };
  }
  return commitEvent(state, event, {
    ...state.parts,
    [payload.part_id]: { ...current, text: current.text + payload.text }
  });
}

function commitSnapshotEvent(state: WorkbenchState, event: AppEvent, update: (snapshot: SessionSnapshot) => SessionSnapshot): WorkbenchState {
  if (!state.snapshot || event.session_id !== state.snapshot.session_id) {
    return { ...state, sequence: event.sequence, streamID: event.stream_id };
  }
  return { ...state, snapshot: update(state.snapshot), sequence: event.sequence, streamID: event.stream_id };
}

function commitEvent(state: WorkbenchState, event: AppEvent, parts: Record<string, StreamingPart>): WorkbenchState {
  return { ...state, parts, sequence: event.sequence, streamID: event.stream_id };
}
