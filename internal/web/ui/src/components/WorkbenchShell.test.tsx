import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkbenchShell } from './WorkbenchShell';

const snapshotWithActivity = {
  session_id: 's',
  session_version: 1,
  stream_id: 'stream',
  event_sequence: 0,
  turns: [{
    turn_id: 't',
    messages: [
      { role: 'user', content: '读一下这篇文章' },
      {
        role: 'assistant',
        content: '',
        assistant_parts: [
          { type: 'reasoning' as const, reasoning: { text: '先抓取页面' } },
          { type: 'tool_call' as const, tool_call: { id: 'c1', name: 'WebFetch', input: { url: 'https://arxiv.org/abs/2601.09361' } } },
        ],
      },
      { role: 'user', tool_results: [{ tool_use_id: 'c1', content: 'page body' }] },
      { role: 'assistant', content: '这是文章摘要。' },
    ],
  }],
};

it('对话与轨迹共用内容流，仅工作段随标签切换显示', async () => {
  const user = userEvent.setup();
  const { container } = render(<WorkbenchShell
    workspaces={[{ id: 'w', name: 'Project', path: '/project', last_opened_at: new Date().toISOString() }]}
    sessions={[{ session_id: 's', title: 'Conversation', created_at: new Date().toISOString(), last_used_at: new Date().toISOString(), transcript_size: 1 }]}
    snapshot={snapshotWithActivity}
    parts={{}}
  />);
  expect(screen.getByRole('heading', { name: 'Conversation' })).toBeInTheDocument();
  // 两个标签渲染同一内容流：正文始终可见
  expect(screen.getByText('这是文章摘要。')).toBeInTheDocument();
  // 对话标签下工作段挂载但处于收起外壳中
  expect(container.querySelector('.activity-shell')).not.toBeNull();
  expect(container.querySelector('.activity-shell.open')).toBeNull();
  // 切换到轨迹标签：工作段外壳展开
  await user.click(screen.getByRole('button', { name: '轨迹' }));
  expect(container.querySelector('.activity-shell.open')).not.toBeNull();
  expect(screen.getByText(/执行了 1 项操作/)).toBeInTheDocument();
  // 切回对话：工作段重新收起
  await user.click(screen.getByRole('button', { name: '对话' }));
  expect(container.querySelector('.activity-shell.open')).toBeNull();
  expect(screen.getByText('这是文章摘要。')).toBeInTheDocument();
});

it('会话面板可折叠为纯图标导轨并恢复', async () => {
  const user = userEvent.setup();
  const { container } = render(<WorkbenchShell
    workspaces={[{ id: 'w', name: 'Project', path: '/project', last_opened_at: new Date().toISOString() }]}
    sessions={[{ session_id: 's', title: 'Conversation', created_at: new Date().toISOString(), last_used_at: new Date().toISOString(), transcript_size: 1 }]}
    snapshot={snapshotWithActivity}
    parts={{}}
  />);
  // 工作区在导轨上渲染为字母头像
  expect(screen.getByRole('button', { name: '工作区 Project' })).toBeInTheDocument();
  expect(container.querySelector('.workbench-shell.panel-collapsed')).toBeNull();
  // 面板头部收起按钮 → 折叠为纯导轨
  await user.click(screen.getByRole('button', { name: '收起会话面板' }));
  expect(container.querySelector('.workbench-shell.panel-collapsed')).not.toBeNull();
  // 顶栏切换按钮 → 恢复面板
  await user.click(screen.getByRole('button', { name: '切换会话面板' }));
  expect(container.querySelector('.workbench-shell.panel-collapsed')).toBeNull();
});

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
  // 点击流式过程卡片后切到轨迹标签，并打开详情抽屉
  expect(screen.getByRole('button', { name: '轨迹' })).toHaveClass('active');
  expect(screen.getByLabelText('轨迹详情')).toHaveTextContent('details');
});
