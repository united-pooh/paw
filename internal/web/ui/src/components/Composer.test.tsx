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

it('reloads the scoped draft when the session changes', async () => {
  localStorage.setItem('paw:draft:w:a', 'draft-a');
  localStorage.setItem('paw:draft:w:b', 'draft-b');
  const view = render(<Composer workspaceID="w" sessionID="a" onSubmit={async () => undefined} />);
  expect(screen.getByLabelText('消息')).toHaveValue('draft-a');
  view.rerender(<Composer workspaceID="w" sessionID="b" onSubmit={async () => undefined} />);
  expect(await screen.findByDisplayValue('draft-b')).toBeInTheDocument();
});

it('steers by default, can queue, and cancels an active turn without losing draft', async () => {
  const user = userEvent.setup();
  const actions: string[] = [];
  render(<Composer workspaceID="w" sessionID="s" activeTurnID="turn" queueCount={1}
    onSubmit={async () => undefined}
    onSteer={async (text) => { actions.push(`steer:${text}`); }}
    onQueue={async (text) => { actions.push(`queue:${text}`); }}
    onCancel={async () => { actions.push('cancel'); }} />);
  const textarea = screen.getByLabelText('消息');
  await user.type(textarea, 'adjust{Enter}');
  expect(actions).toContain('steer:adjust');
  await user.type(textarea, 'later');
  await user.click(screen.getByRole('button', { name: '排队' }));
  await user.keyboard('{Enter}');
  expect(actions).toContain('queue:later');
  await user.type(textarea, 'draft');
  await user.click(screen.getByRole('button', { name: '停止' }));
  expect(actions).toContain('cancel');
  expect(textarea).toHaveValue('draft');
  expect(screen.getByRole('status')).toHaveTextContent('已排队 1 条消息');
});

it('输入 @ 展示文件候补，Enter 确认、目录可下钻、Escape 关闭', async () => {
  const user = userEvent.setup();
  const queries: string[] = [];
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined}
    loadCompletions={async (trigger, query) => {
      queries.push(`${trigger}${query}`);
      if (trigger !== '@') return [];
      if (query.startsWith('docs/')) return [{ label: 'guide.md' }];
      return [{ label: 'README.md' }, { label: 'docs/', dir: true }];
    }} />);
  const textarea = screen.getByLabelText('消息');
  await user.type(textarea, '@read');
  // 候补出现，默认选中第一项，Enter 写回并追加空格
  expect(await screen.findByRole('option', { name: /README\.md/ })).toBeInTheDocument();
  await user.keyboard('{Enter}');
  expect(textarea).toHaveValue('@README.md ');

  // 目录候选：选中后保留下钻，继续加载目录内文件
  await user.type(textarea, '@do');
  expect(await screen.findByRole('option', { name: /docs\// })).toBeInTheDocument();
  await user.keyboard('{ArrowDown}{Enter}');
  expect(textarea).toHaveValue('@README.md @docs/');
  expect(await screen.findByRole('option', { name: /guide\.md/ })).toBeInTheDocument();
  expect(queries).toContain('@docs/');

  // Escape 关闭弹窗，Enter 恢复正常提交
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('listbox')).toBeNull();
});

it('斜杠指令候补与提交互不干扰', async () => {
  const user = userEvent.setup();
  const calls: string[] = [];
  render(<Composer workspaceID="w" sessionID="s"
    onSubmit={async (text) => { calls.push(text); }}
    loadCompletions={async (trigger) => trigger === '/' ? [{ label: '/task', detail: '派发子任务' }] : []} />);
  const textarea = screen.getByLabelText('消息');
  await user.type(textarea, '/ta');
  expect(await screen.findByRole('option', { name: /\/task/ })).toBeInTheDocument();
  await user.keyboard('{Enter}');
  expect(textarea).toHaveValue('/task ');
  // 弹窗已关闭，此时 Enter 正常发送
  await user.keyboard('{Enter}');
  expect(calls).toEqual(['/task']);
});

it('does not silently fall back when a running action callback is unavailable', async () => {
  const user = userEvent.setup();
  const actions: string[] = [];
  render(<Composer workspaceID="w" sessionID="s" activeTurnID="turn"
    onSubmit={async () => { actions.push('submit'); }}
    onSteer={async () => { actions.push('steer'); }} />);
  expect(screen.getByRole('button', { name: '排队' })).toBeDisabled();
  await user.type(screen.getByLabelText('消息'), 'adjust{Enter}');
  expect(actions).toEqual(['steer']);
});

it('卡片堆加载模型目录，切换模型与推理强度', async () => {
  const user = userEvent.setup();
  const selections: Array<{ model_id?: string; effort?: string }> = [];
  const options = {
    active_model_id: 'local/alpha',
    models: [
      { id: 'local/alpha', name: 'alpha', provider: 'local', source: 'configured', reasoning_capable: true, effort: 'high' },
      { id: 'local/beta', name: 'beta', provider: 'local', source: 'configured', reasoning_capable: false },
    ],
    effort_options: ['default', 'low', 'medium', 'high', 'max'],
  };
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined}
    loadModelOptions={async () => options}
    onSelectModel={async (selection) => {
      selections.push(selection);
      if (selection.model_id) return { ...options, active_model_id: selection.model_id };
      return options;
    }} />);
  // 卡片堆出现，peek 摘要显示当前模型与强度
  const modelSelect = await screen.findByLabelText('切换模型');
  expect(screen.getByText('alpha · 高')).toBeInTheDocument();
  expect(modelSelect).toHaveValue('local/alpha');
  // 推理强度反映当前模型的 effort，且 reasoning_capable 时可选
  expect(screen.getByLabelText('推理强度')).toHaveValue('high');
  // 切换模型
  await user.selectOptions(modelSelect, 'local/beta');
  expect(selections).toEqual([{ model_id: 'local/beta' }]);
  expect(await screen.findByLabelText('切换模型')).toHaveValue('local/beta');
  // beta 不支持推理 → 推理强度选择器禁用
  expect(screen.getByLabelText('推理强度')).toBeDisabled();
  // 调整推理强度
  await user.selectOptions(screen.getByLabelText('切换模型'), 'local/alpha');
  await user.selectOptions(screen.getByLabelText('推理强度'), 'max');
  expect(selections).toContainEqual({ effort: 'max' });
});

it('未提供模型数据源时不渲染卡片堆', () => {
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined} />);
  expect(screen.queryByLabelText('切换模型')).toBeNull();
  expect(screen.queryByLabelText('推理强度')).toBeNull();
});
