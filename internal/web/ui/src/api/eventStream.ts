import type { AppEvent } from './types';
import type { WorkbenchStore } from '../app/store';

export interface EventSourceLike {
  addEventListener(type: string, listener: EventListener): void;
  close(): void;
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
}

export interface EventStreamOptions {
  workspaceID: string;
  streamID: string;
  sequence: number;
  store: WorkbenchStore;
  reloadSnapshot: () => Promise<void>;
  createSource?: (url: string) => EventSourceLike;
}

export class EventStream {
  private source: EventSourceLike | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private retry = 0;
  private disposed = false;

  constructor(private readonly options: EventStreamOptions) {}

  start(): void {
    this.connect();
  }

  dispose(): void {
    this.disposed = true;
    this.source?.close();
    this.source = null;
    if (this.retryTimer !== null) clearTimeout(this.retryTimer);
  }

  private connect(): void {
    if (this.disposed) return;
    const after = `${this.options.streamID}:${this.options.sequence}`;
    const url = `/api/workspaces/${encodeURIComponent(this.options.workspaceID)}/events?after=${encodeURIComponent(after)}`;
    const createSource = this.options.createSource ?? ((target) => new EventSource(target));
    const source = createSource(url);
    this.source = source;
    source.onopen = () => {
      this.retry = 0;
      this.options.store.dispatch({ type: 'connection.changed', connection: 'live' });
    };
    source.onerror = () => {
      source.close();
      this.options.store.dispatch({ type: 'connection.changed', connection: 'reconnecting' });
      this.scheduleReconnect();
    };
    for (const type of [
      'assistant.part.started', 'assistant.delta', 'assistant.part.completed',
      'reasoning.started', 'reasoning.delta', 'reasoning.completed', 'event.reset_required'
    ]) {
      source.addEventListener(type, ((event: MessageEvent<string>) => {
        const parsed = JSON.parse(event.data) as AppEvent;
        this.options.store.dispatch({ type: 'event.received', event: parsed });
        if (parsed.type === 'event.reset_required') void this.options.reloadSnapshot();
      }) as EventListener);
    }
  }

  private scheduleReconnect(): void {
    if (this.disposed || this.retryTimer !== null) return;
    const delay = Math.min(1000 * 2 ** this.retry, 15000);
    this.retry += 1;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.connect();
    }, delay);
  }
}
