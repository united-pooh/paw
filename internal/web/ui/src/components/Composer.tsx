import { useEffect, useMemo, useRef, useState } from 'react';
import type { CompletionItem } from '../api/types';
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
  /** 输入候补数据源（@ 文件 / 指令 $ 技能），未提供时不启用候补 */
  loadCompletions?: (trigger: string, query: string) => Promise<CompletionItem[]>;
}

function newCommandID(): string { return crypto.randomUUID(); }

/* ---------- 触发点检测（与 Go 端 complete.DetectWordTrigger 同规则） ---------- */

interface TriggerHit { trigger: '@' | '/' | '$'; start: number; query: string }

function detectTrigger(value: string): TriggerHit | null {
  const runes = Array.from(value);
  const n = runes.length;
  if (n === 0) return null;
  let wordStart = n;
  for (let i = n - 1; i >= 0; i--) {
    if (/\s/.test(runes[i])) { wordStart = i + 1; break; }
    wordStart = i;
  }
  if (wordStart >= n) return null;
  const ch = runes[wordStart];
  if (ch !== '@' && ch !== '/' && ch !== '$') return null;
  if (wordStart > 0 && !/\s/.test(runes[wordStart - 1])) return null;
  const start = runes.slice(0, wordStart).join('').length;
  return { trigger: ch, start, query: runes.slice(wordStart + 1).join('') };
}

interface CompletionState extends TriggerHit {
  items: CompletionItem[];
  selected: number;
  loading: boolean;
}

function SendIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 19V5" />
      <path d="m5 12 7-7 7 7" />
    </svg>
  );
}

function StopIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <rect x="5" y="5" width="14" height="14" rx="3" />
    </svg>
  );
}

export function Composer({ workspaceID, sessionID, activeTurnID, queueCount = 0, onSubmit, onSteer, onQueue, onCancel, loadCompletions }: ComposerProps) {
  const storageKey = `paw:draft:${workspaceID}:${sessionID}`;
  const [text, setText] = useState(() => localStorage.getItem(storageKey) ?? '');
  const [pending, setPending] = useState(false);
  const [commandID, setCommandID] = useState<string>();
  const [runningAction, setRunningAction] = useState<RunningAction>('steer');
  const [completion, setCompletion] = useState<CompletionState | null>(null);
  const requestSeq = useRef(0);
  const running = Boolean(activeTurnID);
  const canSubmit = useMemo(() => text.trim() !== '' && !pending, [text, pending]);

  useEffect(() => {
    setText(localStorage.getItem(storageKey) ?? '');
    setCommandID(undefined);
    setRunningAction('steer');
    setCompletion(null);
  }, [storageKey]);

  const update = (value: string) => { setText(value); if (value) localStorage.setItem(storageKey, value); else localStorage.removeItem(storageKey); };

  // 输入变化时检测 @ / / $ 触发词，防抖拉取候补。
  useEffect(() => {
    if (!loadCompletions) { setCompletion(null); return; }
    const hit = detectTrigger(text);
    if (!hit) { setCompletion(null); return; }
    const seq = ++requestSeq.current;
    setCompletion((prev) => ({
      ...hit,
      items: prev?.trigger === hit.trigger ? prev.items : [],
      selected: 0,
      loading: true,
    }));
    const timer = setTimeout(() => {
      loadCompletions(hit.trigger, hit.query)
        .then((items) => {
          if (requestSeq.current !== seq) return;
          if (items.length === 0) { setCompletion(null); return; }
          setCompletion({ ...hit, items, selected: 0, loading: false });
        })
        .catch(() => { if (requestSeq.current === seq) setCompletion(null); });
    }, 120);
    return () => clearTimeout(timer);
  }, [text, loadCompletions]);

  const applyCompletion = (item: CompletionItem) => {
    if (!completion) return;
    const before = text.slice(0, completion.start);
    let replacement: string;
    if (completion.trigger === '@') {
      // 保留 query 中的路径前缀（如 @~/ 或 @docs/），只替换最末文件名片段
      const idx = completion.query.lastIndexOf('/');
      const prefix = idx >= 0 ? completion.query.slice(0, idx + 1) : '';
      replacement = `@${prefix}${item.label}${item.dir ? '' : ' '}`;
    } else {
      replacement = `${item.label} `;
    }
    update(before + replacement);
    // 目录候选：保持弹窗，effect 会基于新文本继续下钻加载
    if (completion.trigger === '@' && item.dir) return;
    setCompletion(null);
  };

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
      update(''); setCommandID(undefined); setCompletion(null);
    } finally { setPending(false); }
  };
  const cancel = async () => {
    if (!activeTurnID || !onCancel || pending) return;
    setPending(true);
    try { await onCancel(newCommandID(), activeTurnID); } finally { setPending(false); }
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (completion && !completion.loading && completion.items.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setCompletion({ ...completion, selected: Math.min(completion.selected + 1, completion.items.length - 1) });
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setCompletion({ ...completion, selected: Math.max(completion.selected - 1, 0) });
        return;
      }
      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        applyCompletion(completion.items[completion.selected]);
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        setCompletion(null);
        return;
      }
    }
    if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submit(); }
  };

  return <div className="composer-wrap">
    {completion && (
      <div className="completion-popover" role="listbox" aria-label="输入候补">
        {completion.loading ? <div className="completion-empty">加载候补…</div>
          : completion.items.map((item, index) => (
            <button key={item.label} type="button" role="option" aria-selected={index === completion.selected}
              className={index === completion.selected ? 'selected' : ''}
              onMouseDown={(event) => event.preventDefault()}
              onMouseEnter={() => setCompletion({ ...completion, selected: index })}
              onClick={() => applyCompletion(item)}>
              <span className="completion-label">{item.label}</span>
              {item.dir && <span className="completion-badge">目录</span>}
              {item.detail && <span className="completion-detail">{item.detail}</span>}
            </button>
          ))}
      </div>
    )}
    <QueueIndicator count={queueCount} />
    {running && <div className="composer-mode"><button type="button" disabled={!onSteer} className={runningAction === 'steer' ? 'active' : ''} onMouseDown={(event) => event.preventDefault()} onClick={() => setRunningAction('steer')}>即时调整</button><button type="button" disabled={!onQueue} className={runningAction === 'queue' ? 'active' : ''} onMouseDown={(event) => event.preventDefault()} onClick={() => setRunningAction('queue')}>排队</button></div>}
    <div className="composer">
      <textarea aria-label="消息" value={text} onChange={(event) => update(event.target.value)} onKeyDown={handleKeyDown} placeholder={running ? (runningAction === 'queue' ? '排队到当前回合结束后发送' : '立即调整当前回合') : '给 Paw 发消息，@ 引用文件 · / 指令 · $ 技能'} />
      {running
        ? <button type="button" className="composer-send stop" aria-label="停止" title="停止当前回合" disabled={pending || !onCancel} onClick={() => void cancel()}><StopIcon /></button>
        : <button type="button" className="composer-send" aria-label="发送" title="发送" disabled={!canSubmit} onClick={() => void submit()}><SendIcon /></button>}
    </div>
    <div className="composer-hint">Enter 发送 · Shift + Enter 换行 · @ 文件 · / 指令 · $ 技能</div>
  </div>;
}
