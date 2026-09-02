import { render, screen } from '@testing-library/react';
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

it('过程卡只出现在轨迹模式，对话模式不渲染', () => {
  const parts = {
    p1: { part_id: 'p1', session_id: 's1', turn_id: 't1', kind: 'reasoning', text: '思考中' },
    p2: { part_id: 'p2', session_id: 's1', turn_id: 't1', kind: 'assistant', text: '书写中' },
  };
  const view = render(<ConversationView snapshot={snapshotWithTurn({})} parts={parts} showActivity={false} onInspect={() => undefined} />);
  expect(document.querySelector('.process-card')).toBeNull();
  view.rerender(<ConversationView snapshot={snapshotWithTurn({})} parts={parts} showActivity onInspect={() => undefined} />);
  expect(document.querySelectorAll('.process-card')).toHaveLength(2);
});
