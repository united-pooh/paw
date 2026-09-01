import { useEffect, useState } from 'react';
import type { StreamingPart } from '../../api/types';

interface TraceDetailPayload {
  id: string;
  kind: string;
  content: string;
  truncated: boolean;
}

export function TraceDetailPanel({
  workspaceID,
  sessionID,
  part,
  detailID,
  onClose,
}: {
  workspaceID?: string;
  sessionID?: string;
  part?: StreamingPart;
  detailID?: string;
  onClose: () => void;
}) {
  const [detail, setDetail] = useState<TraceDetailPayload | null>(null);
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('ready');

  useEffect(() => {
    setDetail(null);
    if (!detailID || !workspaceID || !sessionID) {
      setState('ready');
      return;
    }
    let disposed = false;
    setState('loading');
    void (async () => {
      try {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(workspaceID)}/trace/${encodeURIComponent(detailID)}`,
          { credentials: 'same-origin' }
        );
        if (!response.ok) throw new Error(String(response.status));
        const payload = (await response.json()) as TraceDetailPayload;
        if (!disposed) setDetail(payload);
      } catch {
        if (!disposed) setState('error');
        return;
      }
      if (!disposed) setState('ready');
    })();
    return () => { disposed = true; };
  }, [detailID, workspaceID, sessionID]);

  if (!part) return null;
  const content = detail?.content ?? part.text;
  return <aside className="trace-detail" aria-label="轨迹详情">
    <header>
      <strong>{part.kind}</strong>
      <button type="button" onClick={onClose}>×</button>
    </header>
    <dl>
      <dt>Part ID</dt><dd>{part.part_id}</dd>
      <dt>Turn</dt><dd>{part.turn_id}</dd>
    </dl>
    {state === 'loading' && <p role="status">加载详情…</p>}
    {state === 'error' && <p role="alert">详情加载失败</p>}
    {state === 'ready' && <pre>{content}</pre>}
    {detail?.truncated && <p role="status">内容已截断</p>}
    {state === 'ready' && content && (
      <button type="button" onClick={() => void navigator.clipboard.writeText(content)}>复制</button>
    )}
  </aside>;
}
