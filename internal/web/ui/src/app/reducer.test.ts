import { initialState, reducer } from './reducer';
import type { AppEvent, SessionSnapshot } from '../api/types';

const snapshot: SessionSnapshot = {
  session_id: 's1', session_version: 4, turns: [], stream_id: 'stream', event_sequence: 2,
  parts: [{ part_id: 'p1', session_id: 's1', turn_id: 't1', kind: 'assistant', text: 'hi' }]
};

function event(sequence: number, type: string, payload: unknown, streamID = 'stream'): AppEvent {
  return { schema_version: 1, stream_id: streamID, sequence, workspace_id: 'w1', session_id: 's1', turn_id: 't1', type, time: new Date().toISOString(), payload };
}

it('initializes from snapshot and ignores duplicate events', () => {
  const state = reducer(initialState, { type: 'snapshot.loaded', snapshot });
  const duplicate = reducer(state, { type: 'event.received', event: event(2, 'assistant.delta', { part_id: 'p1', offset: 2, text: '!' }) });
  expect(duplicate).toBe(state);
});

it('requests reset for stream and sequence gaps', () => {
  const state = reducer(initialState, { type: 'snapshot.loaded', snapshot });
  expect(reducer(state, { type: 'event.received', event: event(3, 'assistant.delta', { part_id: 'p1', offset: 2, text: '!' }, 'other') }).resetReason).toBe('stream_mismatch');
  expect(reducer(state, { type: 'event.received', event: event(4, 'assistant.delta', { part_id: 'p1', offset: 2, text: '!' }) }).resetReason).toBe('sequence_gap');
});

it('handles UTF-8 offsets, duplicates, gaps and leaves session version unchanged', () => {
  const utf8Snapshot = { ...snapshot, event_sequence: 0, parts: [{ ...snapshot.parts![0], text: '你' }] };
  let state = reducer(initialState, { type: 'snapshot.loaded', snapshot: utf8Snapshot });
  state = reducer(state, { type: 'event.received', event: event(1, 'assistant.delta', { part_id: 'p1', offset: 3, text: '好' }) });
  expect(state.parts.p1.text).toBe('你好');
  expect(state.snapshot?.session_version).toBe(4);
  const duplicate = reducer(state, { type: 'event.received', event: event(2, 'assistant.delta', { part_id: 'p1', offset: 3, text: '好' }) });
  expect(duplicate.parts.p1.text).toBe('你好');
  expect(reducer(duplicate, { type: 'event.received', event: event(3, 'assistant.delta', { part_id: 'p1', offset: 99, text: 'x' }) }).resetReason).toBe('part_offset_gap');
});

it('workspace.switched clears transient state while keeping connection', () => {
  let state = reducer(initialState, { type: 'snapshot.loaded', snapshot });
  state = reducer(state, { type: 'event.received', event: event(3, 'question.requested', { request_id: 'q1', prompt: 'Pick', mode: 'single', options: [], created_at: new Date().toISOString() }) });
  expect(Object.keys(state.interactions)).toHaveLength(1);
  state = reducer(state, { type: 'workspace.switched' });
  expect(state.snapshot).toBeNull();
  expect(state.parts).toEqual({});
  expect(state.interactions).toEqual({});
  expect(state.sequence).toBe(0);
  expect(state.connection).toBe('live');
});

it('updates active turn and queue state from command lifecycle events', () => {
  let state = reducer(initialState, { type: 'snapshot.loaded', snapshot });
  state = reducer(state, { type: 'event.received', event: { ...event(3, 'turn.started', { turn_id: 't2' }), turn_id: 't2', entity_version: 5 } });
  expect(state.snapshot?.active_turn_id).toBe('t2');
  expect(state.snapshot?.session_version).toBe(5);
  state = reducer(state, { type: 'event.received', event: { ...event(4, 'queue.updated', { items: [{ content: 'later' }], session_version: 6 }), turn_id: 't2', entity_version: 6 } });
  expect(state.snapshot?.queue).toHaveLength(1);
  expect(state.snapshot?.session_version).toBe(6);
  state = reducer(state, { type: 'event.received', event: { ...event(5, 'turn.cancelled', { turn_id: 't2' }), turn_id: 't2', entity_version: 7 } });
  expect(state.snapshot?.active_turn_id).toBeUndefined();
  expect(state.snapshot?.session_version).toBe(7);
});

it('ignores unknown same-schema events and fails unknown schema versions', () => {
  const state = reducer(initialState, { type: 'snapshot.loaded', snapshot });
  const unknown = reducer(state, { type: 'event.received', event: event(3, 'future.event', {}) });
  expect(unknown.sequence).toBe(3);
  const fatal = reducer(unknown, { type: 'event.received', event: { ...event(4, 'assistant.delta', {}), schema_version: 2 } });
  expect(fatal.connection).toBe('fatal');
});
