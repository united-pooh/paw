import type { SessionSummary } from '../../api/types';

export function SessionList({ sessions, selected, onSelect, onCreate, onCollapse }: { sessions: SessionSummary[]; selected?: string; onSelect: (id: string) => void; onCreate?: () => void; onCollapse?: () => void }) {
  return <section className="session-list" aria-label="会话列表">
    <div className="session-list-header">
      <span>会话</span>
      <div className="session-list-actions">
        <button type="button" disabled={!onCreate} onClick={onCreate} aria-label="新建会话" title="新建会话">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
        {onCollapse && (
          <button type="button" onClick={onCollapse} aria-label="收起会话面板" title="收起会话面板">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <rect width="18" height="18" x="3" y="3" rx="2" />
              <path d="M9 3v18" />
              <path d="m14 9-3 3 3 3" />
            </svg>
          </button>
        )}
      </div>
    </div>
    {sessions.map((session) => <button type="button" key={session.session_id} className={session.session_id === selected ? 'selected' : ''} onClick={() => onSelect(session.session_id)}>
      <span>{session.title || '新会话'}</span><small>{new Date(session.last_used_at).toLocaleString()}</small>
    </button>)}
  </section>;
}
