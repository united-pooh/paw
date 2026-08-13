import { render, screen } from '@testing-library/react';
import { VirtualList } from './VirtualList';

test('renders only the visible 24px rows out of 2000 items', () => {
  const items = Array.from({ length: 2000 }, (_, index) => ({ id: String(index) }));
  render(
    <VirtualList
      items={items}
      rowHeight={24}
      height={240}
      overscan={4}
      getKey={(item) => item.id}
      renderRow={(item) => <div role="row">{item.id}</div>}
    />,
  );
  expect(screen.getAllByRole('row').length).toBeLessThanOrEqual(19);
});
