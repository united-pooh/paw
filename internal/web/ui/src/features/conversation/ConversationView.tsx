import type { SessionSnapshot, StreamingPart } from '../../api/types';
import { MarkdownContent } from '../../components/MarkdownContent';

export function ConversationView({ snapshot, parts, onInspect }: { snapshot: SessionSnapshot | null; parts: Record<string, StreamingPart>; onInspect: (partID: string) => void }) {
  if (!snapshot) return <div className="empty-state">选择一个会话开始查看对话</div>;
  return <div className="conversation-view">
    {snapshot.turns.flatMap((turn) => turn.messages.map((message, index) => <article key={`${turn.turn_id}-${index}`} className={`message ${message.role}`}>
      <div className="message-role">{message.role === 'user' ? '你' : 'Paw'}</div>
      <MarkdownContent text={message.content ?? ''} />
    </article>))}
    {Object.values(parts).map((part) => <button className={`process-card ${part.kind}`} type="button" onClick={() => onInspect(part.part_id)} key={part.part_id}>
      <span>{part.kind === 'reasoning' ? '思考过程' : '实时响应'}</span><small>{part.text.slice(0, 140) || '等待内容…'}</small>
    </button>)}
  </div>;
}
