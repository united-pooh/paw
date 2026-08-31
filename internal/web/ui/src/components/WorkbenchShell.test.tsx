import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkbenchShell } from './WorkbenchShell';

it('switches from conversation summary to trace detail', async () => {
  const user = userEvent.setup();
  render(<WorkbenchShell
    workspaces={[{ id: 'w', name: 'Project', path: '/project', last_opened_at: new Date().toISOString() }]}
    sessions={[{ session_id: 's', title: 'Conversation', created_at: new Date().toISOString(), last_used_at: new Date().toISOString(), transcript_size: 1 }]}
    snapshot={{ session_id: 's', session_version: 1, turns: [], stream_id: 'stream', event_sequence: 0 }}
    parts={{ p: { part_id: 'p', session_id: 's', turn_id: 't', kind: 'reasoning', text: 'details' } }}
  />);
  expect(screen.getByRole('heading', { name: 'Conversation' })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /思考过程/ }));
  expect(screen.getByRole('heading', { name: '运行轨迹' })).toBeInTheDocument();
  expect(screen.getByLabelText('轨迹详情')).toHaveTextContent('details');
});
