import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { vi } from 'vitest';
import type { SessionSnapshot } from '../../api/types';
import { ConversationView } from './ConversationView';

function snapshotWithTurn(turn: Partial<SessionSnapshot['turns'][number]>): SessionSnapshot {
  return {
    session_id: 's1',
    session_version: 1,
    stream_id: 'stream',
    event_sequence: 0,
    turns: [{
      turn_id: 't1',
      messages: [
        { role: 'user', content: '你好' },
        { role: 'assistant', content: '你好，有什么可以帮你？' },
      ],
      ...turn,
    }],
  };
}

it('完成的回合在 assistant 页脚展示时间、token 与 tok/s，用户消息只展示时间', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      status: 'completed',
      started_at: '2026-09-02T14:30:00Z',
      duration_ms: 10_000,
      input_tokens: 1234,
      output_tokens: 500,
    })}
    parts={{}}
    onInspect={() => undefined}
  />);
  const assistant = screen.getByText('你好，有什么可以帮你？').closest('article')!;
  const meta = assistant.querySelector('.message-meta')!;
  expect(meta.textContent).toContain('1.2k');
  expect(meta.textContent).toContain('↓500');
  expect(meta.textContent).toContain('50.0 tok/s');
  expect(meta.textContent).toContain('10s');

  const user = screen.getByText('你好').closest('article')!;
  const userMeta = user.querySelector('.message-meta')!;
  expect(userMeta.textContent).not.toContain('tok/s');
  expect(userMeta.textContent).not.toContain('↑');

  // 时间戳页脚在气泡之外
  const bubble = user.querySelector('.user-bubble')!;
  expect(bubble).not.toBeNull();
  expect(bubble.querySelector('.message-meta')).toBeNull();
});

it('进行中的回合显示生成中而不是 token 统计', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({ status: 'running', started_at: '2026-09-02T14:30:00Z', input_tokens: 100, output_tokens: 50 })}
    parts={{}}
    onInspect={() => undefined}
  />);
  const assistant = screen.getByText('你好，有什么可以帮你？').closest('article')!;
  expect(assistant.querySelector('.message-meta')!.textContent).toContain('生成中');
  expect(assistant.querySelector('.message-meta')!.textContent).not.toContain('tok/s');
});

it('缺少时间与用量时不渲染页脚', () => {
  render(<ConversationView snapshot={snapshotWithTurn({ status: 'completed' })} parts={{}} onInspect={() => undefined} />);
  expect(document.querySelector('.message-meta')).toBeNull();
});

it('工作段以竖轨穿插在对话流中：思考节点默认折叠，内容就地展开', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [{ type: 'reasoning', reasoning: { text: '先梳理思路' } }] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    showActivity={false}
    onInspect={() => undefined}
  />);
  // 竖轨存在且为思考节点（紫色），对话模式下默认折叠
  const rail = document.querySelector('.activity-rail')!;
  expect(rail).not.toBeNull();
  const thinkNode = rail.querySelector<HTMLDetailsElement>('.rail-node.think')!;
  expect(thinkNode).not.toBeNull();
  expect(thinkNode.open).toBe(false);
  // 思考内容保留在 DOM 中，展开即可见
  expect(rail.querySelector('.rail-think-content')?.textContent).toContain('先梳理思路');
});

it('同一段的多段思考合并为一个思考节点', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [{ type: 'reasoning', reasoning: { text: '第一段' } }] },
        { role: 'assistant', assistant_parts: [{ type: 'reasoning', reasoning: { text: '第二段' } }] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    onInspect={() => undefined}
  />);
  expect(document.querySelectorAll('.rail-node.think')).toHaveLength(1);
  const content = document.querySelector('.rail-think-content')!;
  expect(content.textContent).toContain('第一段');
  expect(content.textContent).toContain('第二段');
});

it('混合段：思考与工具各占一个节点，工具结果收纳在可展开的行内', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [
          { type: 'reasoning', reasoning: { text: '想先调用工具' } },
          { type: 'tool_call', tool_call: { id: 'call-1', name: 'Read', input: { file_path: 'a.go' } } },
        ], tool_results: [{ tool_use_id: 'call-1', content: '42 lines' }] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    showActivity={false}
    onInspect={() => undefined}
  />);
  const rail = document.querySelector('.activity-rail')!;
  expect(rail.querySelector('.rail-node.think')).not.toBeNull();
  const toolNode = rail.querySelector<HTMLDetailsElement>('.rail-node.tool')!;
  expect(toolNode.querySelector('.rail-label')?.textContent).toBe('1 项操作');
  expect(toolNode.open).toBe(false);
  // 工具行：名称 + 目标 + 结果内容（折叠在行内 details 里）
  const row = toolNode.querySelector<HTMLDetailsElement>('.rail-tool')!;
  expect(row.querySelector('.rail-tool-name')?.textContent).toBe('Read');
  expect(row.querySelector('.rail-tool-target')?.textContent).toBe('a.go');
  expect(row.querySelector('.rail-pre')?.textContent).toBe('42 lines');
  expect(row.open).toBe(false);
});

it('含失败工具的段：工具节点变红并自动展开，失败行就地展示错误内容', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [
          { type: 'tool_call', tool_call: { id: 'call-1', name: 'Bash', input: { command: 'vitest run' } } },
        ], tool_results: [{ tool_use_id: 'call-1', content: 'FAIL 1 test', is_error: true }] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    onInspect={() => undefined}
  />);
  const toolNode = document.querySelector<HTMLDetailsElement>('.rail-node.tool')!;
  expect(toolNode.classList.contains('error')).toBe(true);
  expect(toolNode.open).toBe(true);
  expect(toolNode.querySelector('.rail-label')?.textContent).toContain('1 项失败');
  // 失败行同样自动展开，错误内容直接可见
  const row = toolNode.querySelector<HTMLDetailsElement>('.rail-tool.error')!;
  expect(row.open).toBe(true);
  expect(row.querySelector('.rail-pre')?.textContent).toBe('FAIL 1 test');
});

it('轨迹模式（showActivity）下竖轨节点默认全部展开', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [
          { type: 'reasoning', reasoning: { text: '思路' } },
          { type: 'tool_call', tool_call: { id: 'call-1', name: 'Read' } },
        ] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    showActivity
    onInspect={() => undefined}
  />);
  const nodes = document.querySelectorAll<HTMLDetailsElement>('.activity-rail .rail-node');
  expect(nodes).toHaveLength(2);
  nodes.forEach((node) => expect(node.open).toBe(true));
});

it('流式正文即时渲染标记 live，快照正文到达后由快照接管', async () => {
  const snapshot = snapshotWithTurn({ status: 'running', started_at: '2026-09-02T14:30:00Z' });
  snapshot.turns[0].messages = [{ role: 'user', content: '你好' }];
  snapshot.active_turn_id = 't1';
  const parts = {
    p1: { part_id: 'p1', session_id: 's1', turn_id: 't1', kind: 'assistant', text: '正在输' },
  };
  const view = render(<ConversationView snapshot={snapshot} parts={parts} showActivity={false} onInspect={() => undefined} />);
  // delta 流式进行中：正文经打字水位逐渐显示且标 live
  const live = document.querySelector('article.message.assistant.live');
  expect(live).not.toBeNull();
  expect(live!.textContent).toContain('生成中');
  await waitFor(() => expect(document.querySelector('article.message.assistant.live')?.textContent).toContain('正在输'));

  // 回合完成后的快照包含正文：泵追平期间由流式气泡续播，追平后快照接管（无重复）
  const completed = snapshotWithTurn({ status: 'completed' });
  view.rerender(<ConversationView snapshot={completed} parts={parts} showActivity={false} onInspect={() => undefined} />);
  expect(document.querySelectorAll('article.message.assistant')).toHaveLength(1);
  await waitFor(() => expect(screen.getByText('你好，有什么可以帮你？').closest('article')).toBeInTheDocument());
  expect(document.querySelector('article.message.assistant.stream-typing')).toBeNull();
});

it('消息操作条：复制正文，assistant 消息支持分叉与导出', async () => {
  const user = userEvent.setup();
  const spy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
  const forks: string[] = [];
  render(<ConversationView snapshot={snapshotWithTurn({})} parts={{}} onInspect={() => undefined}
    onFork={() => forks.push('fork')} exportUrl="/api/workspaces/w/sessions/s1/export" />);

  // 用户与 assistant 消息都可复制
  const copyButtons = screen.getAllByLabelText('复制');
  expect(copyButtons).toHaveLength(2);
  await user.click(copyButtons[1]);
  expect(spy).toHaveBeenCalledWith('你好，有什么可以帮你？');
  await user.click(copyButtons[0]);
  expect(spy).toHaveBeenCalledWith('你好');
  expect(screen.getAllByLabelText('已复制').length).toBeGreaterThan(0);

  // 分叉回调与导出链接（只在 assistant 消息上）
  const forkButtons = screen.getAllByLabelText('分叉当前会话');
  expect(forkButtons).toHaveLength(1);
  await user.click(forkButtons[0]);
  expect(forks).toEqual(['fork']);
  expect(screen.getByLabelText('导出会话')).toHaveAttribute('href', '/api/workspaces/w/sessions/s1/export');
  spy.mockRestore();
});

it('assistant 页脚操作条在前、元信息紧随其后，同行左排', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      status: 'completed',
      started_at: '2026-09-02T14:30:00Z',
      duration_ms: 10_000,
      input_tokens: 100,
      output_tokens: 50,
    })}
    parts={{}}
    onInspect={() => undefined}
    onFork={() => undefined}
  />);
  const assistant = screen.getByText('你好，有什么可以帮你？').closest('article')!;
  const footer = assistant.querySelector('.message-footer')!;
  expect(footer.firstElementChild).toHaveClass('message-actions');
  expect(footer.lastElementChild).toHaveClass('message-meta');
});

it('对话模式：进行中回合以实时竖轨呈现思考与工具呼吸节点，回合结束即消失；轨迹模式保留过程卡', () => {
  const parts = {
    p1: { part_id: 'p1', session_id: 's1', turn_id: 't1', kind: 'reasoning', text: '梳理思路中' },
    p2: { part_id: 'p2', session_id: 's1', turn_id: 't1', kind: 'assistant', text: '书写中' },
  };
  const tools = {
    c1: { tool_use_id: 'c1', turn_id: 't1', name: 'Read', target: 'a.go', status: 'running' as const },
  };
  // 回合进行中：对话模式显示实时竖轨（思考中 + 运行中的工具行），不再有临时过程卡
  const running = snapshotWithTurn({ status: 'running' });
  running.turns[0].messages = [{ role: 'user', content: '你好' }];
  running.active_turn_id = 't1';
  const view = render(<ConversationView snapshot={running} parts={parts} tools={tools} showActivity={false} onInspect={() => undefined} />);
  const live = document.querySelector('.activity-rail.live')!;
  expect(live).not.toBeNull();
  expect(live.querySelector('.rail-think-content')?.textContent).toContain('梳理思路中');
  expect(live.querySelector('.rail-tool-name')?.textContent).toBe('Read');
  expect(live.querySelector('.rail-dot.pending')).not.toBeNull();
  expect(document.querySelector('.process-card')).toBeNull();

  // 回合结束（active 清除）：实时竖轨消失，由快照里的静息竖轨接管
  const finished = snapshotWithTurn({ status: 'completed' });
  view.rerender(<ConversationView snapshot={finished} parts={parts} tools={tools} showActivity={false} onInspect={() => undefined} />);
  expect(document.querySelector('.activity-rail.live')).toBeNull();

  // 轨迹模式：始终展示全部流式片段过程卡
  view.rerender(<ConversationView snapshot={finished} parts={parts} tools={tools} showActivity onInspect={() => undefined} />);
  expect(document.querySelectorAll('.process-card')).toHaveLength(2);
});

it('实时竖轨只展示当前回合的工具，其它回合的历史工具不混入', () => {
  const running = snapshotWithTurn({ status: 'running' });
  running.turns[0].messages = [{ role: 'user', content: '你好' }];
  running.active_turn_id = 't1';
  const tools = {
    c1: { tool_use_id: 'c1', turn_id: 't0', name: 'Read', status: 'completed' as const },
    c2: { tool_use_id: 'c2', turn_id: 't1', name: 'Bash', status: 'running' as const },
  };
  render(<ConversationView snapshot={running} parts={{}} tools={tools} showActivity={false} onInspect={() => undefined} />);
  const live = document.querySelector('.activity-rail.live')!;
  expect(live).not.toBeNull();
  expect(live.querySelectorAll('.rail-tool')).toHaveLength(1);
  expect(live.querySelector('.rail-tool-name')?.textContent).toBe('Bash');
});

/** jsdom 无量布局：手动指定滚动几何并触发 scroll 事件 */
function mockScroll(el: Element, scrollTop: number) {
  Object.defineProperties(el, {
    scrollHeight: { value: 2000, configurable: true },
    clientHeight: { value: 500, configurable: true },
  });
  (el as HTMLElement).scrollTop = scrollTop;
  fireEvent.scroll(el);
}

it('separates the full scroll viewport from centered content and offers return without unread', () => {
  render(<ConversationView snapshot={snapshotWithTurn({})} parts={{}} onInspect={() => undefined} />);
  expect(document.querySelector('.conversation-view > .conversation-content')).not.toBeNull();
  mockScroll(document.querySelector('.conversation-view')!, 100);
  fireEvent.click(screen.getByRole('button', { name: '↓ 回到最新' }));
  expect(document.querySelector('.scroll-to-latest')).toBeNull();
});

it('快照晚到时滚动监听仍正确绑定（首帧 empty-state 不丢监听器）', () => {
  const view = render(<ConversationView snapshot={null} parts={{}} onInspect={() => undefined} />);
  expect(document.querySelector('.empty-state')).not.toBeNull();
  const snapshot = snapshotWithTurn({ status: 'completed' });
  view.rerender(<ConversationView snapshot={snapshot} parts={{}} onInspect={() => undefined} />);
  const scrollEl = document.querySelector('.conversation-view')!;
  // 上翻脱离后跟随后续新消息应出现浮钮
  mockScroll(scrollEl, 0);
  const withTwo = snapshotWithTurn({ status: 'completed' });
  withTwo.turns.push({
    turn_id: 't2',
    messages: [
      { role: 'user', content: '再来一条' },
      { role: 'assistant', content: '之回复' },
    ],
  });
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} />);
  expect(screen.getByText(/↓ 1 条新消息/)).toBeInTheDocument();
});

it('上翻脱离跟随后出现「↓ N 条新消息」，点击回底并清零；自行滚回同样清除', () => {
  const snapshot = snapshotWithTurn({ status: 'completed' });
  const view = render(<ConversationView snapshot={snapshot} parts={{}} onInspect={() => undefined} />);
  const scrollEl = document.querySelector('.conversation-view')!;
  expect(document.querySelector('.scroll-to-latest')).toBeNull();

  // 用户上翻：远离底部 → 脱离跟随
  mockScroll(scrollEl, 0);

  // 新增一个 assistant 消息 → 浮现「↓ 1 条新消息」
  const withTwo = snapshotWithTurn({ status: 'completed' });
  withTwo.turns.push({
    turn_id: 't2',
    messages: [
      { role: 'user', content: '第二个问题' },
      { role: 'assistant', content: '第二个回答' },
    ],
  });
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} />);
  expect(screen.getByText(/↓ 1 条新消息/)).toBeInTheDocument();

  // 再去一条 → 计数累加
  withTwo.turns.push({
    turn_id: 't3',
    messages: [
      { role: 'user', content: '第三个问题' },
      { role: 'assistant', content: '第三个回答' },
    ],
  });
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} />);
  expect(screen.getByText(/↓ 2 条新消息/)).toBeInTheDocument();

  // 点击浮钮：回到 scrollBottom、跟随恢复、未读清零
  fireEvent.click(screen.getByRole('button', { name: /条新消息/ }));
  expect((scrollEl as HTMLElement).scrollTop).toBe(2000);
  expect(document.querySelector('.scroll-to-latest')).toBeNull();

  // 后续渲染继续保持贴底跟随
  mockScroll(scrollEl, 1500);
  expect(document.querySelector('.scroll-to-latest')).toBeNull();

  // 手动滚回底部同样清除未读
  const withFour = snapshotWithTurn({ status: 'completed' });
  withFour.turns.push(
    { turn_id: 't2', messages: [{ role: 'user', content: '二' }, { role: 'assistant', content: '答二' }] },
  );
  view.rerender(<ConversationView snapshot={withFour} parts={{}} onInspect={() => undefined} />);
  // 新周期：先离开底部再来消息
  mockScroll(scrollEl, 100);
  const withFive = snapshotWithTurn({ status: 'completed' });
  withFive.turns.push(
    { turn_id: 't2', messages: [{ role: 'user', content: '二' }, { role: 'assistant', content: '答二' }] },
    { turn_id: 't3', messages: [{ role: 'user', content: '三' }, { role: 'assistant', content: '答三' }] },
  );
  view.rerender(<ConversationView snapshot={withFive} parts={{}} onInspect={() => undefined} />);
  expect(screen.getByText(/条新消息/)).toBeInTheDocument();
  mockScroll(scrollEl, 1500); // 滚到底部
  expect(document.querySelector('.scroll-to-latest')).toBeNull();
});

it('发送消息时强制回到底部：即使此前上翻脱离也恢复跟随并清未读（TUI submit 回底同款契约）', () => {
  const snapshot = snapshotWithTurn({ status: 'completed' });
  const view = render(<ConversationView snapshot={snapshot} parts={{}} onInspect={() => undefined} sendSignal={0} />);
  const scrollEl = document.querySelector('.conversation-view')!;

  // 上翻脱离跟随 → 新消息产生未读
  mockScroll(scrollEl, 0);
  const withTwo = snapshotWithTurn({ status: 'completed' });
  withTwo.turns.push({
    turn_id: 't2',
    messages: [
      { role: 'user', content: '第二个问题' },
      { role: 'assistant', content: '第二个回答' },
    ],
  });
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} sendSignal={0} />);
  expect(screen.getByText(/↓ 1 条新消息/)).toBeInTheDocument();
  expect((scrollEl as HTMLElement).scrollTop).toBe(0);

  // 用户在 Composer 提交消息（WorkbenchShell 递增 sendSignal）：立即回底、未读清零
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} sendSignal={1} />);
  expect((scrollEl as HTMLElement).scrollTop).toBe(2000);
  expect(document.querySelector('.scroll-to-latest')).toBeNull();

  // 后续 assistant 消息到达：跟随已恢复，不再产生未读
  withTwo.turns.push({
    turn_id: 't3',
    messages: [
      { role: 'user', content: '第三个问题' },
      { role: 'assistant', content: '第三个回答' },
    ],
  });
  view.rerender(<ConversationView snapshot={withTwo} parts={{}} onInspect={() => undefined} sendSignal={1} />);
  expect((scrollEl as HTMLElement).scrollTop).toBe(2000);
  expect(document.querySelector('.scroll-to-latest')).toBeNull();
});

it('sendSignal 未变化时普通重渲染不强制回底（尊重上翻脱离）', () => {
  const snapshot = snapshotWithTurn({ status: 'completed' });
  const view = render(<ConversationView snapshot={snapshot} parts={{}} onInspect={() => undefined} sendSignal={0} />);
  const scrollEl = document.querySelector('.conversation-view')!;
  // 上翻脱离
  mockScroll(scrollEl, 100);
  // 普通重渲染（快照对象刷新但内容条数不变）：不得强制回底
  view.rerender(<ConversationView snapshot={snapshotWithTurn({ status: 'completed' })} parts={{}} onInspect={() => undefined} sendSignal={0} />);
  expect((scrollEl as HTMLElement).scrollTop).toBe(100);
  expect(screen.getByRole('button', { name: '↓ 回到最新' })).toBeInTheDocument();
});

it('navigates to a stable user anchor and keeps manual reading during updates', async () => {
  const snapshot = snapshotWithTurn({});
  snapshot.turns.push({ turn_id: 't2', messages: [{ role: 'user', content: '第二问' }, { role: 'assistant', content: '第二答' }] });
  const view = render(<ConversationView snapshot={snapshot} parts={{}} onInspect={() => undefined} />);
  const viewport = document.querySelector('.conversation-view')!;
  mockScroll(viewport, 100);
  const anchor = document.querySelector<HTMLElement>('[data-turn-id="t2"][data-turn-question]');
  expect(anchor).not.toBeNull();
  vi.spyOn(viewport, 'getBoundingClientRect').mockReturnValue({ top: 50 } as DOMRect);
  vi.spyOn(anchor!, 'getBoundingClientRect').mockReturnValue({ top: 474 } as DOMRect);
  fireEvent.click(screen.getByRole('button', { name: '第 2 轮：第二问' }));
  expect(viewport.scrollTop).toBe(500);
  expect(anchor).toHaveFocus();
  view.rerender(<ConversationView snapshot={{ ...snapshot }} parts={{}} onInspect={() => undefined} />);
  expect(viewport.scrollTop).toBe(500);
  expect(document.querySelector('[data-turn-id="t2"][data-turn-question]')).toBe(anchor);
});

it('keeps one navigation item and the question anchor across streaming handoff', async () => {
  const snapshot = snapshotWithTurn({ status: 'running', messages: [{ role: 'user', content: '你好' }] });
  snapshot.active_turn_id = 't1';
  const parts = { p: { part_id: 'p', session_id: 's1', turn_id: 't1', kind: 'assistant', text: '流式答案' } };
  const view = render(<ConversationView snapshot={snapshot} parts={parts} showActivity={false} onInspect={() => undefined} />);
  const anchor = document.querySelector('[data-turn-id="t1"][data-turn-question]');
  expect(anchor).not.toBeNull();
  await waitFor(() => expect(document.querySelector('.message.assistant')).toHaveTextContent('流式答案'));
  view.rerender(<ConversationView snapshot={snapshotWithTurn({ status: 'completed' })} parts={parts} showActivity={false} onInspect={() => undefined} />);
  await waitFor(() => expect(document.querySelector('.message.assistant')).toHaveTextContent('你好，有什么可以帮你？'));
  expect(document.querySelector('[data-turn-id="t1"][data-turn-question]')).toBe(anchor);
  expect(screen.getAllByRole('button', { name: '第 1 轮：你好' })).toHaveLength(1);
});
