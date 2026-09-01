import { EventStream, type EventSourceLike } from './eventStream';
import { WorkbenchStore } from '../app/store';

class Source implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  listeners = new Map<string, EventListener>();
  closed = false;
  addEventListener(type: string, listener: EventListener): void { this.listeners.set(type, listener); }
  close(): void { this.closed = true; }
  emit(type: string, value: unknown): void {
    this.listeners.get(type)?.({ data: JSON.stringify(value) } as MessageEvent<string>);
  }
}

it('reconnects from the latest reduced cursor', () => {
  vi.useFakeTimers();
  const store = new WorkbenchStore();
  const sources: Source[] = [];
  const urls: string[] = [];
  const stream = new EventStream({
    workspaceID: 'w1', streamID: 'stream', sequence: 8, store,
    reloadSnapshot: async () => undefined,
    createSource: (url) => { urls.push(url); const source = new Source(); sources.push(source); return source; }
  });
  stream.start();
  sources[0].emit('assistant.part.started', { schema_version: 1, stream_id: 'stream', sequence: 9, workspace_id: 'w1', session_id: 's1', turn_id: 't1', type: 'assistant.part.started', time: new Date().toISOString(), payload: { part_id: 'p1', part_index: 0, kind: 'assistant' } });
  sources[0].onerror?.(new Event('error'));
  vi.advanceTimersByTime(1000);
  expect(urls[1]).toContain('after=stream%3A9');
  stream.dispose();
  vi.useRealTimers();
});

it('connects with snapshot cursor and reloads on reset', async () => {
  const source = new Source();
  let capturedURL = '';
  let reloads = 0;
  const stream = new EventStream({
    workspaceID: 'w1', streamID: 'stream', sequence: 8, store: new WorkbenchStore(),
    reloadSnapshot: async () => { reloads += 1; },
    createSource: (url) => { capturedURL = url; return source; }
  });
  stream.start();
  expect(capturedURL).toContain('after=stream%3A8');
  source.emit('event.reset_required', { schema_version: 1, stream_id: 'stream', sequence: 9, workspace_id: 'w1', type: 'event.reset_required', time: new Date().toISOString(), payload: { reason: 'cursor_too_old' } });
  await Promise.resolve();
  expect(reloads).toBe(1);
  stream.dispose();
  expect(source.closed).toBe(true);
});
