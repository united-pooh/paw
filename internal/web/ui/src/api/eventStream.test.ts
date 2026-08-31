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
