import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { api } from '../api/client';
import { EventStream } from '../api/eventStream';
import type { RecentWorkspace, SessionSnapshot, SessionSummary } from '../api/types';
import { WorkbenchShell } from '../components/WorkbenchShell';
import { WorkbenchStore } from './store';

function commandID(): string { return crypto.randomUUID(); }

export function App() {
  const store = useMemo(() => new WorkbenchStore(), []);
  const workbench = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  const [workspaces, setWorkspaces] = useState<RecentWorkspace[]>([]);
  const [workspaceID, setWorkspaceID] = useState<string>();
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [sessionID, setSessionID] = useState<string>();
  const [error, setError] = useState<string>();
  const stream = useRef<EventStream | null>(null);

  const refreshSession = async (targetWorkspace = workspaceID, targetSession = sessionID, publish = true): Promise<SessionSnapshot | null> => {
    if (!targetWorkspace || !targetSession) return null;
    const snapshot = await api.sessionSnapshot(targetWorkspace, targetSession);
    if (publish) store.dispatch({ type: 'snapshot.loaded', snapshot });
    return snapshot;
  };

  const connect = (targetWorkspace: string, snapshot: SessionSnapshot): void => {
    stream.current?.dispose();
    const next = new EventStream({
      workspaceID: targetWorkspace,
      streamID: snapshot.stream_id,
      sequence: snapshot.event_sequence,
      store,
      reloadSnapshot: async () => { await refreshSession(targetWorkspace, snapshot.session_id); },
      onEvent: (event) => {
        if (event.session_id === snapshot.session_id && ['turn.completed', 'turn.failed', 'turn.cancelled'].includes(event.type)) {
          void refreshSession(targetWorkspace, snapshot.session_id, false).then((latest) => {
            if (!latest) return;
            const current = store.getSnapshot();
            store.dispatch({ type: 'snapshot.loaded', snapshot: { ...latest, stream_id: current.streamID || latest.stream_id, event_sequence: current.sequence } });
          });
        }
      }
    });
    stream.current = next;
    next.start();
  };

  const selectSession = async (targetWorkspace: string, targetSession: string): Promise<void> => {
    setSessionID(targetSession);
    const snapshot = await refreshSession(targetWorkspace, targetSession);
    if (snapshot) connect(targetWorkspace, snapshot);
  };

  const selectWorkspace = async (targetWorkspace: string): Promise<void> => {
    setWorkspaceID(targetWorkspace);
    setSessionID(undefined);
    stream.current?.dispose();
    const page = await api.sessions(targetWorkspace);
    setSessions(page.items);
    if (page.items[0]) await selectSession(targetWorkspace, page.items[0].session_id);
  };

  useEffect(() => {
    let disposed = false;
    void (async () => {
      try {
        const fragment = new URLSearchParams(window.location.hash.slice(1));
        const bootstrapToken = fragment.get('bootstrap');
        if (bootstrapToken) {
          await api.exchangeBootstrap(bootstrapToken);
          history.replaceState(null, '', `${location.pathname}${location.search}`);
        }
        const bootstrap = await api.bootstrap();
        let loaded = bootstrap.loaded_workspaces ?? [];
        if (loaded.length === 0 && bootstrap.recent_workspaces[0]) {
          const opened = await api.openWorkspace(bootstrap.recent_workspaces[0].path);
          loaded = [{ id: opened.id, path: opened.path, name: opened.name, last_opened_at: new Date().toISOString() }];
        }
        if (disposed) return;
        setWorkspaces(loaded);
        if (loaded[0]) await selectWorkspace(loaded[0].id);
      } catch (cause) {
        if (!disposed) setError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => { disposed = true; stream.current?.dispose(); };
    // Bootstrap is intentionally a one-shot application startup effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const run = async (action: () => Promise<unknown>, reload = true): Promise<void> => {
    setError(undefined);
    try {
      await action();
      if (reload) await refreshSession();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      throw cause;
    }
  };

  const openWorkspace = (): void => {
    const path = window.prompt('输入绝对工作区路径');
    if (!path) return;
    void run(async () => {
      const opened = await api.openWorkspace(path);
      const workspace = { id: opened.id, path: opened.path, name: opened.name, last_opened_at: new Date().toISOString() };
      setWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
      await selectWorkspace(workspace.id);
    }, false);
  };

  const createSession = (targetWorkspace: string): void => {
    void run(async () => {
      const created = await api.createSession(targetWorkspace, commandID());
      const page = await api.sessions(targetWorkspace);
      setSessions(page.items);
      await selectSession(targetWorkspace, created.session_id);
    }, false);
  };

  if (error && workspaces.length === 0) return <main className="app-fatal" role="alert">无法加载工作台：{error}</main>;
  if (workspaces.length === 0) return <main className="app-empty"><h1>浏览器工作台</h1><p>尚未打开工作区</p><button type="button" onClick={openWorkspace}>打开工作区</button></main>;

  return <>
    {error && <div className="app-error" role="alert">{error}</div>}
    <WorkbenchShell
      workspaces={workspaces}
      sessions={sessions}
      snapshot={workbench.snapshot}
      parts={workbench.parts}
      interactions={(() => { const entries = Object.entries(workbench.interactions ?? {}); const [requestID, value] = entries[0] ?? ['', undefined]; return value ? { requestID, ...value } : null; })()}
      onAnswer={(wid, sid, requestID, selectedOption) => { void run(() => api.answerQuestion(wid, sid, requestID, { selected_option: selectedOption }), false); }}
      onDecide={(wid, sid, requestID, decision) => { void run(() => api.decidePermission(wid, sid, requestID, { decision }), false); }}
      onOpenWorkspace={openWorkspace}
      onCreateSession={createSession}
      onSelectWorkspace={(id) => { void selectWorkspace(id); }}
      onSelectSession={(id) => { if (workspaceID) void selectSession(workspaceID, id); }}
      onSubmit={(wid, sid, text, id) => run(async () => {
        const version = workbench.snapshot?.session_id === sid ? workbench.snapshot.session_version : 0;
        await api.submitMessage(wid, sid, { command_id: id, session_version: version, text });
      })}
      onSteer={(wid, sid, text, id, turnID) => run(() => api.steer(wid, sid, { command_id: id, active_turn_id: turnID, text }))}
      onQueue={(wid, sid, text, id, turnID) => run(() => api.queue(wid, sid, { command_id: id, active_turn_id: turnID, text }))}
      onCancel={(wid, sid, id, turnID) => run(() => api.cancel(wid, sid, { command_id: id, active_turn_id: turnID }))}
    />
  </>;
}
