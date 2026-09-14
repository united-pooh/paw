import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Composer } from './Composer';

beforeEach(() => localStorage.clear());

// 卡片堆的常用查询（胶囊与模型候选 listbox 在测试中反复出现）
const modelPill = () => screen.getByRole('button', { name: '切换模型' });
const modelListbox = () => within(screen.getByRole('listbox', { name: '模型候选' }));

it('starts with one row and no shortcut hint', () => {
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined} />);
  expect(screen.getByLabelText('消息')).toHaveAttribute('rows', '1');
  expect(screen.queryByText(/Enter 发送/)).toBeNull();
});

describe('composer autosizing', () => {
  beforeEach(() => {
    vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLTextAreaElement) {
      expect(this.style.height).toBe('auto');
      return this.value.split('\n').length * 25 + 11;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('grows with multiline edits and shrinks when text is removed', () => {
    render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined} />);
    const textarea = screen.getByLabelText('消息');
    expect(textarea).toHaveStyle({ height: '36px' });
    fireEvent.change(textarea, { target: { value: 'first\nsecond\nthird' } });
    expect(textarea).toHaveStyle({ height: '86px' });
    fireEvent.change(textarea, { target: { value: 'short' } });
    expect(textarea).toHaveStyle({ height: '36px' });
    fireEvent.change(textarea, { target: { value: '' } });
    expect(textarea).toHaveStyle({ height: '36px' });
  });

  it('resizes restored drafts on mount and when the session changes', () => {
    localStorage.setItem('paw:draft:w:a', 'first\nsecond\nthird');
    localStorage.setItem('paw:draft:w:b', 'short');
    const view = render(<Composer workspaceID="w" sessionID="a" onSubmit={async () => undefined} />);
    expect(screen.getByLabelText('消息')).toHaveStyle({ height: '86px' });
    view.rerender(<Composer workspaceID="w" sessionID="b" onSubmit={async () => undefined} />);
    expect(screen.getByLabelText('消息')).toHaveValue('short');
    expect(screen.getByLabelText('消息')).toHaveStyle({ height: '36px' });
  });

  it('keeps the pending draft expanded and collapses after a successful send', async () => {
    let finish: (() => void) | undefined;
    render(<Composer workspaceID="w" sessionID="s" onSubmit={() => new Promise(resolve => { finish = resolve; })} />);
    const textarea = screen.getByLabelText('消息');
    fireEvent.change(textarea, { target: { value: 'first\nsecond' } });
    fireEvent.keyDown(textarea, { key: 'Enter' });
    expect(textarea).toHaveValue('first\nsecond');
    expect(textarea).toHaveStyle({ height: '61px' });
    await act(async () => finish?.());
    expect(textarea).toHaveValue('');
    expect(textarea).toHaveStyle({ height: '36px' });
  });

  it('remeasures completion replacement without submitting it', async () => {
    const user = userEvent.setup();
    const submit = vi.fn();
    render(<Composer workspaceID="w" sessionID="s" onSubmit={submit}
      loadCompletions={async () => [{ label: '/task' }]} />);
    const textarea = screen.getByLabelText('消息');
    await user.type(textarea, 'first{shift>}{enter}{/shift}/ta');
    await screen.findByRole('option', { name: '/task' });
    await user.keyboard('{Enter}');
    expect(textarea).toHaveValue('first\n/task ');
    expect(textarea).toHaveStyle({ height: '61px' });
    expect(submit).not.toHaveBeenCalled();
  });

  it('observes only width changes and disconnects without moving focus or the cursor', () => {
    let notify: ResizeObserverCallback = () => undefined;
    const disconnect = vi.fn();
    const observe = vi.fn();
    const observer: ResizeObserver = { observe, unobserve: vi.fn(), disconnect };
    const Observer = vi.fn(function (callback: ResizeObserverCallback) {
      notify = callback;
      return observer;
    });
    vi.stubGlobal('ResizeObserver', Observer);
    let height = 36;
    const measure = vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(() => height);
    const view = render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined} />);
    const textarea = screen.getByLabelText<HTMLTextAreaElement>('消息');
    const resized = (width: number) => act(() => notify([{ contentRect: { width } } as ResizeObserverEntry], observer));
    expect(observe).toHaveBeenCalledWith(textarea);
    fireEvent.change(textarea, { target: { value: 'some text' } });
    textarea.focus();
    textarea.setSelectionRange(2, 4);
    resized(500);
    const calls = measure.mock.calls.length;
    height = 86;
    resized(500);
    expect(measure).toHaveBeenCalledTimes(calls);
    resized(250);
    expect(textarea).toHaveStyle({ height: '86px' });
    expect(document.activeElement).toBe(textarea);
    expect([textarea.selectionStart, textarea.selectionEnd]).toEqual([2, 4]);
    expect(Observer).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(disconnect).toHaveBeenCalledTimes(1);
  });
});

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

it('卡片堆加载模型目录，胶囊 morph 成搜索框筛选切换模型与推理强度', async () => {
  const user = userEvent.setup();
  const selections: Array<{ model_id?: string; effort?: string }> = [];
  const options = {
    active_model_id: 'local/alpha',
    models: [
      { id: 'local/alpha', name: 'alpha', provider: 'local', source: 'configured', reasoning_capable: true, effort: 'high' },
      { id: 'local/beta', name: 'beta', provider: 'local', source: 'configured', reasoning_capable: false },
      { id: 'deepseek/deepseek-v4.1-pro', name: 'deepseek-v4.1-pro', provider: 'deepseek', source: 'catalog', reasoning_capable: true },
    ],
    effort_options: ['default', 'low', 'medium', 'high', 'xhigh', 'max'],
  };
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined}
    loadModelOptions={async () => options}
    onSelectModel={async (selection) => {
      selections.push(selection);
      if (selection.model_id) return { ...options, active_model_id: selection.model_id };
      return options;
    }} />);
  // 卡片堆出现，peek 摘要显示当前模型与强度；胶囊显示当前模型 ID
  const pill = await screen.findByRole('button', { name: '切换模型' });
  expect(screen.getByText('alpha · 高')).toBeInTheDocument();
  expect(pill).toHaveTextContent('local/alpha');
  expect(pill).toHaveAttribute('aria-expanded', 'false');
  // 推理强度反映当前模型的 effort，且 reasoning_capable 时可选
  expect(screen.getByLabelText('推理强度')).toHaveValue('high');

  // 点击胶囊 → morph 成搜索框，候选列表出现（查询限定在 listbox 内，避开原生 select 的 option）
  await user.click(pill);
  expect(await screen.findByLabelText('搜索模型')).toBeInTheDocument();
  expect(modelPill()).toHaveAttribute('aria-expanded', 'true');
  expect(modelListbox().getAllByRole('option')).toHaveLength(3);

  // 输入筛选：只剩 beta
  await user.type(screen.getByLabelText('搜索模型'), 'beta');
  expect(modelListbox().getAllByRole('option')).toHaveLength(1);
  // Enter 选定
  await user.keyboard('{Enter}');
  expect(selections).toEqual([{ model_id: 'local/beta' }]);
  // 胶囊文案更新；选择器延迟 140ms 收起（让打勾动画可见），用 waitFor 等落定
  expect(await screen.findByRole('button', { name: '切换模型' })).toHaveTextContent('local/beta');
  await waitFor(() => expect(modelPill()).toHaveAttribute('aria-expanded', 'false'));
  // beta 不支持推理 → 推理强度选择器禁用
  expect(screen.getByLabelText('推理强度')).toBeDisabled();

  // 再次打开，用鼠标点击切回 alpha
  await user.click(modelPill());
  await user.click(modelListbox().getByRole('option', { name: 'local/alpha' }));
  expect(selections).toContainEqual({ model_id: 'local/alpha' });
  expect(await screen.findByRole('button', { name: '切换模型' })).toHaveTextContent('local/alpha');

  // 调整推理强度（原生 select 保留）
  await user.selectOptions(screen.getByLabelText('推理强度'), 'max');
  expect(selections).toContainEqual({ effort: 'max' });
});

it('模型搜索：Esc 先清空再关闭，空结果显示占位', async () => {
  const user = userEvent.setup();
  const options = {
    active_model_id: 'local/alpha',
    models: [{ id: 'local/alpha', name: 'alpha', provider: 'local', source: 'configured', reasoning_capable: true }],
    effort_options: ['default', 'high'],
  };
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined}
    loadModelOptions={async () => options} onSelectModel={async () => options} />);
  await user.click(await screen.findByRole('button', { name: '切换模型' }));
  await user.type(screen.getByLabelText('搜索模型'), 'zzz');
  expect(modelListbox().queryByRole('option')).toBeNull();
  expect(screen.getByText('没有匹配「zzz」的模型')).toBeInTheDocument();
  // 第一次 Esc 清空输入，列表恢复；第二次 Esc 关闭
  await user.keyboard('{Escape}');
  expect(screen.getByLabelText('搜索模型')).toHaveValue('');
  expect(modelListbox().getAllByRole('option')).toHaveLength(1);
  await user.keyboard('{Escape}');
  // 输入框不从 DOM 卸载（同体 morph），关闭后胶囊回到未展开态、搜索框不可见
  await waitFor(() => expect(modelPill()).toHaveAttribute('aria-expanded', 'false'));
  expect(modelPill()).not.toHaveClass('searching');
});

it('未提供模型数据源时不渲染卡片堆', () => {
  render(<Composer workspaceID="w" sessionID="s" onSubmit={async () => undefined} />);
  expect(screen.queryByLabelText('切换模型')).toBeNull();
  expect(screen.queryByLabelText('推理强度')).toBeNull();
});
