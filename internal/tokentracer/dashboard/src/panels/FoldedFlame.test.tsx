import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FoldedFlame } from './FoldedFlame';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, fakeTraceIO } from '../test/fixtures';

function FoldedFlameHarness() {
  const trace = new TraceStore(fakeTraceIO(fixtureSnapshot));
  const selectionStore = new SelectionStore();
  const filterStore = new FilterStore();
  const view = render(
    <StoreProvider trace={trace} selection={selectionStore} filters={filterStore}>
      <div style={{ width: 600 }}>
        <FoldedFlame snapshot={fixtureSnapshot} width={600} />
      </div>
    </StoreProvider>,
  );
  return { selectionStore, ...view };
}

test('defaults to token width and can switch to duration', async () => {
  const user = userEvent.setup();
  FoldedFlameHarness();
  expect(screen.getByRole('button', { name: '按 Token 宽度' })).toHaveAttribute('aria-pressed', 'true');
  const before = Number(screen.getByTestId('flame-node-failed-row').getAttribute('width'));
  await user.click(screen.getByRole('button', { name: '按耗时宽度' }));
  expect(Number(screen.getByTestId('flame-node-failed-row').getAttribute('width'))).not.toBe(before);
});

test('drills into a stage and retains a textual failed marker', async () => {
  const user = userEvent.setup();
  FoldedFlameHarness();
  await user.click(screen.getByRole('button', { name: /进入 Turn 1/ }));
  expect(screen.getByRole('button', { name: '返回上层' })).toBeEnabled();
  expect(screen.getByText('failed')).toBeInTheDocument();
});
