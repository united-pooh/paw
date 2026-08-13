import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Events } from './Events';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import type { SelectionStore as SelectionStoreType } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, makeLargeSnapshot, fakeTraceIO } from '../test/fixtures';
import type { TraceSnapshot } from '../trace/types';

function EventsHarness({
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
      <Events snapshot={snapshot} />
    </StoreProvider>
  );
}

test('compacts repeated events, links to a row, and copies sanitized error text', async () => {
  const user = userEvent.setup();
  const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
  const selectionStore = new SelectionStore();
  render(<EventsHarness snapshot={fixtureSnapshot} selectionStore={selectionStore} />);
  expect(screen.getByText(/隐藏 2 条重复事件/)).toBeInTheDocument();
  await user.click(screen.getByRole('row', { name: /api_call/ }));
  expect(selectionStore.getSnapshot().selectedRowID).not.toBeNull();
  await user.click(screen.getByRole('button', { name: '复制错误详情' }));
  expect(writeText).toHaveBeenCalledWith(expect.not.stringContaining('Authorization'));
});

test('keeps the DOM bounded at 2000 events', () => {
  render(
    <EventsHarness snapshot={makeLargeSnapshot(1, 2_000)} selectionStore={new SelectionStore()} />,
  );
  expect(screen.getAllByRole('row').length).toBeLessThanOrEqual(50);
});
