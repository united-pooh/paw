import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Composer } from './Composer';

beforeEach(() => localStorage.clear());

it('submits on Enter, keeps Shift+Enter, and does not duplicate pending command IDs', async () => {
  const user = userEvent.setup();
  let resolveSubmit: (() => void) | undefined;
  const calls: Array<{ text: string; id: string }> = [];
  render(<Composer workspaceID="w" sessionID="s" onSubmit={(text, id) => new Promise((resolve) => { calls.push({ text, id }); resolveSubmit = resolve; })} />);
  const textarea = screen.getByLabelText('消息');
  await user.type(textarea, 'hello{shift>}{enter}{/shift}world');
  expect(textarea).toHaveValue('hello\nworld');
  await user.keyboard('{Enter}');
  expect(calls).toHaveLength(1);
  await user.keyboard('{Enter}');
  expect(calls).toHaveLength(1);
  resolveSubmit?.();
  await screen.findByRole('button', { name: '发送' });
  expect(textarea).toHaveValue('');
  expect(localStorage.getItem('paw:draft:w:s')).toBeNull();
});

it('restores drafts from localStorage and blocks when workspace is busy', () => {
  localStorage.setItem('paw:draft:w:s', 'saved');
  render(<Composer workspaceID="w" sessionID="s" busy onSubmit={async () => undefined} />);
  expect(screen.getByLabelText('消息')).toHaveValue('saved');
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled();
});
