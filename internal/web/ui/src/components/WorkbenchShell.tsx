import { useState } from 'react';
import type { RecentWorkspace, SessionSnapshot, SessionSummary, StreamingPart } from '../api/types';
import { WorkspaceSidebar } from '../features/workspaces/WorkspaceSidebar';
import { SessionList } from '../features/sessions/SessionList';
import { ConversationView } from '../features/conversation/ConversationView';
import { TraceView } from '../features/trace/TraceView';
import { TraceDetailPanel } from '../features/trace/TraceDetailPanel';

export interface WorkbenchShellProps {
  workspaces: RecentWorkspace[];
  sessions: SessionSummary[];
  snapshot: SessionSnapshot | null;
  parts: Record<string, StreamingPart>;
}

export function WorkbenchShell({ workspaces, sessions, snapshot, parts }: WorkbenchShellProps) {
  const [workspaceID, setWorkspaceID] = useState(workspaces[0]?.id);
  const [sessionID, setSessionID] = useState(snapshot?.session_id ?? sessions[0]?.session_id);
  const [tab, setTab] = useState<'conversation' | 'trace'>('conversation');
  const [selectedPart, setSelectedPart] = useState<string>();
  const inspect = (partID: string) => { setSelectedPart(partID); setTab('trace'); };
  return <main className="workbench-shell">
    <WorkspaceSidebar workspaces={workspaces} selected={workspaceID} onSelect={setWorkspaceID} />
    <SessionList sessions={sessions} selected={sessionID} onSelect={setSessionID} />
    <section className="main-workspace">
      <header className="topbar"><div><h1>{sessions.find((item) => item.session_id === sessionID)?.title || '浏览器工作台'}</h1><span className="connection-badge">本地连接</span></div><nav><button className={tab === 'conversation' ? 'active' : ''} onClick={() => setTab('conversation')} type="button">对话</button><button className={tab === 'trace' ? 'active' : ''} onClick={() => setTab('trace')} type="button">轨迹</button></nav></header>
      {tab === 'conversation' ? <ConversationView snapshot={snapshot} parts={parts} onInspect={inspect} /> : <TraceView parts={parts} selected={selectedPart} onSelect={setSelectedPart} />}
      <div className="readonly-composer"><textarea aria-label="消息" placeholder="写消息功能将在下一阶段接通" disabled /><button type="button" disabled>发送</button></div>
    </section>
    <TraceDetailPanel part={selectedPart ? parts[selectedPart] : undefined} onClose={() => setSelectedPart(undefined)} />
  </main>;
}
