import { fireEvent, render, screen } from '@testing-library/react';
import { TokenHeatmap } from './TokenHeatmap';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, fakeTraceIO } from '../test/fixtures';
import { defaultFilters } from '../trace/projections';

function installCanvasContextSpy() {
  const context = {
    canvas: {} as HTMLCanvasElement,
    fillRect: vi.fn(),
    strokeRect: vi.fn(),
    clearRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    fill: vi.fn(),
    fillText: vi.fn(),
    save: vi.fn(),
    restore: vi.fn(),
    scale: vi.fn(),
    set fillStyle(value: string) {},
    get fillStyle() {
      return '';
    },
    set strokeStyle(value: string) {},
    get strokeStyle() {
      return '';
    },
    set lineWidth(value: number) {},
    get lineWidth() {
      return 1;
    },
    set font(value: string) {},
    get font() {
      return '';
    },
    set textAlign(value: string) {},
    get textAlign() {
      return '';
    },
  };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
    context as unknown as CanvasRenderingContext2D,
  );
  return context;
}

function installRectStub(width: number, height: number) {
  const rect = {
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: width,
    bottom: height,
    width,
    height,
    toJSON: () => ({}),
  } as DOMRect;
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(rect);
}

function renderHarness(width: number, height: number, snapshot = fixtureSnapshot) {
  const trace = new TraceStore(fakeTraceIO(snapshot));
  const selectionStore = new SelectionStore();
  const filterStore = new FilterStore();
  render(
    <StoreProvider trace={trace} selection={selectionStore} filters={filterStore}>
      <div style={{ width, height }}>
        <TokenHeatmap snapshot={snapshot} />
      </div>
    </StoreProvider>,
  );
  return { selectionStore, filterStore };
}

afterEach(() => {
  vi.restoreAllMocks();
});

test('aggregates to device pixels and draws failures with a shape marker', () => {
  installRectStub(320, 180);
  const context = installCanvasContextSpy();
  renderHarness(320, 180);
  expect(context.fillRect.mock.calls.length).toBeLessThanOrEqual(
    320 * fixtureSnapshot.timeline.rows.length,
  );
  expect(context.stroke.mock.calls.length).toBeGreaterThan(0);
});

test('brush selection updates only SelectionStore', async () => {
  installRectStub(400, 180);
  installCanvasContextSpy();
  const { selectionStore, filterStore } = renderHarness(400, 180);
  const canvas = screen.getByRole('img', { name: 'Token Heatmap' });
  fireEvent.pointerDown(canvas, { clientX: 80, clientY: 40, pointerId: 1 });
  fireEvent.pointerMove(canvas, { clientX: 240, clientY: 120, pointerId: 1 });
  fireEvent.pointerUp(canvas, { clientX: 240, clientY: 120, pointerId: 1 });
  expect(selectionStore.getSnapshot().selectedTimeRange).toEqual({
    startMS: 1_786_579_202_000,
    endMS: 1_786_579_206_000,
  });
  expect(filterStore.getSnapshot()).toEqual(defaultFilters);
});
