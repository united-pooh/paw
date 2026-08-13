import { TraceStore } from './TraceStore';
import type { EventSourceLike, TraceIO } from './TraceStore';
import { SelectionStore } from './SelectionStore';
import { FilterStore } from './FilterStore';
import { fixtureSnapshot } from '../test/fixtures';
import type { TraceSnapshot } from '../trace/types';
import { defaultFilters } from '../trace/projections';

function createFakeTraceIO(snapshot: TraceSnapshot) {
  let concurrent = 0;
  let maxConcurrent = 0;
  const source = new FakeTraceSource();
  const fetchSnapshot = vi.fn((_signal: AbortSignal) => {
    concurrent++;
    maxConcurrent = Math.max(maxConcurrent, concurrent);
    return new Promise<TraceSnapshot>((resolve) => {
      queueMicrotask(() => {
        concurrent--;
        resolve(snapshot);
      });
    });
  });
  const io: TraceIO & {
    source: FakeTraceSource;
    fetchSnapshot: ReturnType<typeof vi.fn>;
    maxConcurrentFetches: () => number;
    flushFetch: () => Promise<void>;
  } = {
    source,
    fetchSnapshot,
    maxConcurrentFetches: () => maxConcurrent,
    createEventSource: () => source,
    flushFetch: async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    },
  };
  return io;
}

class FakeTraceSource implements EventSourceLike {
  private listeners = new Map<string, EventListener[]>();
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  addEventListener(type: string, listener: EventListener): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  emit(type: string, data: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent(type, { data }));
    }
  }

  fail(): void {
    this.onerror?.(new Event('error'));
  }

  open(): void {
    this.onopen?.(new Event('open'));
  }

  close(): void {
    this.listeners.clear();
  }
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

test('coalesces event bursts into one in-flight snapshot request', async () => {
  const io = createFakeTraceIO(fixtureSnapshot);
  const store = new TraceStore(io, 150);
  await store.start();
  io.source.emit('token_tracer', '{}');
  io.source.emit('token_tracer', '{}');
  vi.advanceTimersByTime(150);
  expect(io.fetchSnapshot).toHaveBeenCalledTimes(2);
  expect(io.maxConcurrentFetches()).toBe(1);
  store.dispose();
});

test('keeps the last snapshot while EventSource reconnects', async () => {
  const io = createFakeTraceIO(fixtureSnapshot);
  const store = new TraceStore(io, 150);
  await store.start();
  io.source.fail();
  expect(store.getSnapshot()).toMatchObject({ connection: 'reconnecting', snapshot: fixtureSnapshot });
  io.source.open();
  await io.flushFetch();
  expect(store.getSnapshot().connection).toBe('live');
  store.dispose();
});

test('stops mutating state after dispose', async () => {
  const io = createFakeTraceIO(fixtureSnapshot);
  const store = new TraceStore(io, 150);
  await store.start();
  store.dispose();
  const before = store.getSnapshot();
  io.source.emit('token_tracer', '{}');
  io.source.open();
  await vi.runAllTimersAsync();
  expect(store.getSnapshot()).toBe(before);
});

test('selection never mutates filters and removes missing row ids', () => {
  const selection = new SelectionStore();
  const filters = new FilterStore();
  selection.selectRow('failed-row', 'calls');
  selection.selectRange({ startMS: 10, endMS: 20 }, 'heatmap');
  expect(filters.getSnapshot()).toEqual(defaultFilters);
  selection.reconcile(new Set(['other-row']), new Set());
  expect(selection.getSnapshot().selectedRowID).toBeNull();
});

test('filters stay independent and reset cleanly', () => {
  const filters = new FilterStore();
  filters.setScope('agent');
  filters.setModel('gpt-4.1');
  filters.setErrorsOnly(true);
  expect(filters.getSnapshot()).toEqual({ scope: 'agent', model: 'gpt-4.1', errorsOnly: true });
  filters.reset();
  expect(filters.getSnapshot()).toEqual(defaultFilters);
});
