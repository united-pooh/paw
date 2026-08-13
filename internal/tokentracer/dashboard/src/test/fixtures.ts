import type { TraceSnapshot, TraceEvent, TimelineRow, Usage } from '../trace/types';
import type { TimeRange } from '../trace/types';
import type { EventSourceLike, TraceIO } from '../stores/TraceStore';

const T0 = '2026-08-13T00:00:00.000Z';
const T1 = '2026-08-13T00:00:01.000Z';
const T2 = '2026-08-13T00:00:02.000Z';
const T3 = '2026-08-13T00:00:03.000Z';
const T4 = '2026-08-13T00:00:04.000Z';
const T5 = '2026-08-13T00:00:05.000Z';
const T6 = '2026-08-13T00:00:06.000Z';
const T8 = '2026-08-13T00:00:08.000Z';
const T10 = '2026-08-13T00:00:10.000Z';

export const fixtureRange: TimeRange = { startMS: 1_786_579_202_000, endMS: 1_786_579_205_000 };

function usage(input: number, cacheRead: number, cacheCreation: number, output: number): Usage {
  const totalContext = input + cacheRead + cacheCreation;
  return {
    input,
    cache_read: cacheRead,
    cache_creation: cacheCreation,
    output,
    total_context: totalContext,
    cache_hit_rate: totalContext > 0 ? Math.round((cacheRead / totalContext) * 1000) / 10 : 0,
  };
}

const plannerUsage = usage(1000, 2000, 500, 300);
const failedUsageA = usage(1000, 500, 1000, 500);
const failedUsageB = usage(1000, 500, 2000, 1000);
const outsideUsage = usage(100, 50, 0, 25);

const leafTotal = usage(3100, 3050, 3500, 1825);

export const fixtureRows: TimelineRow[] = [
  {
    id: 'stage:turn-1',
    kind: 'stage',
    stage_id: 'turn-1',
    stage_name: 'Turn 1',
    name: 'Turn 1',
    display_name: 'Turn 1',
    start_time: T0,
    end_time: T10,
    duration_ms: 10_000,
    status: 'failed',
    error: 'fixture failure: boom',
    usage: usage(0, 0, 0, 0),
    token_grand_total: 0,
    token_share: 0,
    calls: 3,
  },
  {
    id: 'planner',
    kind: 'agent',
    stage_id: 'turn-1',
    stage_name: 'Turn 1',
    agent_id: 'planner',
    parent_id: 'stage:turn-1',
    name: 'planner',
    display_name: 'planner',
    role: 'planner',
    provider: 'fixture-provider',
    model: 'gpt-4.1',
    session_id: 'session-1',
    invocation_index: 0,
    start_time: T0,
    end_time: T2,
    duration_ms: 2000,
    status: 'completed',
    usage: plannerUsage,
    token_grand_total: 3800,
    token_share: 33.1,
    calls: 1,
    markers: [
      {
        type: 'api_call',
        time: T1,
        label: 'api',
        detail: 'input=1000 cache=2000 output=300',
        status: 'usage',
        usage: plannerUsage,
      },
    ],
  },
  {
    id: 'failed-row',
    kind: 'agent',
    stage_id: 'turn-1',
    stage_name: 'Turn 1',
    agent_id: 'failed-row',
    parent_id: 'stage:turn-1',
    name: 'failed-row',
    display_name: 'failed-row',
    role: 'critic',
    provider: 'fixture-provider',
    model: 'gpt-4.1',
    session_id: 'session-2',
    invocation_index: 0,
    start_time: T1,
    end_time: T4,
    duration_ms: 3000,
    status: 'failed',
    error: 'boom',
    usage: usage(2000, 1000, 3000, 1500),
    token_grand_total: 7500,
    token_share: 65.4,
    calls: 2,
    critical: true,
    bottleneck: true,
    markers: [
      {
        type: 'api_call',
        time: T2,
        label: 'api',
        detail: 'input=1000 cache=500 output=500',
        status: 'usage',
        usage: failedUsageA,
      },
      {
        type: 'api_call',
        time: T3,
        label: 'api',
        detail: 'input=1000 cache=500 output=1000',
        status: 'usage',
        usage: failedUsageB,
      },
    ],
  },
  {
    id: 'outside-row',
    kind: 'agent',
    stage_id: 'turn-1',
    stage_name: 'Turn 1',
    agent_id: 'outside-row',
    parent_id: 'stage:turn-1',
    name: 'outside-row',
    display_name: 'outside-row',
    role: 'researcher',
    provider: 'fixture-provider',
    model: 'gpt-4.1-mini',
    session_id: 'session-3',
    invocation_index: 0,
    start_time: T8,
    end_time: T10,
    duration_ms: 2000,
    status: 'completed',
    usage: outsideUsage,
    token_grand_total: 175,
    token_share: 1.5,
    calls: 1,
    markers: [
      {
        type: 'api_call',
        time: T8,
        label: 'api',
        detail: 'input=100 cache=50 output=25',
        status: 'usage',
        usage: outsideUsage,
      },
    ],
  },
];

export const fixtureEvents: TraceEvent[] = [
  {
    seq: 1,
    type: 'api_call',
    timestamp: T1,
    data: {
      stage_id: 'turn-1',
      agent_id: 'planner',
      provider: 'fixture-provider',
      model: 'gpt-4.1',
      error: 'request failed: Authorization: Bearer sk-fixture-secret',
    },
  },
  { seq: 2, type: 'cleanup', timestamp: T5, data: { stage_id: 'turn-1' } },
  { seq: 3, type: 'cleanup', timestamp: T6, data: { stage_id: 'turn-1' } },
  { seq: 4, type: 'cleanup', timestamp: T8, data: { stage_id: 'turn-1' } },
];

export const fixtureSnapshot: TraceSnapshot = {
  pipeline: {
    id: 'run-20260813-000000',
    name: 'Token Tracer Fixture',
    start_time: T0,
    end_time: T10,
    total: leafTotal,
    calls: 3,
    status: 'failed',
  },
  run_id: 'run-20260813-000000',
  session_id: 'session-fixture',
  workspace: '/tmp/fixture',
  server_url: 'http://127.0.0.1:18999',
  timeline: {
    start_time: T0,
    end_time: T10,
    generated_at: T10,
    duration_ms: 10_000,
    max_concurrency: 2,
    overlap_ms: 1000,
    token_total: leafTotal,
    token_grand_total: 11_475,
    error: 'fixture failure: boom',
    critical_path: ['failed-row', 'stage:turn-1'],
    bottleneck_id: 'failed-row',
    rows: fixtureRows,
  },
  events: fixtureEvents,
};

export function makeLargeSnapshot(rowCount: number, eventCount: number): TraceSnapshot {
  const rows: TimelineRow[] = [
    {
      ...fixtureRows[0],
      id: 'stage:turn-1',
      usage: usage(0, 0, 0, 0),
      token_grand_total: 0,
      token_share: 0,
      calls: rowCount,
    },
  ];
  let leafInput = 0;
  let leafCacheRead = 0;
  let leafCacheCreation = 0;
  let leafOutput = 0;
  let grandTotal = 0;
  const baseMS = Date.parse(T0);
  for (let i = 0; i < rowCount; i++) {
    const rowUsage = usage(100 + (i % 7) * 10, 40 + (i % 5) * 10, 20 + (i % 3) * 10, 10 + (i % 9) * 5);
    const startMS = baseMS + (i % 10) * 1000;
    leafInput += rowUsage.input;
    leafCacheRead += rowUsage.cache_read;
    leafCacheCreation += rowUsage.cache_creation;
    leafOutput += rowUsage.output;
    const rowTotal = rowUsage.input + rowUsage.cache_read + rowUsage.cache_creation + rowUsage.output;
    grandTotal += rowTotal;
    rows.push({
      id: `agent:turn-1:agent-${i}:0`,
      kind: 'agent',
      stage_id: 'turn-1',
      stage_name: 'Turn 1',
      agent_id: `agent-${i}`,
      parent_id: 'stage:turn-1',
      name: `agent-${i}`,
      display_name: `agent-${i}`,
      start_time: new Date(startMS).toISOString(),
      end_time: new Date(startMS + 1000).toISOString(),
      duration_ms: 1000,
      status: i % 97 === 0 ? 'failed' : 'completed',
      error: i % 97 === 0 ? `fixture error ${i}` : undefined,
      usage: rowUsage,
      token_grand_total: rowTotal,
      token_share: 0,
      calls: 1,
      markers: [
        {
          type: 'api_call',
          time: new Date(startMS + 500).toISOString(),
          label: 'api',
          detail: 'fixture api call',
          status: 'usage',
          usage: rowUsage,
        },
      ],
    });
  }
  const events: TraceEvent[] = [];
  for (let i = 0; i < eventCount; i++) {
    events.push({
      seq: 5 + i,
      type: 'tool_event',
      timestamp: new Date(baseMS + (i % 10) * 1000).toISOString(),
      data: { stage_id: 'turn-1', agent_id: `agent-${i % 50}`, tool: `tool-${i % 8}` },
    });
  }
  const total = usage(leafInput, leafCacheRead, leafCacheCreation, leafOutput);
  return {
    pipeline: {
      id: 'run-large',
      name: 'Token Tracer Large Fixture',
      start_time: T0,
      end_time: T10,
      total,
      calls: rowCount,
      status: 'failed',
    },
    run_id: 'run-large',
    timeline: {
      start_time: T0,
      end_time: T10,
      generated_at: T10,
      duration_ms: 10_000,
      max_concurrency: 4,
      overlap_ms: 2000,
      token_total: total,
      token_grand_total: grandTotal,
      error: 'fixture error 0',
      rows,
    },
    events,
  };
}

export function fakeTraceIO(snapshot: TraceSnapshot): TraceIO & { flush: () => Promise<void> } {
  const fetchSnapshot = (_signal: AbortSignal): Promise<TraceSnapshot> =>
    new Promise((resolve) => {
      queueMicrotask(() => resolve(snapshot));
    });
  return {
    fetchSnapshot,
    createEventSource: () => new FakeEventSource() as unknown as EventSourceLike,
    flush: async () => {
      await Promise.resolve();
      await Promise.resolve();
    },
  };
}

class FakeEventSource {
  private listeners = new Map<string, EventListener[]>();
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;

  addEventListener(type: string, listener: EventListener): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  close(): void {
    this.closed = true;
  }
}
