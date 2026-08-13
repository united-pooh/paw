import { act, render, screen } from '@testing-library/react';
import { Inspector } from './Inspector';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import type { SelectionStore as SelectionStoreType } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, fakeTraceIO } from '../test/fixtures';
import type { TraceSnapshot } from '../trace/types';

function InspectorHarness({
  snapshot,
  selectionStore,
}: {
  snapshot: TraceSnapshot;
  selectionStore: SelectionStoreType;
}) {
  const trace = new TraceStore(fakeTraceIO(snapshot));
  const filterStore = new FilterStore();
  return (
    <StoreProvider trace={trace} selection={selectionStore} filters={filterStore}>
      <Inspector snapshot={snapshot} />
    </StoreProvider>
  );
}

test('shows an explicit empty state and complete selected-row metrics', () => {
  const selectionStore = new SelectionStore();
  const view = render(
    <InspectorHarness snapshot={fixtureSnapshot} selectionStore={selectionStore} />,
  );
  expect(screen.getByText('选择调用、事件或时间桶查看详情')).toBeInTheDocument();
  act(() => selectionStore.selectRow('failed-row', 'calls'));
  view.rerender(<InspectorHarness snapshot={fixtureSnapshot} selectionStore={selectionStore} />);
  expect(screen.getByText('cache creation')).toBeInTheDocument();
  expect(screen.getByText(fixtureSnapshot.timeline.error!)).toBeInTheDocument();
});

test('lists intersecting rows for a selected time range', () => {
  const selectionStore = new SelectionStore();
  const view = render(
    <InspectorHarness snapshot={fixtureSnapshot} selectionStore={selectionStore} />,
  );
  act(() =>
    selectionStore.selectRange({ startMS: 1_786_579_202_000, endMS: 1_786_579_205_000 }, 'heatmap'),
  );
  view.rerender(<InspectorHarness snapshot={fixtureSnapshot} selectionStore={selectionStore} />);
  expect(screen.getByText(/时间范围/)).toBeInTheDocument();
  expect(screen.getByText(/failed-row/)).toBeInTheDocument();
});
