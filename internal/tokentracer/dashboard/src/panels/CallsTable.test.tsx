import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CallsTable } from './CallsTable';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, makeLargeSnapshot, fakeTraceIO } from '../test/fixtures';
import { fixtureRange } from '../test/fixtures';
import type { TraceSnapshot } from '../trace/types';

function createHarness(snapshot: TraceSnapshot) {
  const trace = new TraceStore(fakeTraceIO(snapshot));
  const selectionStore = new SelectionStore();
  const filterStore = new FilterStore();
  const openPanel = vi.fn();
  const view = render(
    <StoreProvider trace={trace} selection={selectionStore} filters={filterStore} openPanel={openPanel}>
      <CallsTable snapshot={snapshot} />
    </StoreProvider>,
  );
  return { selectionStore, filterStore, openPanel, ...view };
}

test('sorts exact token values and links selection without filtering range-out rows', async () => {
  const user = userEvent.setup();
  const { selectionStore } = createHarness(fixtureSnapshot);
  await user.click(screen.getByRole('button', { name: '按总 Token 排序' }));
  expect(screen.getAllByRole('row')[1]).toHaveTextContent('failed-row');
  await user.click(screen.getByRole('row', { name: /failed-row/ }));
  expect(selectionStore.getSnapshot().selectedRowID).toBe('failed-row');
  act(() => selectionStore.selectRange(fixtureRange, 'heatmap'));
  expect(screen.getAllByRole('row')).toHaveLength(fixtureSnapshot.timeline.rows.length + 1);
  expect(screen.getByRole('row', { name: /outside-row/ })).toHaveAttribute('data-in-range', 'false');
});

test('keeps the DOM bounded at 2000 rows', async () => {
  const user = userEvent.setup();
  createHarness(makeLargeSnapshot(2_000, 2_000));
  await user.click(screen.getByRole('button', { name: '按总 Token 排序' }));
  expect(screen.getAllByRole('row').length).toBeLessThanOrEqual(30);
});
