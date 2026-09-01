import type { AppEvent, DeltaPayload, SessionSnapshot, StreamingPart } from '../api/types';

export type ConnectionState = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'reset' | 'fatal';

export interface WorkbenchState {
  snapshot: SessionSnapshot | null;
  streamID: string;
  sequence: number;
  parts: Record<string, StreamingPart>;
  connection: ConnectionState;
  resetReason?: string;
  diagnostic?: string;
}

export type StoreAction =
  | { type: 'snapshot.loaded'; snapshot: SessionSnapshot }
  | { type: 'event.received'; event: AppEvent }
  | { type: 'connection.changed'; connection: ConnectionState };

export const initialState: WorkbenchState = {
  snapshot: null,
  streamID: '',
  sequence: 0,
  parts: {},
  connection: 'idle'
};

export function reducer(state: WorkbenchState, action: StoreAction): WorkbenchState {
  if (action.type === 'connection.changed') {
    return { ...state, connection: action.connection };
  }
  if (action.type === 'snapshot.loaded') {
    const parts = Object.fromEntries((action.snapshot.parts ?? []).map((part) => [part.part_id, part]));
    return {
      snapshot: action.snapshot,
      streamID: action.snapshot.stream_id,
      sequence: action.snapshot.event_sequence,
      parts,
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
      const turns = snapshot.turns.some((turn) => turn.turn_id === turnID)
        ? snapshot.turns.map((turn) => turn.turn_id === turnID ? { ...turn, status: 'running' } : turn)
        : [...snapshot.turns, { turn_id: turnID, messages: [], status: 'running' }];
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
  return { ...state, sequence: event.sequence, streamID: event.stream_id };
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
