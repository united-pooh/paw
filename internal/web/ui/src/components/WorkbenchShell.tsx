import { useState } from 'react';
import type { RecentWorkspace, SessionSnapshot, SessionSummary, StreamingPart } from '../api/types';
import { WorkspaceSidebar } from '../features/workspaces/WorkspaceSidebar';
import { SessionList } from '../features/sessions/SessionList';
import { ConversationView } from '../features/conversation/ConversationView';
import { TraceView } from '../features/trace/TraceView';
import { TraceDetailPanel } from '../features/trace/TraceDetailPanel';
import { Composer } from './Composer';

export interface WorkbenchShellProps {
  workspaces: RecentWorkspace[];
  sessions: SessionSummary[];
  snapshot: SessionSnapshot | null;
  parts: Record<string, StreamingPart>;
  onSelectWorkspace?: (workspaceID: string) => void;
  onSelectSession?: (sessionID: string) => void;
  onOpenWorkspace?: () => void;
  onCreateSession?: (workspaceID: string) => void;
  onSubmit?: (workspaceID: string, sessionID: string, text: string, commandID: string) => Promise<void>;
  onSteer?: (workspaceID: string, sessionID: string, text: string, commandID: string, turnID: string) => Promise<void>;
  onQueue?: (workspaceID: string, sessionID: string, text: string, commandID: string, turnID: string) => Promise<void>;
  onCancel?: (workspaceID: string, sessionID: string, commandID: string, turnID: string) => Promise<void>;
}

export function WorkbenchShell({ workspaces, sessions, snapshot, parts, onSelectWorkspace, onSelectSession, onOpenWorkspace, onCreateSession, onSubmit, onSteer, onQueue, onCancel }: WorkbenchShellProps) {
  const [workspaceID, setWorkspaceID] = useState<string | undefined>(workspaces[0]?.id);
  const [sessionID, setSessionID] = useState<string | undefined>(snapshot?.session_id ?? sessions[0]?.session_id);
  const [tab, setTab] = useState<'conversation' | 'trace'>('conversation');
  const [selectedPart, setSelectedPart] = useState<string>();
  const selectedWorkspaceID = workspaces.some((item) => item.id === workspaceID) ? workspaceID : workspaces[0]?.id;
  const selectedSessionID = sessions.some((item) => item.session_id === sessionID) ? sessionID : (snapshot?.session_id ?? sessions[0]?.session_id);
  const activeSnapshot = snapshot?.session_id === selectedSessionID ? snapshot : null;
  const inspect = (partID: string) => { setSelectedPart(partID); setTab('trace'); };
  return <main className="workbench-shell">
    <WorkspaceSidebar workspaces={workspaces} selected={selectedWorkspaceID} onOpen={onOpenWorkspace} onSelect={(id) => { setWorkspaceID(id); setSessionID(undefined); onSelectWorkspace?.(id); }} />
    <SessionList sessions={sessions} selected={selectedSessionID} onCreate={selectedWorkspaceID && onCreateSession ? () => onCreateSession(selectedWorkspaceID) : undefined} onSelect={(id) => { setSessionID(id); onSelectSession?.(id); }} />
    <section className="main-workspace">
      <header className="topbar"><div><h1>{sessions.find((item) => item.session_id === selectedSessionID)?.title || '浏览器工作台'}</h1><span className="connection-badge">本地连接</span></div><nav><button className={tab === 'conversation' ? 'active' : ''} onClick={() => setTab('conversation')} type="button">对话</button><button className={tab === 'trace' ? 'active' : ''} onClick={() => setTab('trace')} type="button">轨迹</button></nav></header>
      {tab === 'conversation' ? <ConversationView snapshot={activeSnapshot} parts={parts} onInspect={inspect} /> : <TraceView parts={parts} selected={selectedPart} onSelect={setSelectedPart} />}
      {selectedWorkspaceID && selectedSessionID && onSubmit ? <Composer key={`${selectedWorkspaceID}:${selectedSessionID}`} workspaceID={selectedWorkspaceID} sessionID={selectedSessionID} activeTurnID={activeSnapshot?.active_turn_id} queueCount={activeSnapshot?.queue?.length ?? 0}
        onSubmit={(text, commandID) => onSubmit(selectedWorkspaceID, selectedSessionID, text, commandID)}
        onSteer={onSteer ? (text, commandID, turnID) => onSteer(selectedWorkspaceID, selectedSessionID, text, commandID, turnID) : undefined}
        onQueue={onQueue ? (text, commandID, turnID) => onQueue(selectedWorkspaceID, selectedSessionID, text, commandID, turnID) : undefined}
        onCancel={onCancel ? (commandID, turnID) => onCancel(selectedWorkspaceID, selectedSessionID, commandID, turnID) : undefined} /> : <div className="readonly-composer"><textarea aria-label="消息" placeholder="请选择会话" disabled /><button type="button" disabled>发送</button></div>}
    </section>
    <TraceDetailPanel part={selectedPart ? parts[selectedPart] : undefined} onClose={() => setSelectedPart(undefined)} />
  </main>;
}
