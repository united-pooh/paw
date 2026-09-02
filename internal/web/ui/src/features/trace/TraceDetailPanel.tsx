import { useEffect, useState } from 'react';
import type { StreamingPart, ToolCallState } from '../../api/types';

interface TraceDetailPayload {
  id: string;
  kind: string;
  content: string;
  truncated: boolean;
}

export function TraceDetailPanel({
  workspaceID,
  part,
  tool,
  detailID,
  onClose,
}: {
  workspaceID?: string;
  part?: StreamingPart;
  tool?: ToolCallState;
  detailID?: string;
  onClose: () => void;
}) {
  const [detail, setDetail] = useState<TraceDetailPayload | null>(null);
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('ready');

  useEffect(() => {
    setDetail(null);
    if (!detailID || !workspaceID) {
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
  }, [detailID, workspaceID]);

  if (!part && !tool) return null;
  const title = tool ? `工具调用：${tool.name}` : (part?.kind === 'reasoning' ? '思考过程' : (part?.kind ?? ''));
  const content = detail?.content ?? part?.text ?? tool?.result_summary ?? '';
  return <aside className="trace-detail" aria-label="轨迹详情">
    <header>
      <strong>{title}</strong>
      <button type="button" onClick={onClose}>×</button>
    </header>
    <dl>
      {part && <><dt>Part ID</dt><dd>{part.part_id}</dd><dt>Turn</dt><dd>{part.turn_id}</dd></>}
      {tool && <>
        {tool.target && <><dt>路径</dt><dd>{tool.target}</dd></>}
        {tool.args_summary && <><dt>参数</dt><dd>{tool.args_summary}</dd></>}
        <dt>状态</dt><dd>{tool.status === 'running' ? '执行中' : tool.status === 'failed' ? '失败' : '完成'}{tool.duration_ms !== undefined ? `（${tool.duration_ms}ms）` : ''}</dd>
        {tool.error_code && <><dt>错误码</dt><dd>{tool.error_code}</dd></>}
        {tool.detail_id && <><dt>Detail</dt><dd>{tool.detail_id}</dd></>}
      </>}
    </dl>
    {state === 'loading' && <p role="status">加载详情…</p>}
    {state === 'error' && <p role="alert">详情加载失败</p>}
    {state === 'ready' && content && <pre>{content}</pre>}
    {state === 'ready' && !content && detailID && <p role="status">暂无详情内容</p>}
    {detail?.truncated && <p role="status">内容已截断</p>}
    {state === 'ready' && content && (
      <button type="button" onClick={() => void navigator.clipboard.writeText(content)}>复制</button>
    )}
  </aside>;
}
