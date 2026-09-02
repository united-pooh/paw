import { useState } from 'react';
import { api } from '../api/client';
import type { RecentWorkspace, SessionSnapshot, SessionSummary, StreamingPart, ToolCallState } from '../api/types';
import { WorkspaceSidebar } from '../features/workspaces/WorkspaceSidebar';
import { SessionList } from '../features/sessions/SessionList';
import { ConversationView } from '../features/conversation/ConversationView';
import { TraceDetailPanel } from '../features/trace/TraceDetailPanel';
import { InteractionBanner, type PendingInteractionState } from '../features/interactions/InteractionBanner';
import { Composer } from './Composer';

export interface WorkbenchShellProps {
  workspaces: RecentWorkspace[];
  sessions: SessionSummary[];
  snapshot: SessionSnapshot | null;
  parts: Record<string, StreamingPart>;
  tools?: Record<string, ToolCallState>;
  onSelectWorkspace?: (workspaceID: string) => void;
  onSelectSession?: (sessionID: string) => void;
  onOpenWorkspace?: () => void;
  onCreateSession?: (workspaceID: string) => void;
  onSubmit?: (workspaceID: string, sessionID: string, text: string, commandID: string) => Promise<void>;
  onSteer?: (workspaceID: string, sessionID: string, text: string, commandID: string, turnID: string) => Promise<void>;
  onQueue?: (workspaceID: string, sessionID: string, text: string, commandID: string, turnID: string) => Promise<void>;
  onCancel?: (workspaceID: string, sessionID: string, commandID: string, turnID: string) => Promise<void>;
  interactions?: PendingInteractionState | null;
  onAnswer?: (workspaceID: string, sessionID: string, requestID: string, selectedOption: string) => void;
  onDecide?: (workspaceID: string, sessionID: string, requestID: string, decision: 'allow_once' | 'deny') => void;
}

export function WorkbenchShell({ workspaces, sessions, snapshot, parts, interactions, onSelectWorkspace, onSelectSession, onOpenWorkspace, onCreateSession, onSubmit, onSteer, onQueue, onCancel, onAnswer, onDecide }: WorkbenchShellProps) {
  const [workspaceID, setWorkspaceID] = useState<string | undefined>(workspaces[0]?.id);
  const [sessionID, setSessionID] = useState<string | undefined>(snapshot?.session_id ?? sessions[0]?.session_id);
  const [tab, setTab] = useState<'conversation' | 'trace'>('conversation');
  const [panelCollapsed, setPanelCollapsed] = useState(false);
  const [selectedPart, setSelectedPart] = useState<string>();
  const selectedWorkspaceID = workspaces.some((item) => item.id === workspaceID) ? workspaceID : workspaces[0]?.id;
  const selectedSessionID = sessions.some((item) => item.session_id === sessionID) ? sessionID : (snapshot?.session_id ?? sessions[0]?.session_id);
  const activeSnapshot = snapshot?.session_id === selectedSessionID ? snapshot : null;
  const inspect = (partID: string) => { setSelectedPart(partID); setTab('trace'); };
  return <main className={`workbench-shell${panelCollapsed ? ' panel-collapsed' : ''}`}>
    <WorkspaceSidebar workspaces={workspaces} selected={selectedWorkspaceID} onOpen={onOpenWorkspace} onSelect={(id) => { setWorkspaceID(id); setSessionID(undefined); onSelectWorkspace?.(id); }} />
    <SessionList sessions={sessions} selected={selectedSessionID} onCreate={selectedWorkspaceID && onCreateSession ? () => onCreateSession(selectedWorkspaceID) : undefined} onSelect={(id) => { setSessionID(id); onSelectSession?.(id); }} onCollapse={() => setPanelCollapsed(true)} />
    <section className="main-workspace">
      <header className="topbar"><div>
        <button type="button" className="topbar-toggle" aria-label="切换会话面板" title="切换会话面板" onClick={() => setPanelCollapsed((value) => !value)}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect width="18" height="18" x="3" y="3" rx="2" />
            <path d="M9 3v18" />
          </svg>
        </button>
        <h1>{sessions.find((item) => item.session_id === selectedSessionID)?.title || '浏览器工作台'}</h1><span className="connection-badge">本地连接</span></div><nav><button className={tab === 'conversation' ? 'active' : ''} onClick={() => setTab('conversation')} type="button">对话</button><button className={tab === 'trace' ? 'active' : ''} onClick={() => setTab('trace')} type="button">轨迹</button></nav></header>
      {/* 对话与轨迹渲染同一内容流，唯一区别是工作段（思考 / 工具活动）是否显示；
          组件保持挂载，切换标签时工作段以过渡动画插入或移除。 */}
      <ConversationView snapshot={activeSnapshot} parts={parts} showActivity={tab === 'trace'} onInspect={inspect} />
      {/* 悬浮坞：输入框、候补弹窗与交互横幅浮在 transcript 之上，
          底部渐变过渡避免生硬分界；滚动容器预留底部净空，不会被遮挡。 */}
      <div className="dock">
        {interactions && (
          <InteractionBanner
            pending={interactions}
            onAnswer={(requestID, selectedOption) => onAnswer?.(selectedWorkspaceID ?? '', selectedSessionID ?? '', requestID, selectedOption)}
            onDecide={(requestID, decision) => onDecide?.(selectedWorkspaceID ?? '', selectedSessionID ?? '', requestID, decision)}
          />
        )}
        {selectedWorkspaceID && selectedSessionID && onSubmit ? <Composer key={`${selectedWorkspaceID}:${selectedSessionID}`} workspaceID={selectedWorkspaceID} sessionID={selectedSessionID} activeTurnID={activeSnapshot?.active_turn_id} queueCount={activeSnapshot?.queue?.length ?? 0}
          onSubmit={(text, commandID) => onSubmit(selectedWorkspaceID, selectedSessionID, text, commandID)}
          onSteer={onSteer ? (text, commandID, turnID) => onSteer(selectedWorkspaceID, selectedSessionID, text, commandID, turnID) : undefined}
          onQueue={onQueue ? (text, commandID, turnID) => onQueue(selectedWorkspaceID, selectedSessionID, text, commandID, turnID) : undefined}
onCancel={onCancel ? (commandID, turnID) => onCancel(selectedWorkspaceID, selectedSessionID, commandID, turnID) : undefined}
loadCompletions={async (trigger, query) => (await api.completions(selectedWorkspaceID, trigger, query)).items}
loadModelOptions={() => api.modelOptions(selectedWorkspaceID)}
onSelectModel={(selection) => api.selectModel(selectedWorkspaceID, selection)} /> : <div className="readonly-composer"><textarea aria-label="消息" placeholder="请选择会话" disabled /><button type="button" disabled>发送</button></div>}
      </div>
    </section>
    <TraceDetailPanel
      workspaceID={selectedWorkspaceID}
      part={selectedPart ? parts[selectedPart] : undefined}
      detailID={selectedPart && selectedPart.startsWith('detail_') ? selectedPart : undefined}
      onClose={() => setSelectedPart(undefined)}
    />
  </main>;
}
