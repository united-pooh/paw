import type { TraceSnapshot } from '../trace/types';

export type ConnectionState = 'loading' | 'live' | 'reconnecting' | 'error';

export interface TraceState {
  snapshot: TraceSnapshot | null;
  connection: ConnectionState;
  error: string | null;
}

export interface EventSourceLike {
  addEventListener(type: string, listener: EventListener): void;
  close(): void;
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
}

export interface TraceIO {
  fetchSnapshot(signal: AbortSignal): Promise<TraceSnapshot>;
  createEventSource(url: string): EventSourceLike;
}

const defaultIO: TraceIO = {
  async fetchSnapshot(signal: AbortSignal): Promise<TraceSnapshot> {
    const response = await fetch('/api/state', { cache: 'no-store', signal });
    if (!response.ok) {
      throw new Error(`snapshot request failed: HTTP ${response.status}`);
    }
    return (await response.json()) as TraceSnapshot;
  },
  createEventSource(url: string): EventSourceLike {
    return new EventSource(url);
  },
};

export class TraceStore {
  private readonly io: TraceIO;
  private readonly refreshDelayMS: number;
  private readonly listeners = new Set<() => void>();
  private state: TraceState = { snapshot: null, connection: 'loading', error: null };
  private readonly abort = new AbortController();
  private refreshTimer: ReturnType<typeof setTimeout> | null = null;
  private inFlight: Promise<void> | null = null;
  private queued = false;
  private source: EventSourceLike | null = null;
  private disposed = false;

  constructor(io: TraceIO = defaultIO, refreshDelayMS = 150) {
    this.io = io;
    this.refreshDelayMS = refreshDelayMS;
  }

  async start(): Promise<void> {
    if (this.disposed) {
      return;
    }
    await this.requestSnapshot();
    if (this.disposed) {
      return;
    }
    this.connectEvents();
  }

  retry(): Promise<void> {
    return this.requestSnapshot();
  }

  dispose(): void {
    this.disposed = true;
    this.abort.abort();
    if (this.refreshTimer !== null) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    this.source?.close();
    this.source = null;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  getSnapshot(): TraceState {
    return this.state;
  }

  private emit(): void {
    for (const listener of this.listeners) {
      listener();
    }
  }

  private setState(patch: Partial<TraceState>): void {
    this.state = { ...this.state, ...patch };
    this.emit();
  }

  private connectEvents(): void {
    if (this.disposed) {
      return;
    }
    const source = this.io.createEventSource('/events');
    this.source = source;
    source.addEventListener('token_tracer', () => {
      this.scheduleRefresh();
    });
    source.onopen = () => {
      if (this.disposed) {
        return;
      }
      void this.requestSnapshot().then(() => {
        if (!this.disposed) {
          this.setState({ connection: 'live' });
        }
      });
    };
    source.onerror = () => {
      if (this.disposed) {
        return;
      }
      this.setState({ connection: 'reconnecting' });
    };
  }

  private scheduleRefresh(): void {
    if (this.disposed) {
      return;
    }
    if (this.refreshTimer !== null) {
      return;
    }
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      void this.requestSnapshot();
    }, this.refreshDelayMS);
  }

  private async requestSnapshot(): Promise<void> {
    if (this.disposed) {
      return;
    }
    if (this.inFlight !== null) {
      this.queued = true;
      return this.inFlight;
    }
    const promise = this.fetchSnapshot();
    this.inFlight = promise;
    try {
      await promise;
    } finally {
      this.inFlight = null;
      if (this.queued && !this.disposed) {
        this.queued = false;
        this.scheduleRefresh();
      }
    }
  }

  private async fetchSnapshot(): Promise<void> {
    if (this.disposed) {
      return;
    }
    this.setState({ error: null });
    try {
      const snapshot = await this.io.fetchSnapshot(this.abort.signal);
      if (this.disposed) {
        return;
      }
      this.setState({ snapshot, connection: 'live' });
    } catch (error) {
      if (this.disposed || (error instanceof DOMException && error.name === 'AbortError')) {
        return;
      }
      this.setState({
        connection: this.state.snapshot === null ? 'error' : 'reconnecting',
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }
}
