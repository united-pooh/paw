# Token Tracer High-Density Docking Workspace Implementation Plan

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the sparse embedded Token Tracer waterfall with a React/TypeScript IDE-style workspace containing a virtualized Calls Table, Canvas Token Heatmap, SVG Folded Flame, Inspector, and Events panels backed by the existing Go snapshot and SSE APIs.

**Architecture:** Keep `Tracer`, `Timeline`, `/api/state`, `/events`, and `/healthz` unchanged. Build a focused Vite application under `internal/tokentracer/dashboard`, commit its deterministic `dist/`, and serve it from Go through `embed.FS`; all panels read one `TraceStore`, one `SelectionStore`, and one `FilterStore`, while Dockview layout alone is versioned in `localStorage`.

**Tech Stack:** Go 1.25, React 19, TypeScript, Vite, Dockview React 7, Vitest, Testing Library, Playwright, Canvas, SVG, EventSource, `localStorage`, `go:embed`.

---

## Scope and working-tree guardrails

- Approved design: `docs/superpowers/specs/2026-08-13-token-tracer-docking-workspace-design.md`.
- Work only in `internal/tokentracer`, `.gitignore`, `.agent/visual`, and the project `memory/` files named below.
- The checkout already contains unrelated user edits under `internal/config`, `internal/loop`, `internal/theme`, and `internal/ui/bubble`. Do not modify, stage, format, or discard them.
- Do not change the JSON shape of `Usage`, `Snapshot`, `Timeline`, `TimelineRow`, `TimelineMarker`, or `Event`.
- Do not add popout windows, multiple instances of one panel type, named layout templates, or persisted filters/selections.
- Do not delete `internal/tokentracer/dashboard.go` or `legacyDashboardHTML` until the embedded-resource server tests pass.

## Locked file structure

```text
internal/tokentracer/
├── dashboard/
│   ├── e2e/token-tracer.spec.ts
│   ├── src/
│   │   ├── app/{App,DockingWorkspace,NarrowWorkspace,PanelHeaderActions,TopBar}.tsx
│   │   ├── components/{EmptyState,PanelErrorBoundary,VirtualList}.tsx
│   │   ├── panels/{CallsTable,Events,FoldedFlame,Inspector,TokenHeatmap}.tsx
│   │   ├── stores/{FilterStore,LayoutStore,SelectionStore,TraceStore}.ts
│   │   ├── stores/StoreProvider.tsx
│   │   ├── styles/{app,dockview,panels,tokens}.css
│   │   ├── test/{fixtures,setup}.ts
│   │   ├── trace/{format,projections,types}.ts
│   │   └── main.tsx
│   ├── dist/
│   ├── index.html
│   ├── package-lock.json
│   ├── package.json
│   ├── playwright.config.ts
│   ├── tsconfig.app.json
│   ├── tsconfig.json
│   └── vite.config.ts
├── testdata/dashboardfixture/main.go
├── dashboard_embed.go
├── dashboard_embed_test.go
├── server.go
└── tracer_test.go
```

`*.test.ts(x)` files live beside the implementation they test. Keep each production file below roughly 250 lines; split selectors or drawing helpers further when that limit would be exceeded.

### Task 1: Bootstrap the reproducible frontend build

**Files:**
- Create: `internal/tokentracer/dashboard/package.json`
- Create: `internal/tokentracer/dashboard/package-lock.json`
- Create: `internal/tokentracer/dashboard/index.html`
- Create: `internal/tokentracer/dashboard/tsconfig.json`
- Create: `internal/tokentracer/dashboard/tsconfig.app.json`
- Create: `internal/tokentracer/dashboard/vite.config.ts`
- Create: `internal/tokentracer/dashboard/src/main.tsx`
- Create: `internal/tokentracer/dashboard/src/app/App.tsx`
- Create: `internal/tokentracer/dashboard/src/app/App.test.tsx`
- Create: `internal/tokentracer/dashboard/src/test/setup.ts`
- Modify: `.gitignore:44-47`

- [ ] **Step 1: Exempt only the dashboard entry and generated bundle from the existing broad ignore rule**

```gitignore
index.html
!internal/tokentracer/dashboard/index.html
!internal/tokentracer/dashboard/dist/index.html
qa-report.json
```

Run: `git check-ignore -v internal/tokentracer/dashboard/index.html || true`

Expected: no output after the exception is added.

- [ ] **Step 2: Create the package scripts and TypeScript/Vite configuration**

Create `package.json` with this initial content; the install commands in the next step add exact dependency fields and generate `package-lock.json`:

```json
{
  "name": "paw-token-tracer-dashboard",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "typecheck": "tsc -b --pretty false",
    "test": "vitest run",
    "test:watch": "vitest",
    "build": "npm run typecheck && vite build",
    "e2e": "playwright test"
  }
}
```

Create `tsconfig.json`:

```json
{
  "files": [],
  "references": [{ "path": "./tsconfig.app.json" }]
}
```

Create `tsconfig.app.json`:

```json
{
  "compilerOptions": {
    "composite": true,
    "target": "ES2022",
    "useDefineForClassFields": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "allowJs": false,
    "skipLibCheck": true,
    "esModuleInterop": true,
    "allowSyntheticDefaultImports": true,
    "strict": true,
    "forceConsistentCasingInFileNames": true,
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "vite.config.ts", "playwright.config.ts", "e2e"]
}
```

Create `vite.config.ts`:

```ts
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  base: '/',
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8999',
      '/events': 'http://127.0.0.1:8999',
      '/healthz': 'http://127.0.0.1:8999'
    }
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: true
  }
});
```

- [ ] **Step 3: Install and lock the frontend toolchain**

Run:

```bash
npm --prefix internal/tokentracer/dashboard install --save-exact react@19.2.8 react-dom@19.2.8 dockview-react@7.0.4
npm --prefix internal/tokentracer/dashboard install --save-dev --save-exact vite@8.1.5 @vitejs/plugin-react@6.0.4 typescript vitest@4.1.0 jsdom @types/node @types/react @types/react-dom @testing-library/react@16.3.2 @testing-library/dom @testing-library/jest-dom @testing-library/user-event @playwright/test
```

Expected: `package-lock.json` is generated; `npm ls --depth=0` exits 0. Keep the exact versions written by npm and do not hand-edit the lockfile.

- [ ] **Step 4: Write the failing application smoke test**

```tsx
import { render, screen } from '@testing-library/react';
import { App } from './App';

test('renders the fixed Token Tracer shell', () => {
  render(<App />);
  expect(screen.getByRole('banner')).toHaveTextContent('Token Tracer');
  expect(screen.getByRole('main')).toBeInTheDocument();
});
```

Run: `npm --prefix internal/tokentracer/dashboard test -- src/app/App.test.tsx`

Expected: FAIL because `App` and the testing setup are not implemented.

- [ ] **Step 5: Add the minimal accessible root**

Create `src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest';

class ResizeObserverStub implements ResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

globalThis.ResizeObserver = ResizeObserverStub;
```

Create `src/app/App.tsx`:

```tsx
export function App() {
  return (
    <div className="app-shell">
      <header role="banner"><h1>Token Tracer</h1></header>
      <main aria-label="Token Tracer workspace" />
    </div>
  );
}
```

Create `src/main.tsx` and `index.html`:

```tsx
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';

createRoot(document.getElementById('root')!).render(
  <StrictMode><App /></StrictMode>
);
```

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="color-scheme" content="dark" />
    <title>Token Tracer</title>
  </head>
  <body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body>
</html>
```

Run:

```bash
npm --prefix internal/tokentracer/dashboard test -- src/app/App.test.tsx
npm --prefix internal/tokentracer/dashboard run build
```

Expected: the smoke test passes and `dist/index.html` plus hashed assets are created.

### Task 2: Mirror the Go trace contract and build deterministic projections

**Files:**
- Create: `internal/tokentracer/dashboard/src/trace/types.ts`
- Create: `internal/tokentracer/dashboard/src/trace/format.ts`
- Create: `internal/tokentracer/dashboard/src/trace/projections.ts`
- Create: `internal/tokentracer/dashboard/src/trace/projections.test.ts`
- Create: `internal/tokentracer/dashboard/src/test/fixtures.ts`

- [ ] **Step 1: Define the JSON contract without renaming backend fields**

Use snake_case at the transport boundary so no decoder layer can silently drift:

```ts
export interface Usage {
  input: number;
  output: number;
  cache_read: number;
  cache_creation: number;
  total_context: number;
  cache_hit_rate: number;
}

export interface TimelineMarker {
  type: string;
  time: string;
  label: string;
  detail?: string;
  status?: string;
  usage?: Usage;
}

export interface TimelineRow {
  id: string;
  kind: string;
  stage_id?: string;
  stage_name?: string;
  agent_id?: string;
  parent_id?: string;
  name: string;
  display_name?: string;
  role?: string;
  provider?: string;
  model?: string;
  session_id?: string;
  invocation_index?: number;
  start_time: string;
  end_time: string;
  duration_ms: number;
  status: string;
  error?: string;
  usage: Usage;
  token_grand_total: number;
  token_share: number;
  calls: number;
  critical?: boolean;
  bottleneck?: boolean;
  markers?: TimelineMarker[];
}

export interface TraceEvent {
  seq: number;
  type: string;
  timestamp: string;
  data?: Record<string, unknown>;
}

export interface TraceSnapshot {
  pipeline: {
    id: string;
    name: string;
    start_time: string;
    end_time?: string;
    total: Usage;
    calls: number;
    status: string;
  };
  run_id: string;
  session_id?: string;
  workspace?: string;
  server_url?: string;
  timeline: {
    start_time?: string;
    end_time?: string;
    generated_at?: string;
    duration_ms: number;
    max_concurrency: number;
    overlap_ms: number;
    token_total: Usage;
    token_grand_total: number;
    error?: string;
    critical_path?: string[];
    bottleneck_id?: string;
    rows: TimelineRow[];
  };
  events: TraceEvent[];
}

export type PanelID = 'calls' | 'heatmap' | 'flame' | 'inspector' | 'events';
export type TimeRange = { startMS: number; endMS: number };
export type TokenPart = 'input' | 'cache_read' | 'cache_creation' | 'output';
```

- [ ] **Step 2: Write failing projection tests with a fixed snapshot fixture**

The fixture uses a run from `2026-08-13T00:00:00Z` to `00:00:10Z`, one stage row, overlapping `planner` and failed `failed-row` agent rows, an `outside-row` from 8–10 seconds, all four Token parts, three API markers whose leaf-row usage sums to the pipeline total, and three consecutive cleanup events. Export `fixtureRange = { startMS: 1_786_579_202_000, endMS: 1_786_579_205_000 }`; generate larger fixtures by repeating rows/events with unique IDs and monotonically increasing sequence numbers. Test these exact expectations:

```ts
test('builds pixel-bounded heat buckets and preserves usage totals', () => {
  const buckets = buildHeatmap(fixtureSnapshot, fixtureSnapshot.timeline.rows, 64);
  expect(buckets).toHaveLength(3);
  expect(Math.max(...buckets.map((row) => row.cells.length))).toBeLessThanOrEqual(64);
  expect(sumBucketUsage(buckets)).toEqual(fixtureSnapshot.timeline.token_total);
});

test('builds a continuous run-stage-agent-call flame tree', () => {
  const root = buildFlameTree(fixtureSnapshot, 'tokens');
  expect(root.kind).toBe('run');
  expect(root.children.flatMap((stage) => stage.children).some((node) => node.kind === 'agent')).toBe(true);
  expect(flattenFlame(root).some((node) => node.kind === 'api_call')).toBe(true);
});

test('selection range annotates rather than removes rows', () => {
  const result = projectRows(fixtureSnapshot.timeline.rows, defaultFilters, fixtureRange);
  expect(result).toHaveLength(fixtureSnapshot.timeline.rows.length);
  expect(result.some((row) => row.inRange === false)).toBe(true);
});

test('compacts consecutive cleanup events and reports the hidden count', () => {
  expect(compactEvents(fixtureSnapshot.events)).toEqual(expect.arrayContaining([
    expect.objectContaining({ hiddenCount: 2 })
  ]));
});
```

Run: `npm --prefix internal/tokentracer/dashboard test -- src/trace/projections.test.ts`

Expected: FAIL because the projection functions do not exist.

- [ ] **Step 3: Implement the pure projection surface**

Export these exact domain types and functions from `projections.ts`:

```ts
export interface TraceFilters {
  scope: 'all' | 'stage' | 'agent';
  model: string | null;
  errorsOnly: boolean;
}

export interface ProjectedRow extends TimelineRow {
  startMS: number;
  endMS: number;
  throughput: number;
  inRange: boolean;
}

export interface HeatCell {
  rowID: string;
  column: number;
  startMS: number;
  endMS: number;
  usage: Usage;
  tokenTotal: number;
  status: string;
}

export interface HeatRow { rowID: string; label: string; cells: HeatCell[] }

export interface FlameNode {
  id: string;
  rowID?: string;
  kind: 'run' | 'stage' | 'agent' | 'api_call' | 'event_cluster';
  label: string;
  status: string;
  startMS: number;
  endMS: number;
  durationMS: number;
  tokenTotal: number;
  usage: Usage;
  children: FlameNode[];
}

export interface CompactedEvent extends TraceEvent { hiddenCount: number; relatedRowID?: string }

export const defaultFilters: TraceFilters = { scope: 'all', model: null, errorsOnly: false };
export function projectRows(rows: TimelineRow[], filters: TraceFilters, range: TimeRange | null): ProjectedRow[];
export function buildHeatmap(snapshot: TraceSnapshot, rows: TimelineRow[], columns: number): HeatRow[];
export function sumBucketUsage(rows: HeatRow[]): Usage;
export function buildFlameTree(snapshot: TraceSnapshot, mode: 'tokens' | 'duration'): FlameNode;
export function flattenFlame(root: FlameNode): FlameNode[];
export function compactEvents(events: TraceEvent[]): CompactedEvent[];
```

Implementation rules:

- Parse RFC3339 values with `Date.parse`; clamp invalid or negative intervals to zero.
- Compute throughput as `token_grand_total / max(duration_ms / 1000, 0.001)`.
- Heatmap column count is `max(1, floor(columns))`; split each API marker into its timestamp bucket and place a row-level fallback at the row midpoint only when no marker carries usage.
- Exclude aggregate stage rows from Heatmap summation when they have child rows; include a stage only when it is a leaf. This prevents parent/child Token double counting while Calls Table can still display both diagnostic levels.
- Preserve totals by assigning the row fallback only once, not once per pixel.
- Build stage nodes from `kind === 'stage'`; attach agent rows by `parent_id`/`stage_id`; create one API child per `api_call` marker; group consecutive same-type non-API events with the same row identity into `event_cluster` leaves.
- In Token mode, the node width weight is `max(tokenTotal, 1)`; in duration mode it is `max(durationMS, 1)`. Do not add fake Token values to `usage`.
- Compact only consecutive duplicate cleanup/cascade events; keep the first event and set `hiddenCount` to the number suppressed.

- [ ] **Step 4: Add formatting helpers and make the projection tests pass**

`format.ts` exports `formatCount`, `formatDuration`, `formatPercent`, `formatThroughput`, `tokenTotal`, and `usageParts`. Use `Intl.NumberFormat`, not hand-built locale separators.

Run:

```bash
npm --prefix internal/tokentracer/dashboard test -- src/trace/projections.test.ts
npm --prefix internal/tokentracer/dashboard run typecheck
```

Expected: all projection tests pass and TypeScript reports no errors.

### Task 3: Implement shared Trace, Selection, and Filter stores

**Files:**
- Create: `internal/tokentracer/dashboard/src/stores/TraceStore.ts`
- Create: `internal/tokentracer/dashboard/src/stores/TraceStore.test.ts`
- Create: `internal/tokentracer/dashboard/src/stores/SelectionStore.ts`
- Create: `internal/tokentracer/dashboard/src/stores/SelectionStore.test.ts`
- Create: `internal/tokentracer/dashboard/src/stores/FilterStore.ts`
- Create: `internal/tokentracer/dashboard/src/stores/StoreProvider.tsx`

- [ ] **Step 1: Write failing store tests against injected I/O**

```ts
test('coalesces event bursts into one in-flight snapshot request', async () => {
  const io = createFakeTraceIO(fixtureSnapshot);
  const store = new TraceStore(io, 150);
  await store.start();
  io.source.emit('token_tracer', '{}');
  io.source.emit('token_tracer', '{}');
  vi.advanceTimersByTime(150);
  expect(io.fetchSnapshot).toHaveBeenCalledTimes(2);
  expect(io.maxConcurrentFetches()).toBe(1);
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
```

In `TraceStore.test.ts`, define `createFakeTraceIO` as a deterministic fake implementing `TraceIO`: `fetchSnapshot` increments a concurrent counter, resolves a queued deferred promise with `fixtureSnapshot`, and records the maximum; `createEventSource` returns a class with listener maps plus `emit`, `fail`, and `open` methods. Use `vi.useFakeTimers()` and `await vi.runAllTimersAsync()` so the test has no wall-clock sleeps.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/stores`

Expected: FAIL because the stores and fake I/O are not defined.

- [ ] **Step 2: Implement `TraceStore` as a `useSyncExternalStore` source**

Use this public surface:

```ts
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

export class TraceStore {
  constructor(io?: TraceIO, refreshDelayMS = 150);
  start(): Promise<void>;
  retry(): Promise<void>;
  dispose(): void;
  subscribe(listener: () => void): () => void;
  getSnapshot(): TraceState;
}
```

The default I/O fetches `/api/state` with `cache: 'no-store'` and opens `/events`. Keep one `AbortController`, one refresh timer, and one in-flight promise. Event bursts set a queued boolean; after the current request resolves, run at most one queued refresh. Native EventSource reconnects; `onerror` changes only the connection label, and `onopen` requests a complete snapshot before returning to `live`.

- [ ] **Step 3: Implement transient selection and filters**

```ts
export interface SelectionState {
  selectedRowID: string | null;
  selectedEventSeq: number | null;
  selectedTimeRange: TimeRange | null;
  source: PanelID | null;
}

export class SelectionStore {
  selectRow(rowID: string, source: PanelID): void;
  selectEvent(seq: number, rowID: string | null, source: PanelID): void;
  selectRange(range: TimeRange, source: PanelID): void;
  clear(): void;
  reconcile(rowIDs: Set<string>, eventSeqs: Set<number>): void;
  subscribe(listener: () => void): () => void;
  getSnapshot(): SelectionState;
}

export class FilterStore {
  setScope(scope: TraceFilters['scope']): void;
  setModel(model: string | null): void;
  setErrorsOnly(errorsOnly: boolean): void;
  reset(): void;
  subscribe(listener: () => void): () => void;
  getSnapshot(): TraceFilters;
}
```

Do not read or write `localStorage` in either class.

- [ ] **Step 4: Wire the stores through React context and pass all tests**

`StoreProvider.tsx` creates each store once with lazy `useState`, calls `trace.start()` in an effect, disposes it on unmount, and exports `useTraceState`, `useSelection`, and `useFilters` hooks backed by `useSyncExternalStore`.

When a new snapshot arrives, a provider effect calls `selection.reconcile(new Set(snapshot.timeline.rows.map(({ id }) => id)), new Set(snapshot.events.map(({ seq }) => seq)))`. A row selection preserves an existing time range; a Heatmap bucket click sets the bucket range and then its highest-Token row so Inspector can show the selected row together with all rows intersecting that bucket.

Run:

```bash
npm --prefix internal/tokentracer/dashboard test -- src/stores
npm --prefix internal/tokentracer/dashboard run typecheck
```

Expected: store tests pass; no state changes occur after `dispose()`.

### Task 4: Make Dockview layout persistence safe and recoverable

**Files:**
- Create: `internal/tokentracer/dashboard/src/stores/LayoutStore.ts`
- Create: `internal/tokentracer/dashboard/src/stores/LayoutStore.test.ts`

- [ ] **Step 1: Write failing tests for versioning, validation, recovery, debounce, reset, and undo**

```ts
test('round-trips only a known-panel Dockview layout', () => {
  const storage = memoryStorage();
  const store = new LayoutStore(storage, fixedClock);
  store.saveNow(validDockviewLayout);
  expect(store.load()).toEqual(validDockviewLayout);
  expect(storage.getItem(LAYOUT_KEY)).not.toContain('failed request body');
});

test.each([
  ['bad json', '{'],
  ['wrong version', JSON.stringify({ schemaVersion: 2, savedAt: fixedISO, layout: validDockviewLayout })],
  ['unknown panel', JSON.stringify(envelopeWithPanel('shell'))]
])('backs up %s once and returns null', (_name, value) => {
  const storage = memoryStorage({ [LAYOUT_KEY]: value });
  const store = new LayoutStore(storage, fixedClock);
  expect(store.load()).toBeNull();
  expect(storage.keys().filter((key) => key.startsWith(RECOVERY_PREFIX))).toHaveLength(1);
});

test('reset can be undone exactly once', () => {
  const store = new LayoutStore(memoryStorage(), fixedClock);
  store.rememberForUndo(validDockviewLayout);
  expect(store.takeUndo()).toEqual(validDockviewLayout);
  expect(store.takeUndo()).toBeNull();
});
```

`memoryStorage` is a 20-line `Storage` implementation local to the test with a `Map<string, string>` backing store; `validDockviewLayout` is a minimal serialized tree containing `calls`, `heatmap`, `flame`, `inspector`, and `events`; `envelopeWithPanel(id)` clones it with that additional panel. Define `fixedClock = () => new Date('2026-08-13T00:00:00.000Z')` and `fixedISO` to the same string.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/stores/LayoutStore.test.ts`

Expected: FAIL because `LayoutStore` is missing.

- [ ] **Step 2: Implement a strict envelope and panel registry check**

```ts
import type { SerializedDockview } from 'dockview-react';

export const LAYOUT_KEY = 'paw.tokenTracer.layout.v1';
export const RECOVERY_PREFIX = 'paw.tokenTracer.layout.recovery.';
export const PANEL_IDS = ['calls', 'heatmap', 'flame', 'inspector', 'events'] as const;

interface LayoutEnvelope {
  schemaVersion: 1;
  savedAt: string;
  layout: SerializedDockview;
}

export class LayoutStore {
  constructor(storage: Storage = localStorage, now: () => Date = () => new Date());
  load(): SerializedDockview | null;
  quarantineStoredLayout(): void;
  scheduleSave(layout: SerializedDockview): void;
  saveNow(layout: SerializedDockview): void;
  rememberForUndo(layout: SerializedDockview): void;
  takeUndo(): SerializedDockview | null;
  dispose(): void;
}
```

Validation must require an object envelope, `schemaVersion === 1`, a parseable `savedAt`, an object `layout`, and `Object.keys(layout.panels)` contained in `PANEL_IDS`. Each serialized panel's `id` and `contentComponent` must match its registry entry. On invalid input, copy the original string to `${RECOVERY_PREFIX}${now().toISOString()}`, remove older recovery keys, remove `LAYOUT_KEY`, and return `null`. `scheduleSave` uses one 300ms timer. Undo remains in memory for 10 seconds and is never written to storage.

- [ ] **Step 3: Run the complete layout-store test**

`quarantineStoredLayout()` backs up and removes the current raw value using the same single-recovery-key routine as `load()`; this is the only method the Dockview integration calls when `api.fromJSON()` throws.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/stores/LayoutStore.test.ts`

Expected: all tests pass, including fake-timer debounce assertions and one-recovery-key enforcement.

### Task 5: Build the fixed shell, default Dockview workspace, and narrow-screen fallback

**Files:**
- Create: `internal/tokentracer/dashboard/src/app/TopBar.tsx`
- Create: `internal/tokentracer/dashboard/src/app/DockingWorkspace.tsx`
- Create: `internal/tokentracer/dashboard/src/app/NarrowWorkspace.tsx`
- Create: `internal/tokentracer/dashboard/src/app/PanelTab.tsx`
- Create: `internal/tokentracer/dashboard/src/app/PanelHeaderActions.tsx`
- Create: `internal/tokentracer/dashboard/src/app/DockingWorkspace.test.tsx`
- Create: `internal/tokentracer/dashboard/src/components/PanelErrorBoundary.tsx`
- Create: `internal/tokentracer/dashboard/src/components/EmptyState.tsx`
- Modify: `internal/tokentracer/dashboard/src/app/App.tsx`
- Modify: `internal/tokentracer/dashboard/src/main.tsx`

- [ ] **Step 1: Write failing shell and registry tests**

```tsx
test('opens each registered panel at most once and re-enables it after close', async () => {
  render(<TestWorkspace />);
  await user.click(screen.getByRole('button', { name: '添加面板' }));
  expect(screen.getByRole('menuitem', { name: 'Calls Table' })).toBeDisabled();
  await user.click(screen.getByLabelText('关闭 Calls Table'));
  await user.click(screen.getByRole('button', { name: '添加面板' }));
  expect(screen.getByRole('menuitem', { name: 'Calls Table' })).toBeEnabled();
});

test('reset applies defaults and exposes a one-shot undo', async () => {
  const { api } = renderWorkspaceWithSavedLayout(customLayout);
  await user.click(screen.getByRole('button', { name: '恢复默认布局' }));
  expect(panelGroup(api, 'inspector')).toBe(panelGroup(api, 'events'));
  await user.click(screen.getByRole('button', { name: '撤销布局恢复' }));
  expect(api.toJSON()).toEqual(customLayout);
});

test('narrow mode does not save over the desktop layout', () => {
  const layoutStore = renderAtWidth(760, savedDesktopLayout);
  expect(screen.getByRole('tablist', { name: 'Token Tracer panels' })).toBeInTheDocument();
  expect(layoutStore.saveCount).toBe(0);
});
```

The test file provides `TestWorkspace`, `renderWorkspaceWithSavedLayout`, and `renderAtWidth` by rendering `DockingWorkspace` with injected in-memory stores and a mocked `DockviewReact`. The mock owns a real mutable fake `DockviewApi` with `getPanel`, `addPanel`, `clear`, `toJSON`, `fromJSON`, `onDidLayoutChange`, and panel `close`/`setActive`/`setSize`; every mutation emits the layout event. Keep this fake in the test file so production code never contains a Dockview substitute.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/app/DockingWorkspace.test.tsx`

Expected: FAIL because the workspace is not implemented.

- [ ] **Step 2: Define the one-instance panel registry and default layout builder**

```ts
export const PANEL_DEFINITIONS = {
  calls: { title: 'Calls Table', component: CallsTable },
  heatmap: { title: 'Token Heatmap', component: TokenHeatmap },
  flame: { title: 'Folded Flame', component: FoldedFlame },
  inspector: { title: 'Inspector', component: Inspector },
  events: { title: 'Events', component: Events }
} satisfies Record<PanelID, { title: string; component: React.ComponentType }>;

export function addPanelOnce(api: DockviewApi, id: PanelID): IDockviewPanel {
  const existing = api.getPanel(id);
  if (existing) { existing.api.setActive(); return existing; }
  return api.addPanel({ id, component: id, title: PANEL_DEFINITIONS[id].title });
}
```

`applyDefaultLayout(api)` must call `api.clear()` and then create `heatmap`; create `calls` below `heatmap`; create `flame` to the right of `heatmap`; create `inspector` below `flame`; create `events` centered on `inspector` so they share a tab group. Pass string IDs in `position.referencePanel`, use `'below'` and `'right'` for split directions, and use `direction: 'within'` with `referencePanel: 'inspector'` for the Events tab. After the first dimension event, use panel `api.setSize` to target a 64/36 left/right split and a 30/70 heatmap/table split. Never serialize this default as a hand-maintained JSON fixture.

`PanelTab` wraps `DockviewDefaultTab` in `data-testid="panel-tab-${props.api.id}"`; register it as `traceTab` and set `tabComponent: 'traceTab'` for every panel. Wrap each registered content component in a `<section data-testid="panel-${id}" data-group-id={props.api.group.id} data-location={props.api.location.type}>` so browser tests can assert docking outcomes without depending on Dockview's private class names.

- [ ] **Step 3: Implement load, autosave, reset, undo, add, close, maximize, and floating controls**

`DockingWorkspace` must:

1. Store the `DockviewApi` in a ref from `onReady`.
2. Try `layoutStore.load()` and `api.fromJSON(saved, { reuseExistingPanels: false })`; catch Dockview restore errors and call `layoutStore.quarantineStoredLayout()` before applying defaults.
3. Subscribe to `api.onDidLayoutChange` and call `layoutStore.scheduleSave(api.toJSON())` only after initial restoration completes.
4. Render `rightHeaderActionsComponent={PanelHeaderActions}`. From `IDockviewHeaderActionsProps`, use `activePanel`; its buttons call `activePanel.api.maximize()`/`exitMaximized()`, `containerApi.addFloatingGroup(activePanel)`, and `activePanel.api.close()`; return `null` when no active panel exists and do not expose `addPopoutGroup`.
5. Disable an Add Panel item when `api.getPanel(id)` exists.
6. Let the last panel close; keep the fixed TopBar visible.
7. On Reset, capture `api.toJSON()` in memory, apply defaults, show a 10-second Undo action, and save the default.

- [ ] **Step 4: Implement the fixed global TopBar**

Display pipeline name/status, connection state, duration, calls, total context, cache hit rate, output, health, Scope, Model, errors-only, Add Panel, Reset, Clear Selection, and Export JSON. Export must create a `Blob` from the current snapshot only after the explicit click, then revoke the object URL. Health is:

```ts
export function healthScore(rows: TimelineRow[]): number {
  if (rows.length === 0) return 100;
  const failed = rows.filter((row) => row.status === 'failed').length;
  return Math.max(18, Math.round((1 - failed / rows.length) * 100));
}
```

Do not store the exported JSON or include it in layout state.

- [ ] **Step 5: Implement the 960px narrow fallback and error isolation**

`App` uses `matchMedia('(max-width: 959px)')`. At narrow width render a simple WAI-ARIA tablist with all five panels in fixed order and no Dockview instance. Switching narrow tabs changes only in-memory active panel. Returning to wide mode remounts Dockview and restores the saved desktop layout.

When `TraceState.snapshot` is null, render no Dockview: `loading` shows `正在加载追踪数据…`; `error` shows the fetch error summary plus a `重试` button bound to `trace.retry()`. A successful retry initializes the workspace. During reconnection, keep the last snapshot and only change the TopBar connection badge.

Wrap every panel renderer in `PanelErrorBoundary`; its fallback shows the panel title, a sanitized `组件渲染失败` message, and a Retry button that resets only that boundary. Never render `error.stack` or trace data in the fallback.

- [ ] **Step 6: Pass workspace tests**

Run:

```bash
npm --prefix internal/tokentracer/dashboard test -- src/app
npm --prefix internal/tokentracer/dashboard run typecheck
```

Expected: single-instance, reset/undo, narrow preservation, and boundary-isolation tests pass.

### Task 6: Implement the virtualized Calls Table

**Files:**
- Create: `internal/tokentracer/dashboard/src/components/VirtualList.tsx`
- Create: `internal/tokentracer/dashboard/src/components/VirtualList.test.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/CallsTable.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/CallsTable.test.tsx`

- [ ] **Step 1: Write failing virtualization, sorting, and selection tests**

```tsx
test('renders only the visible 24px rows out of 2000 items', () => {
  const items = Array.from({ length: 2000 }, (_, index) => ({ id: String(index) }));
  render(<VirtualList items={items} rowHeight={24} height={240} overscan={4} getKey={(item) => item.id} renderRow={(item) => <div role="row">{item.id}</div>} />);
  expect(screen.getAllByRole('row').length).toBeLessThanOrEqual(19);
});

test('sorts exact token values and links selection without filtering range-out rows', async () => {
  render(<CallsTableHarness snapshot={fixtureSnapshot} />);
  await user.click(screen.getByRole('button', { name: '按总 Token 排序' }));
  expect(screen.getAllByRole('row')[1]).toHaveTextContent('failed-row');
  await user.click(screen.getByRole('row', { name: /failed-row/ }));
  expect(selectionStore.getSnapshot().selectedRowID).toBe('failed-row');
  selectionStore.selectRange(fixtureRange, 'heatmap');
  expect(screen.getAllByRole('row')).toHaveLength(fixtureSnapshot.timeline.rows.length + 1);
  expect(screen.getByRole('row', { name: /outside-row/ })).toHaveAttribute('data-in-range', 'false');
});
```

`CallsTableHarness` is local test code that constructs `TraceStore`, `SelectionStore`, and `FilterStore`, passes them through the injectable `StoreProvider`, and supplies `openPanel = vi.fn()`. Use `makeLargeSnapshot(2_000, 2_000)` from `src/test/fixtures.ts` for the scale assertion.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/components/VirtualList.test.tsx src/panels/CallsTable.test.tsx`

Expected: FAIL because neither component exists.

- [ ] **Step 2: Implement fixed-row virtualization without another dependency**

`VirtualList<T>` takes `items`, `rowHeight`, `height`, `overscan`, `getKey`, and `renderRow`. Derive `start = max(0, floor(scrollTop / rowHeight) - overscan)` and `end = min(items.length, ceil((scrollTop + height) / rowHeight) + overscan)`. Render one relative scroll container, one spacer of `items.length * rowHeight`, and absolutely positioned visible children. Preserve keyboard focus by overscanning the active row and calling `scrollIntoView(index)` when selection moves.

- [ ] **Step 3: Implement the exact high-density columns and interactions**

Use a 24px row height and these columns: index, name/scope, mini-time, stacked Token bar, input, cache read, cache creation, output, throughput, status. Sort keys are `start`, `duration`, `tokens`, `output`, `throughput`, and `status`; first click is descending for numeric metrics and ascending for start/name. The mini-time bar uses timeline-relative percentages; Token segments use the four CSS variables from Task 10.

Rows must have `aria-selected`, a textual status glyph (`✓`, `!`, or `…`), exact-number `title` values, and `data-in-range`. Click calls `selectRow`; Enter selects and activates Inspector through an injected `openPanel('inspector')`; double-click does the same. ArrowUp/ArrowDown moves selection without changing filters.

- [ ] **Step 4: Pass table tests at 2,000-row scale**

Run:

```bash
npm --prefix internal/tokentracer/dashboard test -- src/components/VirtualList.test.tsx src/panels/CallsTable.test.tsx
npm --prefix internal/tokentracer/dashboard run typecheck
```

Expected: DOM row count remains bounded while keyboard and exact-value assertions pass.

### Task 7: Implement the Canvas Token Heatmap and linked time-range brushing

**Files:**
- Create: `internal/tokentracer/dashboard/src/panels/TokenHeatmap.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/TokenHeatmap.test.tsx`

- [ ] **Step 1: Write failing drawing and brushing tests with a mocked canvas context**

```tsx
test('aggregates to device pixels and draws failures with a shape marker', () => {
  const context = installCanvasContextSpy();
  render(<TokenHeatmapHarness width={320} height={180} snapshot={fixtureSnapshot} />);
  expect(context.fillRect.mock.calls.length).toBeLessThanOrEqual(320 * fixtureSnapshot.timeline.rows.length);
  expect(context.stroke.mock.calls.length).toBeGreaterThan(0);
});

test('brush selection updates only SelectionStore', async () => {
  render(<TokenHeatmapHarness width={400} height={180} snapshot={fixtureSnapshot} />);
  fireEvent.pointerDown(screen.getByRole('img', { name: 'Token Heatmap' }), { clientX: 80, clientY: 40, pointerId: 1 });
  fireEvent.pointerMove(screen.getByRole('img', { name: 'Token Heatmap' }), { clientX: 240, clientY: 120, pointerId: 1 });
  fireEvent.pointerUp(screen.getByRole('img', { name: 'Token Heatmap' }), { clientX: 240, clientY: 120, pointerId: 1 });
  expect(selectionStore.getSnapshot().selectedTimeRange).toEqual({ startMS: 1_786_579_202_000, endMS: 1_786_579_206_000 });
  expect(filterStore.getSnapshot()).toEqual(defaultFilters);
});
```

`installCanvasContextSpy` returns an object containing Vitest spies for every used `CanvasRenderingContext2D` method and assigns `HTMLCanvasElement.prototype.getContext` to return it. `TokenHeatmapHarness` renders the component with `fixtureSnapshot`, the injected stores, and a stubbed `getBoundingClientRect()` of exactly 400×180; this makes the pointer-to-time expectation above deterministic.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/TokenHeatmap.test.tsx`

Expected: FAIL because the Heatmap does not exist.

- [ ] **Step 2: Implement resize-coalesced Canvas drawing**

Use one canvas, one accessible hidden description, and one `ResizeObserver`. Coalesce size updates with `requestAnimationFrame`; set backing size to CSS size times `devicePixelRatio`. Call `buildHeatmap(snapshot, rows, floor(plotWidth))`. Draw low-contrast alternating row bands, Token intensity with a normalized log scale, a red diagonal failure stroke plus `!` glyph, selected-row outline, and a translucent range mask. Do not create a DOM node per cell.

- [ ] **Step 3: Implement hover, click, wheel zoom, and range brushing**

Map pointer coordinates to the visible row and bucket. Hover renders a single positioned tooltip with bucket time, total and four Token parts, throughput, and error summary. Click selects the cell's highest-Token row. Pointer drag creates a normalized `TimeRange`; wheel with Ctrl/Meta or the `+`/`−` buttons changes the local visible time domain. Zoom is panel-local and must not be saved in `LayoutStore`.

- [ ] **Step 4: Pass Heatmap tests**

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/TokenHeatmap.test.tsx`

Expected: drawing is pixel-bounded, range selection changes no filters, and the selected row is announced in the hidden description.

### Task 8: Implement the SVG Folded Flame

**Files:**
- Create: `internal/tokentracer/dashboard/src/panels/FoldedFlame.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/FoldedFlame.test.tsx`

- [ ] **Step 1: Write failing mode, hierarchy, drill-down, and status tests**

```tsx
test('defaults to token width and can switch to duration', async () => {
  render(<FoldedFlameHarness snapshot={fixtureSnapshot} />);
  expect(screen.getByRole('button', { name: '按 Token 宽度' })).toHaveAttribute('aria-pressed', 'true');
  const before = Number(screen.getByTestId('flame-node-failed-row').getAttribute('width'));
  await user.click(screen.getByRole('button', { name: '按耗时宽度' }));
  expect(Number(screen.getByTestId('flame-node-failed-row').getAttribute('width'))).not.toBe(before);
});

test('drills into a stage and retains a textual failed marker', async () => {
  render(<FoldedFlameHarness snapshot={fixtureSnapshot} />);
  await user.click(screen.getByRole('button', { name: /进入 Turn 1/ }));
  expect(screen.getByRole('button', { name: '返回上层' })).toBeEnabled();
  expect(screen.getByText('failed')).toBeInTheDocument();
});
```

`FoldedFlameHarness` is the same injected-store harness pattern used by Calls Table and fixes its SVG width to 600. Every rendered node sets `data-testid="flame-node-${rowID ?? id}"`, making the width test independent of SVG browser layout.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/FoldedFlame.test.tsx`

Expected: FAIL because the Flame panel is missing.

- [ ] **Step 2: Implement continuous hierarchy layout**

Build the tree with `buildFlameTree`; choose the current drill root by ID. For each depth, assign every child a continuous `[x, x + width]` interval proportional to Token or duration weight, with no wall-clock gaps. Render only the current subtree and cap visible nodes at 800 by grouping the smallest siblings into one `event_cluster` named `其他 N 项`; retain their aggregate usage/status.

- [ ] **Step 3: Implement SVG interaction and accessibility**

Render one `<g>` per visible node containing `<rect>`, clipped text when width permits, and `<title>` with exact values. A failed node also renders a hatch/stroke and visible `failed` text when wide enough. Click selects `rowID`; double-click or the node's accessible `进入 ...` button drills down. Provide Token/Duration segmented buttons and a breadcrumb Back action. Shade nodes outside the selected time range; do not remove them.

- [ ] **Step 4: Pass Flame tests**

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/FoldedFlame.test.tsx`

Expected: Token and duration layouts differ, drill-down works, and failures remain identifiable without color.

### Task 9: Implement Inspector and the virtualized compact Events stream

**Files:**
- Create: `internal/tokentracer/dashboard/src/panels/Inspector.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/Inspector.test.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/Events.tsx`
- Create: `internal/tokentracer/dashboard/src/panels/Events.test.tsx`

- [ ] **Step 1: Write failing empty-state, details, compaction, copy, and linkage tests**

```tsx
test('shows an explicit empty state and complete selected-row metrics', () => {
  const { rerender } = render(<InspectorHarness snapshot={fixtureSnapshot} />);
  expect(screen.getByText('选择调用、事件或时间桶查看详情')).toBeInTheDocument();
  selectionStore.selectRow('failed-row', 'calls');
  rerender(<InspectorHarness snapshot={fixtureSnapshot} />);
  expect(screen.getByText('cache creation')).toBeInTheDocument();
  expect(screen.getByText(fixtureSnapshot.timeline.error!)).toBeInTheDocument();
});

test('compacts repeated events, links to a row, and copies sanitized error text', async () => {
  const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
  render(<EventsHarness snapshot={fixtureSnapshot} />);
  expect(screen.getByText(/隐藏 2 条重复事件/)).toBeInTheDocument();
  await user.click(screen.getByRole('row', { name: /api_call/ }));
  expect(selectionStore.getSnapshot().selectedRowID).not.toBeNull();
  await user.click(screen.getByRole('button', { name: '复制错误详情' }));
  expect(writeText).toHaveBeenCalledWith(expect.not.stringContaining('Authorization'));
});
```

`InspectorHarness` and `EventsHarness` use the same injected stores and `fixtureSnapshot`. In `setup.ts`, install `navigator.clipboard = { writeText: vi.fn() }` with `Object.defineProperty` so copy behavior is deterministic in jsdom.

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/Inspector.test.tsx src/panels/Events.test.tsx`

Expected: FAIL because both panels are missing.

- [ ] **Step 2: Implement Inspector from the shared snapshot only**

Resolve selection in priority order: selected row, selected event, selected range, then global empty state. Render ID, name, kind, stage, agent, role, session, provider, model, calls, start/end/duration, all Token parts, total, share, throughput, status, error, markers, and related event summaries. When a time range is also selected, list every intersecting row and its aggregate Token total below the primary selection; this is how a multi-call Heatmap bucket remains inspectable. Use text nodes only; do not use `dangerouslySetInnerHTML`. If a selected event references a row, show both event and row fields.

- [ ] **Step 3: Implement Events with compaction and virtualization**

Use `compactEvents` and `VirtualList` with a 28px compact row and expandable details. Filters are event type and `errorsOnly`, local to this panel. Clicking an event calls `selectEvent(seq, relatedRowID, 'events')`. Display `隐藏 N 条重复事件`. Copy only a field-level error string after passing it through `redactSensitiveText`, which replaces case-insensitive `authorization`, `api_key`, `token`, and `secret` assignment values with `[REDACTED]`.

- [ ] **Step 4: Pass Inspector and Events tests at 2,000 events**

Run: `npm --prefix internal/tokentracer/dashboard test -- src/panels/Inspector.test.tsx src/panels/Events.test.tsx`

Expected: complete metrics and linkage pass; Events keeps a bounded DOM row count.

### Task 10: Apply the soft ambient visual system without losing density

**Files:**
- Create: `internal/tokentracer/dashboard/src/styles/tokens.css`
- Create: `internal/tokentracer/dashboard/src/styles/app.css`
- Create: `internal/tokentracer/dashboard/src/styles/dockview.css`
- Create: `internal/tokentracer/dashboard/src/styles/panels.css`
- Modify: `internal/tokentracer/dashboard/src/main.tsx`
- Modify: all panel components for semantic class names and ARIA labels

- [ ] **Step 1: Add the locked semantic palette and density tokens**

```css
:root {
  color-scheme: dark;
  --bg-0: #07101a;
  --bg-1: #0b1723;
  --surface: rgba(16, 29, 42, 0.86);
  --surface-raised: rgba(22, 39, 55, 0.9);
  --line: rgba(141, 174, 200, 0.13);
  --line-active: rgba(104, 187, 255, 0.5);
  --text: #e7f0f6;
  --muted: #8fa5b6;
  --input: #5ea2ef;
  --cache-read: #52ccb1;
  --cache-create: #e1ad5d;
  --output: #ef7e70;
  --failed: #ff6874;
  --selected: #67baff;
  --panel-radius: 12px;
  --row-height: 24px;
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-variant-numeric: tabular-nums;
}
```

- [ ] **Step 2: Theme Dockview through variables and low-contrast separators**

Import Dockview's base CSS first, then the four project styles. Override group backgrounds, headers, tabs, drop overlays, split sashes, and floating shadows. Use 12px group radius, 1px low-alpha borders, 6–10px panel padding, 24px table rows, and no per-row cards. Sashes become visible on hover/focus/drag; inactive lines remain below 0.16 alpha. Preserve Dockview's focus outlines with the selected color.

- [ ] **Step 3: Add non-color status and reduced-motion behavior**

Every failed status includes `!` plus text; live includes a pulse dot plus `运行中`; completed includes `✓`. Add `@media (prefers-reduced-motion: reduce)` that disables pulsing and non-essential transitions. Ensure every icon-only action has an `aria-label`, all menus are keyboard reachable, and selected rows/nodes expose `aria-selected` or equivalent pressed state.

- [ ] **Step 4: Run component tests and production build**

Run:

```bash
npm --prefix internal/tokentracer/dashboard test
npm --prefix internal/tokentracer/dashboard run build
```

Expected: all frontend unit/component tests pass and `dist/` is regenerated with no TypeScript errors.

### Task 11: Replace embedded source strings with the built dashboard

**Files:**
- Create: `internal/tokentracer/dashboard_embed.go`
- Create: `internal/tokentracer/dashboard_embed_test.go`
- Modify: `internal/tokentracer/server.go:36-141`
- Delete after tests pass: `internal/tokentracer/dashboard.go`
- Remove after tests pass: `internal/tokentracer/server.go:160-end` (`legacyDashboardHTML`)

- [ ] **Step 1: Write failing embedded asset and route tests before changing the server**

```go
func TestDashboardAssetsContainBuiltEntry(t *testing.T) {
	data, err := fs.ReadFile(dashboardAssets, "dashboard/dist/index.html")
	if err != nil { t.Fatalf("read embedded dashboard: %v", err) }
	if !bytes.Contains(data, []byte(`<div id="root"></div>`)) {
		t.Fatalf("embedded index missing React root: %s", data)
	}
}

func TestDashboardRoutesEmbeddedAssets(t *testing.T) {
	handler := NewServer(New("Paw"), ServerConfig{}).handler()
	for _, tc := range []struct{ path, contentType string; status int }{
		{"/", "text/html", http.StatusOK},
		{firstEmbeddedAssetPath(t), "", http.StatusOK},
		{"/assets/missing.js", "", http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.status { t.Fatalf("GET %s = %d, want %d", tc.path, recorder.Code, tc.status) }
		if tc.contentType != "" && !strings.Contains(recorder.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("GET %s content-type = %q", tc.path, recorder.Header().Get("Content-Type"))
		}
	}
}
```

Run: `go test ./internal/tokentracer -run 'TestDashboardAssets|TestDashboardRoutes' -count=1`

Expected: FAIL because `dashboardAssets` and `handler()` do not exist.

- [ ] **Step 2: Embed `dist` and centralize route construction**

Create `dashboard_embed.go`:

```go
package tokentracer

import "embed"

//go:embed dashboard/dist
var dashboardAssets embed.FS
```

Refactor `Server.Start` to set `s.server = &http.Server{Handler: s.handler()}`. Implement:

```go
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/", s.dashboardHandler())
	return mux
}

func (s *Server) dashboardHandler() http.Handler {
	dist, err := fs.Sub(dashboardAssets, "dashboard/dist")
	if err != nil { panic(fmt.Sprintf("open embedded token tracer dashboard: %v", err)) }
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { w.Header().Set("Cache-Control", "no-store") }
		files.ServeHTTP(w, r)
	})
}
```

Order API/SSE routes before `/`. Do not add SPA fallback for unknown assets; they must remain 404.

- [ ] **Step 3: Run focused Go tests, then remove both legacy HTML sources**

Run: `go test ./internal/tokentracer -run 'TestDashboardAssets|TestDashboardRoutes|TestServerExposesState' -count=1`

Expected: PASS.

Then delete `internal/tokentracer/dashboard.go` and remove the full `legacyDashboardHTML` constant from `server.go`. Search before and after:

```bash
rg -n 'dashboardHTML|legacyDashboardHTML|执行瀑布' internal/tokentracer
```

Expected after deletion: no matches.

- [ ] **Step 4: Prove Go works without Node at runtime**

Run:

```bash
go test ./internal/tokentracer -count=1
go build ./...
```

Expected: both commands pass using only committed files under `dashboard/dist`.

### Task 12: Add a deterministic real Go fixture and browser end-to-end coverage

**Files:**
- Create: `internal/tokentracer/testdata/dashboardfixture/main.go`
- Create: `internal/tokentracer/dashboard/playwright.config.ts`
- Create: `internal/tokentracer/dashboard/e2e/token-tracer.spec.ts`

- [ ] **Step 1: Create a real local fixture server with failures, overlap, and 2,000 events**

The fixture parses `-port` (default `18999`), creates `tokentracer.New("Token Tracer E2E")`, starts three turns/agents with all Token parts, records one failed turn, and emits 2,000 deterministic `tool_event`/cleanup events. Start `NewServer` on `127.0.0.1`, print the URL, listen for SIGINT/SIGTERM, and call `Shutdown` with a two-second timeout. Do not include credentials or request bodies in fixture events.

Use this bounded startup/shutdown skeleton around a `seed(tracer)` helper; `seed` calls `StartTurn`, three `RecordAPICall` calls with fixed usages, `FinishTurn` once with `errors.New("fixture failure")`, and exactly 2,000 `RecordEvent` calls:

```go
func main() {
	port := flag.Int("port", 18999, "listen port")
	flag.Parse()
	tracer := tokentracer.New("Token Tracer E2E")
	seed(tracer)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := tokentracer.NewServer(tracer, tokentracer.ServerConfig{Host: "127.0.0.1", Port: *port})
	if err := server.Start(ctx); err != nil { log.Fatal(err) }
	fmt.Println(server.URL())
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil { log.Fatal(err) }
}
```

Run: `go run ./internal/tokentracer/testdata/dashboardfixture -port 18999`

Expected: `curl http://127.0.0.1:18999/api/state` returns a snapshot with a failed row, nonzero `cache_creation`, and 2,000 retained events. Stop the process after the check.

- [ ] **Step 2: Configure Playwright to own the fixture lifecycle**

```ts
import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';

const tokenTracerDir = fileURLToPath(new URL('..', import.meta.url));

export default defineConfig({
  testDir: './e2e',
  timeout: 45_000,
  use: {
    baseURL: 'http://127.0.0.1:18999',
    viewport: { width: 1440, height: 1000 },
    colorScheme: 'dark',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'go run ./testdata/dashboardfixture -port 18999',
    url: 'http://127.0.0.1:18999/healthz',
    cwd: tokenTracerDir,
    reuseExistingServer: false,
    timeout: 60_000
  }
});
```

Run: `npm --prefix internal/tokentracer/dashboard exec -- playwright test --list`

Expected: Playwright lists the dashboard tests and resolves `tokenTracerDir` to `internal/tokentracer`.

- [ ] **Step 3: Write desktop interaction tests for the approved workflow**

The test suite must use stable `data-testid="panel-tab-${id}"`, `data-testid="panel-${id}"`, and header-action selectors and cover:

```ts
test('docks, tabs, resizes, persists, closes, restores, resets, and undoes', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('Token Tracer E2E')).toBeVisible();
  await dockToEdge(page, 'events', 'heatmap', 'left');
  await dockToEdge(page, 'events', 'heatmap', 'right');
  await dockToEdge(page, 'events', 'heatmap', 'top');
  await dockToEdge(page, 'events', 'heatmap', 'bottom');
  await dockAsTab(page, 'events', 'inspector');
  await resizeNearestSash(page, 'calls', 120);
  await page.getByLabel('最大化 Calls Table').click();
  await expect(page.getByTestId('panel-calls')).toBeVisible();
  await page.getByLabel('恢复 Calls Table').click();
  await page.getByLabel('浮动 Events').click();
  await expect(page.getByTestId('panel-events')).toHaveAttribute('data-location', 'floating');
  const saved = await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'));
  await page.reload();
  expect(await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'))).toBe(saved);
  await page.getByLabel('关闭 Calls Table').click();
  await page.getByRole('button', { name: '添加面板' }).click();
  await page.getByRole('menuitem', { name: 'Calls Table' }).click();
  await page.getByRole('button', { name: '恢复默认布局' }).click();
  await page.getByRole('button', { name: '撤销布局恢复' }).click();
});
```

Implement `dockToEdge`, `dockAsTab`, and `resizeNearestSash` in the same test file using Playwright mouse events and Dockview's visible drop overlays. Each helper must assert the resulting panel bounding boxes or shared tab group before continuing; do not rely only on a drag completing without error.

- [ ] **Step 4: Add linked-selection, corruption recovery, SSE, and narrow-mode tests**

Cover these exact outcomes:

- Clicking the failed Table row marks the matching Heatmap/Flame element selected and fills Inspector.
- Heatmap brushing leaves the Table row count unchanged.
- Aborting `/events` shows `重新连接中` while the last data remains visible; allowing the request again returns to `实时`.
- Writing `{` to `paw.tokenTracer.layout.v1` then reloading shows all default panels and exactly one recovery key.
- At 760×900, a single tablist replaces Dockview; switching back to 1440×1000 restores the pre-narrow serialized desktop layout.
- The Events scroll container remains responsive with 2,000 entries and fewer than 50 event rows in the DOM.

Run:

```bash
npm --prefix internal/tokentracer/dashboard exec -- playwright install chromium
npm --prefix internal/tokentracer/dashboard run e2e
```

Expected: all user-path tests pass against the embedded dashboard served by Go.

### Task 13: Final runtime, visual, and repository verification

**Files:**
- Regenerate: `internal/tokentracer/dashboard/dist/**`
- Create: `.agent/visual/token-tracer-desktop.png`
- Create: `.agent/visual/token-tracer-narrow.png`
- Create: `.agent/visual/token-tracer-docking-workspace.md`
- Modify: `memory/progress.md`
- Modify: `memory/verify.md`

- [ ] **Step 1: Regenerate and inspect tracked build artifacts**

Run:

```bash
npm --prefix internal/tokentracer/dashboard run build
git status --short internal/tokentracer/dashboard
git diff --check -- internal/tokentracer/dashboard internal/tokentracer/dashboard_embed.go internal/tokentracer/server.go
```

Expected: `dist/` matches the current source, no ignored entry file, and no whitespace errors.

- [ ] **Step 2: Run the complete frontend and Go verification contract**

Run in this order:

```bash
npm --prefix internal/tokentracer/dashboard test
npm --prefix internal/tokentracer/dashboard run build
npm --prefix internal/tokentracer/dashboard run e2e
go build ./...
go test ./...
```

Expected: every command exits 0. If an unrelated pre-existing dirty-file test fails, capture the exact failure and prove the focused Token Tracer commands still pass; do not alter the unrelated files to hide it.

- [ ] **Step 3: Capture fresh desktop and narrow screenshots from the real fixture**

Use Playwright against `http://127.0.0.1:18999/` to save:

- `.agent/visual/token-tracer-desktop.png` at 1440×1000 with Calls + Heatmap + Flame + Inspector/Events visible;
- `.agent/visual/token-tracer-narrow.png` at 760×900 with the single-column tab fallback visible.

Verify both files are non-empty with `file` and `wc -c`, then visually inspect them for clipped controls, hard high-contrast grid lines, illegible values, overlap, unintended popout controls, and broken selected/failed states.

- [ ] **Step 4: Write structured visual evidence**

Create `.agent/visual/token-tracer-docking-workspace.md`:

```markdown
# Token Tracer docking workspace visual evidence

- Changed files: `internal/tokentracer/dashboard/**`, `internal/tokentracer/dashboard_embed.go`, `internal/tokentracer/server.go`
- Route / URL: `http://127.0.0.1:18999/`
- Desktop viewport: `1440x1000`
- Desktop artifact: `.agent/visual/token-tracer-desktop.png`
- Narrow viewport: `760x900`
- Narrow artifact: `.agent/visual/token-tracer-narrow.png`
- Observed result: Five single-instance panels render real snapshot data; the desktop workspace supports dock/tab/resize/maximize/float/close/restore, linked selections remain highlights rather than filters, the narrow layout preserves the desktop layout, and the soft ambient hierarchy has no clipped primary controls or high-contrast wire-grid effect.
```

- [ ] **Step 5: Update persistent project state and perform the final scope audit**

Mark implementation and verification complete in `memory/progress.md`; add the exact final command results and screenshot paths to `memory/verify.md`. Then run:

```bash
git status --short
git diff --name-only -- . ':(exclude)internal/config/**' ':(exclude)internal/loop/**' ':(exclude)internal/theme/**' ':(exclude)internal/ui/bubble/**'
rg -n -g '*.go' -g '*.ts' -g '*.tsx' 'TBD|TODO|implement later|dangerouslySetInnerHTML|addPopoutGroup|dashboardHTML|legacyDashboardHTML' internal/tokentracer
```

Expected: only the intended Token Tracer, visual evidence, ignore rule, and project-memory files are in scope; forbidden placeholders, unsafe HTML, popout calls, and legacy dashboard strings are absent.

## Final acceptance matrix

| Approved requirement | Evidence |
|---|---|
| Calls Table, Token Heatmap, Folded Flame | Tasks 6–8 unit/component tests and desktop screenshot |
| Inspector and Events | Task 9 tests; 2,000-event browser assertion |
| IDE drag/split/tab/resize/float/maximize/close | Task 5 APIs and Task 12 browser paths |
| One instance per panel | Task 5 component test and Add Panel state |
| Global last-layout autosave | Task 4 debounce tests and Task 12 reload assertion |
| Reset plus one-shot undo | Tasks 4–5 tests and Task 12 user path |
| Linked selection/range without implicit filtering | Tasks 2–3 and 6–9 tests |
| Fixed global controls | Task 5 shell test and both screenshots |
| Narrow fallback preserves desktop layout | Tasks 5 and 12 tests |
| Soft ambient styling | Task 10 and structured visual evidence |
| SSE failure/recovery and snapshot single-flight | Task 3 tests and Task 12 network test |
| Bad layout and panel failure isolation | Tasks 4–5 tests and Task 12 corruption path |
| Go binary works without Node runtime | Task 11 embedded asset tests and `go build ./...` |
| No backend trace-contract change | Existing `internal/tokentracer` tests plus final diff audit |

## Sources checked while locking the plan

- Dockview Quickstart: `https://dockview.dev/docs/overview/quickstart/?framework=react`
- Dockview adding and positioning panels: `https://dockview.dev/docs/core/panels/add/`
- Dockview API, serialization, mutation, floating, and maximize surfaces: `https://dockview.dev/docs/api/dockview/overview/`
- Dockview panel maximize/close API: `https://dockview.dev/docs/api/dockview/panelApi/`

Re-open these official pages if the installed 7.x type declarations disagree with an API call in this plan; the installed TypeScript declarations are the final compile-time authority.
