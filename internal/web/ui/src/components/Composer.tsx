import { useEffect, useMemo, useState } from 'react';
import { QueueIndicator } from '../features/conversation/QueueIndicator';

export type RunningAction = 'steer' | 'queue';

export interface ComposerProps {
  workspaceID: string;
  sessionID: string;
  activeTurnID?: string;
  queueCount?: number;
  onSubmit: (text: string, commandID: string) => Promise<void>;
  onSteer?: (text: string, commandID: string, activeTurnID: string) => Promise<void>;
  onQueue?: (text: string, commandID: string, activeTurnID: string) => Promise<void>;
  onCancel?: (commandID: string, activeTurnID: string) => Promise<void>;
}

function newCommandID(): string { return crypto.randomUUID(); }

export function Composer({ workspaceID, sessionID, activeTurnID, queueCount = 0, onSubmit, onSteer, onQueue, onCancel }: ComposerProps) {
  const storageKey = `paw:draft:${workspaceID}:${sessionID}`;
  const [text, setText] = useState(() => localStorage.getItem(storageKey) ?? '');
  const [pending, setPending] = useState(false);
  const [commandID, setCommandID] = useState<string>();
  const [runningAction, setRunningAction] = useState<RunningAction>('steer');
  const running = Boolean(activeTurnID);
  const canSubmit = useMemo(() => text.trim() !== '' && !pending, [text, pending]);

  useEffect(() => {
    setText(localStorage.getItem(storageKey) ?? '');
    setCommandID(undefined);
    setRunningAction('steer');
  }, [storageKey]);

  const update = (value: string) => { setText(value); if (value) localStorage.setItem(storageKey, value); else localStorage.removeItem(storageKey); };
  const submit = async () => {
    if (!canSubmit) return;
    const id = commandID ?? newCommandID();
    setCommandID(id); setPending(true);
    try {
      if (activeTurnID && runningAction === 'queue') {
        if (!onQueue) return;
        await onQueue(text.trim(), id, activeTurnID);
      } else if (activeTurnID) {
        if (!onSteer) return;
        await onSteer(text.trim(), id, activeTurnID);
      } else await onSubmit(text.trim(), id);
      update(''); setCommandID(undefined);
    } finally { setPending(false); }
  };
  const cancel = async () => {
    if (!activeTurnID || !onCancel || pending) return;
    setPending(true);
    try { await onCancel(newCommandID(), activeTurnID); } finally { setPending(false); }
  };
  return <div className="composer-wrap">
    <QueueIndicator count={queueCount} />
    {running && <div className="composer-mode"><button type="button" disabled={!onSteer} className={runningAction === 'steer' ? 'active' : ''} onMouseDown={(event) => event.preventDefault()} onClick={() => setRunningAction('steer')}>即时调整</button><button type="button" disabled={!onQueue} className={runningAction === 'queue' ? 'active' : ''} onMouseDown={(event) => event.preventDefault()} onClick={() => setRunningAction('queue')}>排队</button></div>}
    <div className="composer">
      <textarea aria-label="消息" value={text} onChange={(event) => update(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submit(); } }} placeholder={running ? (runningAction === 'queue' ? '排队到当前回合结束后发送' : '立即调整当前回合') : '给智能体发消息'} />
      {running ? <button type="button" className="stop" disabled={pending || !onCancel} onClick={() => void cancel()}>停止</button> : <button type="button" disabled={!canSubmit} onClick={() => void submit()}>{pending ? '发送中' : '发送'}</button>}
    </div>
  </div>;
}
