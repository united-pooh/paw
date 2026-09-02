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
