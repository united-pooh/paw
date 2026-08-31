import type { SessionSummary } from '../../api/types';

export function SessionList({ sessions, selected, onSelect }: { sessions: SessionSummary[]; selected?: string; onSelect: (id: string) => void }) {
  return <section className="session-list" aria-label="会话列表">
    <div className="session-list-header"><span>会话</span><button type="button" disabled>＋</button></div>
    {sessions.map((session) => <button type="button" key={session.session_id} className={session.session_id === selected ? 'selected' : ''} onClick={() => onSelect(session.session_id)}>
      <span>{session.title || '新会话'}</span><small>{new Date(session.last_used_at).toLocaleString()}</small>
    </button>)}
  </section>;
}
