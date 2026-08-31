import { useMemo, useState } from 'react';

export interface ComposerProps {
  workspaceID: string;
  sessionID: string;
  busy?: boolean;
  onSubmit: (text: string, commandID: string) => Promise<void>;
}

function newCommandID(): string {
  return crypto.randomUUID();
}

export function Composer({ workspaceID, sessionID, busy = false, onSubmit }: ComposerProps) {
  const storageKey = `paw:draft:${workspaceID}:${sessionID}`;
  const [text, setText] = useState(() => localStorage.getItem(storageKey) ?? '');
  const [pending, setPending] = useState(false);
  const [commandID, setCommandID] = useState<string>();
  const canSubmit = useMemo(() => text.trim() !== '' && !pending && !busy, [text, pending, busy]);

  const update = (value: string) => {
    setText(value);
    if (value) localStorage.setItem(storageKey, value); else localStorage.removeItem(storageKey);
  };
  const submit = async () => {
    if (!canSubmit) return;
    const id = commandID ?? newCommandID();
    setCommandID(id);
    setPending(true);
    try {
      await onSubmit(text.trim(), id);
      update('');
      setCommandID(undefined);
    } finally {
      setPending(false);
    }
  };
  return <div className="composer">
    <textarea aria-label="消息" value={text} onChange={(event) => update(event.target.value)} onKeyDown={(event) => {
      if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submit(); }
    }} placeholder={busy ? '工作区中已有会话运行中' : '给智能体发消息'} />
    <button type="button" disabled={!canSubmit} onClick={() => void submit()}>{pending ? '发送中' : '发送'}</button>
  </div>;
}
