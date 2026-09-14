import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { CompletionItem, ModelOptionsResponse } from '../api/types';
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
  /** 模型/推理强度卡片堆的数据源与切换回调，未提供时不渲染卡片堆 */
  loadModelOptions?: () => Promise<ModelOptionsResponse>;
  onSelectModel?: (selection: { model_id?: string; effort?: string }) => Promise<ModelOptionsResponse>;
}

/** 推理强度档位的展示文案（default = 不显式设置）。 */
const EFFORT_LABELS: Record<string, string> = { default: '默认', low: '低', medium: '中', high: '高', xhigh: '超高', max: '最高' };
const effortLabel = (effort: string): string => EFFORT_LABELS[effort] ?? effort;

function newCommandID(): string { return crypto.randomUUID(); }

function resizeTextarea(textarea: HTMLTextAreaElement | null) {
  if (!textarea) return;
  textarea.style.height = 'auto';
  textarea.style.height = `${textarea.scrollHeight}px`;
}

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

function ChevronIcon() {
  return (
    <svg width="10" height="10" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path d="M2.5 4.5L6 8L9.5 4.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <circle cx="6.2" cy="6.2" r="4.7" stroke="currentColor" strokeWidth="1.6" />
      <path d="M9.8 9.8L12.5 12.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg className="o-check" width="12" height="12" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M2.5 7.5L5.5 10.5L11.5 3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/* ---------- 模型胶囊 → 搜索框 morph 的参数 ---------- */
/** 搜索框宽度上下限（px），目标宽度为胶囊宽 + SEARCH_W_GROW */
const SEARCH_MIN_W = 220;
const SEARCH_MAX_W = 460;
const SEARCH_W_GROW = 90;
/** 候选列表最大高度（px，约 6 条），超出后列表内部滚动；与 workbench.css .dropdown-list 的 max-height 对应 */
const LIST_MAX_H = 216;
/** 展开后聚焦输入框的延迟（等宽度动画起步） */
const FOCUS_DELAY_MS = 200;
/** 选定后延迟关闭（让打勾动画可见） */
const CHOOSE_CLOSE_MS = 140;
/** 收缩宽度过渡（CSS width .38s）结束后移除 --pill-w，交还 auto 布局 */
const PILL_CLEANUP_MS = 420;

/** 高亮模型 ID 中与查询匹配的片段。 */
function highlightModelID(id: string, query: string): React.ReactNode {
  const q = query.trim().toLowerCase();
  if (!q) return id;
  const i = id.toLowerCase().indexOf(q);
  if (i < 0) return id;
  return <>{id.slice(0, i)}<mark>{id.slice(i, i + q.length)}</mark>{id.slice(i + q.length)}</>;
}

export function Composer({ workspaceID, sessionID, activeTurnID, queueCount = 0, onSubmit, onSteer, onQueue, onCancel, loadCompletions, loadModelOptions, onSelectModel }: ComposerProps) {
  const storageKey = `paw:draft:${workspaceID}:${sessionID}`;
  const [text, setText] = useState(() => localStorage.getItem(storageKey) ?? '');
  const [pending, setPending] = useState(false);
  const [commandID, setCommandID] = useState<string>();
  const [runningAction, setRunningAction] = useState<RunningAction>('steer');
  const [completion, setCompletion] = useState<CompletionState | null>(null);
  const [modelOptions, setModelOptions] = useState<ModelOptionsResponse | null>(null);
  const [selectingModel, setSelectingModel] = useState(false);
  // 模型选择器：searchOpen 时胶囊 morph 成搜索框，候选列表把卡片往上挤
  const [searchOpen, setSearchOpen] = useState(false);
  const [modelQuery, setModelQuery] = useState('');
  const [modelActiveIdx, setModelActiveIdx] = useState(0);
  const pillRef = useRef<HTMLDivElement>(null);
  const deckCardRef = useRef<HTMLDivElement>(null);
  const modelListRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const requestSeq = useRef(0);
  const modelLoaderRef = useRef(loadModelOptions);
  modelLoaderRef.current = loadModelOptions;
  const running = Boolean(activeTurnID);
  const canSubmit = useMemo(() => text.trim() !== '' && !pending, [text, pending]);

  useLayoutEffect(() => {
    resizeTextarea(textareaRef.current);
  }, [text]);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    let width = 0;
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width === width) return;
      width = entry.contentRect.width;
      resizeTextarea(textarea);
    });
    observer.observe(textarea);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    setText(localStorage.getItem(storageKey) ?? '');
    setCommandID(undefined);
    setRunningAction('steer');
    setCompletion(null);
  }, [storageKey]);

  const update = (value: string) => { setText(value); if (value) localStorage.setItem(storageKey, value); else localStorage.removeItem(storageKey); };

  // 挂载（或切换工作区）时拉取模型目录；加载失败则静默隐藏卡片堆。
  useEffect(() => {
    const loader = modelLoaderRef.current;
    if (!loader) return;
    let cancelled = false;
    loader().then((options) => { if (!cancelled) setModelOptions(options); }).catch(() => { /* 隐藏卡片堆 */ });
    return () => { cancelled = true; };
  }, [workspaceID]);

  const applyModelSelection = async (selection: { model_id?: string; effort?: string }) => {
    if (!onSelectModel || selectingModel) return;
    setSelectingModel(true);
    try { setModelOptions(await onSelectModel(selection)); } catch { /* 保留旧状态 */ } finally { setSelectingModel(false); }
  };

  const activeModel = modelOptions?.models.find((model) => model.id === modelOptions.active_model_id);
  const activeEffort = activeModel?.effort || 'default';

  /* ---------- 模型选择器：胶囊 ⇄ 搜索框 morph ---------- */

  // 过滤后的候选模型（输入实时筛选）
  const filteredModels = useMemo(() => {
    const q = modelQuery.trim().toLowerCase();
    if (!modelOptions) return [];
    if (!q) return modelOptions.models;
    return modelOptions.models.filter((m) => m.id.toLowerCase().includes(q));
  }, [modelOptions, modelQuery]);

  // 展开/收缩时驱动 --pill-w 实现宽度过渡（width:auto 不可过渡，用 FLIP：
  // 钉住起点宽 → 强制 reflow 提交 → 下一帧写到目标宽，靠 CSS transition 走过去）
  useLayoutEffect(() => {
    const pill = pillRef.current;
    if (!pill) return;
    let cleanup: ReturnType<typeof setTimeout> | undefined;
    if (searchOpen) {
      // ① 起点 = 胶囊当前内容宽度；② 下一帧过渡到自适应目标宽度
      const startW = pill.offsetWidth;
      pill.style.setProperty('--pill-w', `${startW}px`);
      void pill.offsetWidth; // 强制 reflow
      const targetW = Math.max(SEARCH_MIN_W, Math.min(SEARCH_MAX_W, startW + SEARCH_W_GROW));
      requestAnimationFrame(() => pill.style.setProperty('--pill-w', `${targetW}px`));
    } else if (pill.style.getPropertyValue('--pill-w')) {
      // 收缩（对称 FLIP）：钉住当前搜索框宽 → 摘下 --pill-w 量出自然内容宽 →
      // 钉回起点并 reflow → 下一帧过渡到自然宽，动画结束交还 auto。
      // --pill-w 未设置说明从未展开过（如挂载首帧），无事可做。
      const startW = pill.offsetWidth;
      pill.style.removeProperty('--pill-w');
      const naturalW = pill.offsetWidth;
      pill.style.setProperty('--pill-w', `${startW}px`);
      void pill.offsetWidth;
      requestAnimationFrame(() => {
        pill.style.setProperty('--pill-w', `${naturalW}px`);
        cleanup = setTimeout(() => pill.style.removeProperty('--pill-w'), PILL_CLEANUP_MS);
      });
    }
    // 收缩途中重新展开时取消清理定时器，避免 --pill-w 被中途摘掉
    return () => clearTimeout(cleanup);
  }, [searchOpen]);

  // 列表高度（夹紧上限）写入 --list-h：CSS 自定义属性沿 DOM 继承，dropdown 是
  // deck-card 的子元素，设一次即可同时驱动卡片高度与列表高度，筛选变少时平滑回落
  useLayoutEffect(() => {
    const list = modelListRef.current;
    const card = deckCardRef.current;
    if (!searchOpen || !card || !list) return;
    card.style.setProperty('--list-h', `${Math.min(list.scrollHeight, LIST_MAX_H)}px`);
  }, [searchOpen, filteredModels]);

  const resetModelSearch = (query = '') => {
    setModelQuery(query);
    setModelActiveIdx(0);
  };

  const openModelSearch = () => {
    if (searchOpen || !modelOptions) return;
    resetModelSearch();
    setSearchOpen(true);
    setTimeout(() => searchInputRef.current?.focus(), FOCUS_DELAY_MS);
  };

  const closeModelSearch = useCallback(() => {
    if (!searchOpen) return;
    setSearchOpen(false);
    searchInputRef.current?.blur();
  }, [searchOpen]);

  const chooseModel = (id: string) => {
    void applyModelSelection({ model_id: id });
    setTimeout(closeModelSearch, CHOOSE_CLOSE_MS);
  };

  // 点击卡片外部收缩
  useEffect(() => {
    if (!searchOpen) return;
    const onDocClick = (event: MouseEvent) => {
      if (!deckCardRef.current?.contains(event.target as Node)) closeModelSearch();
    };
    document.addEventListener('click', onDocClick);
    return () => document.removeEventListener('click', onDocClick);
  }, [searchOpen, closeModelSearch]);

  const handleSearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setModelActiveIdx((i) => Math.min(i + 1, filteredModels.length - 1));
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setModelActiveIdx((i) => Math.max(i - 1, 0));
    } else if (event.key === 'Enter') {
      const target = filteredModels[modelActiveIdx];
      if (target) chooseModel(target.id);
    } else if (event.key === 'Escape') {
      event.stopPropagation();
      if (modelQuery) resetModelSearch();
      else closeModelSearch();
    }
  };

  // 高亮项变化时滚动进可视区
  useEffect(() => {
    modelListRef.current?.children[modelActiveIdx]?.scrollIntoView({ block: 'nearest' });
  }, [modelActiveIdx]);

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
    {/* 卡片堆：模型配置卡在后、输入卡在前。静置时后卡只露顶部一条预览带，
        悬浮预览带或聚焦控件时后卡以高度动画向上抽出；负 margin 始终保持 30px
        交叠，抽出后下缘依旧藏在输入卡之下，保持「抽出来的卡片」而非两张分离卡片。
        搜索态（search-open）下候选列表在卡片内部、控件行下方展开，把卡片高度往上挤。 */}
    {modelOptions && modelOptions.models.length > 0 && (
      <div className={searchOpen ? 'deck-card search-open' : 'deck-card'} ref={deckCardRef}>
        <div className="deck-peek" aria-hidden="true">
          {activeModel ? `${activeModel.name}${activeEffort !== 'default' ? ` · ${effortLabel(activeEffort)}` : ''}` : '模型'}
        </div>
        <div className="deck-row">
          <div className="deck-field">
            <span className="deck-tag">模型</span>
            {/* 胶囊/搜索框同体 morph：同一元素双形态，width 过渡向右延展。
                卡片内部点击不拦截冒泡——document 级监听用 contains() 判断外部点击，单一机制 */}
            <div className={searchOpen ? 'model-pill searching' : 'model-pill'} ref={pillRef}
              role="button" tabIndex={0} aria-label="切换模型" aria-expanded={searchOpen}
              onClick={openModelSearch}
              onKeyDown={(event) => {
                if ((event.key === 'Enter' || event.key === ' ') && !searchOpen) {
                  event.preventDefault(); openModelSearch();
                }
              }}>
              <span className="pill-face">
                <span className="pill-name">{modelOptions.active_model_id}</span>
                <ChevronIcon />
              </span>
              <span className="search-face">
                <SearchIcon />
                <input ref={searchInputRef} type="text" aria-label="搜索模型" placeholder="搜索模型…"
                  autoComplete="off" spellCheck={false} value={modelQuery}
                  onChange={(event) => resetModelSearch(event.target.value)}
                  onKeyDown={handleSearchKeyDown} />
                {modelQuery && (
                  <button type="button" className="clear-btn" tabIndex={-1} aria-label="清空搜索"
                    onClick={() => { resetModelSearch(); searchInputRef.current?.focus(); }}>✕</button>
                )}
              </span>
            </div>
          </div>
          <label className="deck-field">
            <span className="deck-tag">推理强度</span>
            <select aria-label="推理强度" value={activeEffort} disabled={selectingModel || !activeModel?.reasoning_capable}
              onChange={(event) => void applyModelSelection({ effort: event.target.value })}>
              {(modelOptions.effort_options.length > 0 ? modelOptions.effort_options : ['default']).map((effort) => (
                <option key={effort} value={effort}>{effortLabel(effort)}</option>
              ))}
            </select>
          </label>
        </div>
        {/* 候选列表：deck-card 内部、控件行之下，展开时把卡片高度往上挤 */}
        <div className="model-dropdown">
          <div className="dropdown-list" role="listbox" aria-label="模型候选" ref={modelListRef}>
            {filteredModels.length === 0
              ? <div className="dropdown-empty">没有匹配「{modelQuery}」的模型</div>
              : filteredModels.map((model, index) => (
                <button key={model.id} type="button" role="option" aria-selected={model.id === modelOptions.active_model_id}
                  className={['model-option', model.id === modelOptions.active_model_id ? 'selected' : '', index === modelActiveIdx ? 'active' : ''].filter(Boolean).join(' ')}
                  style={{ animationDelay: `${index * 18}ms` }}
                  onMouseEnter={() => setModelActiveIdx(index)}
                  onClick={() => chooseModel(model.id)}
                  disabled={selectingModel}>
                  <span className="o-name">{highlightModelID(model.id, modelQuery)}</span>
                  <CheckIcon />
                </button>
              ))}
          </div>
        </div>
      </div>
    )}
    <div className="composer">
      <textarea ref={textareaRef} rows={1} aria-label="消息" value={text} onChange={(event) => update(event.target.value)} onKeyDown={handleKeyDown} placeholder={running ? (runningAction === 'queue' ? '排队到当前回合结束后发送' : '立即调整当前回合') : '给 Paw 发消息，@ 引用文件 · / 指令 · $ 技能'} />
      {running
        ? <button type="button" className="composer-send stop" aria-label="停止" title="停止当前回合" disabled={pending || !onCancel} onClick={() => void cancel()}><StopIcon /></button>
        : <button type="button" className="composer-send" aria-label="发送" title="发送" disabled={!canSubmit} onClick={() => void submit()}><SendIcon /></button>}
    </div>
  </div>;
}
