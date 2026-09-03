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

it('纯思考的工作段直接平铺内容，不再嵌套「思考过程」折叠框', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [{ type: 'reasoning', reasoning: { text: '先梳理思路' } }] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    onInspect={() => undefined}
  />);
  // 外层工作段卡片存在，摘要为「思考过程」
  expect(document.querySelector('.activity-group')).not.toBeNull();
  // 内部不再有同名的折叠框，内容直接平铺
  expect(document.querySelector('.activity-reasoning')).toBeNull();
  expect(document.querySelector('.activity-reasoning-content')?.textContent).toContain('先梳理思路');
});

it('混合段（思考 + 工具）中的思考块保持可折叠区块', () => {
  render(<ConversationView
    snapshot={snapshotWithTurn({
      messages: [
        { role: 'user', content: '问题' },
        { role: 'assistant', assistant_parts: [
          { type: 'reasoning', reasoning: { text: '想先调用工具' } },
          { type: 'tool_call', tool_call: { id: 'call-1', name: 'Read' } },
        ] },
        { role: 'assistant', content: '答案' },
      ],
    })}
    parts={{}}
    onInspect={() => undefined}
  />);
  expect(document.querySelector('.activity-reasoning')).not.toBeNull();
  expect(document.querySelector('.activity-tool-name')?.textContent).toBe('Read');
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

it('对话模式只展示进行中的思考过程卡，回合结束即消失；轨迹模式展示全部过程卡', () => {
  const parts = {
    p1: { part_id: 'p1', session_id: 's1', turn_id: 't1', kind: 'reasoning', text: '思考中' },
    p2: { part_id: 'p2', session_id: 's1', turn_id: 't1', kind: 'assistant', text: '书写中' },
  };
  // 回合进行中：对话模式只显示推理临时卡，正文过程卡由打字机气泡替代
  const running = snapshotWithTurn({ status: 'running' });
  running.turns[0].messages = [{ role: 'user', content: '你好' }];
  running.active_turn_id = 't1';
  const view = render(<ConversationView snapshot={running} parts={parts} showActivity={false} onInspect={() => undefined} />);
  expect(document.querySelectorAll('.process-card')).toHaveLength(1);
  expect(document.querySelector('.process-card')!.textContent).toContain('思考过程');

  // 回合结束（active 清除）：对话模式不再有任何过程卡
  const finished = snapshotWithTurn({ status: 'completed' });
  view.rerender(<ConversationView snapshot={finished} parts={parts} showActivity={false} onInspect={() => undefined} />);
  expect(document.querySelector('.process-card')).toBeNull();

  // 轨迹模式：始终展示全部流式片段
  view.rerender(<ConversationView snapshot={finished} parts={parts} showActivity onInspect={() => undefined} />);
  expect(document.querySelectorAll('.process-card')).toHaveLength(2);
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
  expect(document.querySelector('.scroll-to-latest')).toBeNull();
});
