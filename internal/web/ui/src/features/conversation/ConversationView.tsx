import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { MessagePart, SessionSnapshot, StreamingPart, ToolCall, ToolResult } from '../../api/types';
import { CopyButton } from '../../components/CopyButton';
import { MarkdownContent } from '../../components/MarkdownContent';

/** 从工具调用入参中提取可展示的目标（路径 / 命令 / URL） */
function toolTarget(call: ToolCall): string {
  const input = call.input;
  if (!input || typeof input !== 'object' || Array.isArray(input)) return '';
  const record = input as Record<string, unknown>;
  for (const key of ['file_path', 'path', 'pattern', 'url', 'command', 'target', 'query']) {
    const value = record[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return '';
}

/** 汇总一条消息里的全部工具调用 */
function messageToolCalls(message: MessagePart): ToolCall[] {
  const calls: ToolCall[] = [];
  for (const part of message.assistant_parts ?? []) {
    if (part.type === 'tool_call' && part.tool_call) calls.push(part.tool_call);
  }
  if (calls.length === 0) {
    if (message.tool_use) calls.push(message.tool_use);
    calls.push(...(message.tool_uses ?? []));
  }
  return calls;
}

function messageReasoning(message: MessagePart): string[] {
  const chunks: string[] = [];
  for (const part of message.assistant_parts ?? []) {
    if (part.type !== 'reasoning' || !part.reasoning) continue;
    if (part.reasoning.redacted) {
      chunks.push('[思考内容已由模型提供方隐藏]');
    } else if (part.reasoning.text?.trim()) {
      chunks.push(part.reasoning.text);
    }
  }
  return chunks;
}

function messageResults(message: MessagePart): ToolResult[] {
  const results: ToolResult[] = [];
  if (message.tool_result) results.push(message.tool_result);
  results.push(...(message.tool_results ?? []));
  return results;
}

/** 消息是否有可见内容（思考 / 工具调用 / 文本 / 工具结果） */
function isVisible(message: MessagePart): boolean {
  return (message.content ?? '').trim() !== ''
    || messageReasoning(message).length > 0
    || messageToolCalls(message).length > 0
    || messageResults(message).length > 0;
}

/* ---------- 工作段（WorkSegment）聚类 ----------
 * 复刻 TUI 端 transcript_worksegment 的语义：连续的 reasoning / tool_call /
 * tool_result 运行被收编为一个“活动段”，正文（assistant 文本）与用户消息
 * 是段边界。渲染时整段折叠为一行摘要，展开后呈现紧凑时间轴。 */

type ActivityItem =
  | { kind: 'reasoning'; text: string }
  | { kind: 'tool'; call: ToolCall; result?: ToolResult };

/** 回合级展示元信息：开始时间 / 耗时 / 本轮 token 增量（来自 turn sidecar）。 */
interface TurnMeta {
  started_at?: string;
  duration_ms?: number;
  input_tokens?: number;
  output_tokens?: number;
  status?: string;
}

type ConversationBlock =
| { type: 'user-text'; key: string; turnID: string; text: string; meta?: TurnMeta }
| { type: 'assistant-text'; key: string; turnID: string; text: string; meta?: TurnMeta }
| { type: 'activity'; key: string; items: ActivityItem[] };

function buildBlocks(snapshot: SessionSnapshot): ConversationBlock[] {
  const blocks: ConversationBlock[] = [];
  let activity: ActivityItem[] = [];
  let callIndex = new Map<string, number>();
  let sequence = 0;

  const flushActivity = (): void => {
    if (activity.length === 0) return;
    blocks.push({ type: 'activity', key: `activity-${sequence++}`, items: activity });
    activity = [];
    callIndex = new Map();
  };

  for (const turn of snapshot.turns) {
    const meta: TurnMeta = {
      started_at: turn.started_at,
      duration_ms: turn.duration_ms,
      input_tokens: turn.input_tokens,
      output_tokens: turn.output_tokens,
      status: turn.status,
    };
    // token 用量是回合级聚合，只挂在该回合最后一条可见的 assistant 正文上
    // （Cherry Studio 式页脚）；其余 assistant 块只显示时间。
    let lastAssistantIndex = -1;
    turn.messages.forEach((message, index) => {
      if (message.role === 'assistant' && isVisible(message) && (message.content ?? '').trim() !== '') lastAssistantIndex = index;
    });
    turn.messages.forEach((message, index) => {
      if (!isVisible(message)) return;
      const baseKey = `${turn.turn_id}-${index}`;
      const content = (message.content ?? '').trim();

      for (const text of messageReasoning(message)) activity.push({ kind: 'reasoning', text });
      for (const call of messageToolCalls(message)) {
        if (call.id) callIndex.set(call.id, activity.length);
        activity.push({ kind: 'tool', call });
      }
      for (const result of messageResults(message)) {
        const at = result.tool_use_id ? callIndex.get(result.tool_use_id) : undefined;
        const item = at === undefined ? undefined : activity[at];
        if (at !== undefined && item?.kind === 'tool') {
          activity[at] = { kind: 'tool', call: item.call, result };
        } else {
          activity.push({ kind: 'tool', call: { id: result.tool_use_id ?? '', name: '工具结果' }, result });
        }
      }

      if (message.role === 'user' && content !== '') {
        flushActivity();
        blocks.push({ type: 'user-text', key: baseKey, turnID: turn.turn_id, text: message.content ?? '', meta: { started_at: meta.started_at, status: meta.status } });
      } else if (message.role === 'assistant' && content !== '') {
        flushActivity();
        blocks.push({ type: 'assistant-text', key: baseKey, turnID: turn.turn_id, text: message.content ?? '', meta: index === lastAssistantIndex ? meta : { started_at: meta.started_at, status: meta.status } });
      }
    });
  }
  flushActivity();
  return blocks;
}

/** HH:MM 时钟格式；解析失败时原样返回。 */
function formatClock(iso?: string): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
}

/** token 数的紧凑格式：1234 → 1.2k */
function formatTokens(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
  return String(value);
}

/** 消息页脚元信息行：时间 · token 出入 · tok/s · 耗时（进行中只显示时间）。 */
function MessageMetaRow({ meta }: { meta: TurnMeta }) {
  const clock = formatClock(meta.started_at);
  const running = meta.status === 'running';
  const input = meta.input_tokens ?? 0;
  const output = meta.output_tokens ?? 0;
  const seconds = (meta.duration_ms ?? 0) / 1000;
  const hasUsage = !running && (input > 0 || output > 0);
  if (!clock && !hasUsage) return null;
  return (
    <div className="message-meta">
      {clock && <span className="meta-clock">{clock}</span>}
      {running && <span className="meta-live">生成中…</span>}
      {hasUsage && (
        <>
          <span className="meta-tokens" title={`输入 ${input} / 输出 ${output} tokens`}>↑{formatTokens(input)} ↓{formatTokens(output)}</span>
          {output > 0 && seconds > 0 && <span className="meta-speed">{(output / seconds).toFixed(1)} tok/s</span>}
          {seconds > 0 && <span className="meta-duration">{seconds >= 10 ? `${Math.round(seconds)}s` : `${seconds.toFixed(1)}s`}</span>}
        </>
      )}
    </div>
  );
}

/** 工具名汇总：按出现顺序去重，重复调用折叠为 ×N */
function summarizeToolNames(items: ActivityItem[]): string {
  const counts = new Map<string, number>();
  for (const item of items) {
    if (item.kind !== 'tool') continue;
    counts.set(item.call.name, (counts.get(item.call.name) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([name, count]) => (count > 1 ? `${name} ×${count}` : name))
    .join('、');
}

function ForkGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="18" r="3" />
      <circle cx="6" cy="6" r="3" />
      <circle cx="18" cy="6" r="3" />
      <path d="M18 9v1a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V9" />
      <path d="M12 12v3" />
    </svg>
  );
}

function ExportGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" x2="12" y1="15" y2="3" />
    </svg>
  );
}

/** 消息操作条：复制正文；assistant 消息额外提供会话分叉与导出（JSON 下载）。 */
function MessageActions({ text, onFork, exportUrl }: { text: string; onFork?: () => void; exportUrl?: string }) {
  return <div className="message-actions">
    <CopyButton text={text} />
    {onFork && (
      <button type="button" className="copy-btn" aria-label="分叉当前会话" title="分叉当前会话（复制全部上下文到新会话）" onClick={onFork}>
        <ForkGlyph />
      </button>
    )}
    {exportUrl && (
      <a className="copy-btn" aria-label="导出会话" title="导出会话（JSON）" href={exportUrl} download>
        <ExportGlyph />
      </a>
    )}
  </div>;
}

// 平滑渲染水位：上游网关常以聚合 chunk 下发（一次给全量），直接渲染会整段弹出。
// 这里以恒定速率追上目标长度，聚合下发时呈现稳定打字机；真流式时几乎无感追平。
// 片段结束后（streaming=false）加速追平而非瞬间铺满，保留打字体感；快照接管后
// 由正文气泡继续展示全量内容，无副作用。
function useTypingText(target: string, streaming: boolean): string {
  const [shown, setShown] = useState(() => (streaming ? 0 : target.length));
  useEffect(() => {
    const step = streaming ? 60 : 120;
    const timer = setInterval(() => {
      setShown((prev) => Math.min(target.length, prev + step));
    }, 45);
    return () => clearInterval(timer);
  }, [streaming, target.length]);
  return target.slice(0, shown);
}

/** 流式正文气泡：markdown 按打字水位渲染，最后一段跟随光标（CSS）。
 *  泵未追平时通过 onPumpState 上报，父级会延迟快照正文接管，保证打字体感不被打断。 */
function LiveTextBubble({ part, active, startedAt, showMeta, onPumpState }: {
  part: StreamingPart; active: boolean; startedAt?: string; showMeta: boolean;
  onPumpState: (partID: string, pumping: boolean) => void;
}) {
  const streaming = active && !part.completed;
  const text = useTypingText(part.text, streaming);
  const pumping = text.length < part.text.length;
  useEffect(() => { onPumpState(part.part_id, pumping); }, [pumping, onPumpState, part.part_id]);
  // stream-typing 覆盖整个泵期（含片段完成后加速追平），光标跟随；live 仅标记片段仍在流。
  return <article className={`message assistant${pumping ? ' stream-typing' : ''}${streaming ? ' live' : ''}`}>
    <div className="message-role">Paw</div>
    <MarkdownContent text={text} />
    {showMeta && active && <MessageMetaRow meta={{ started_at: startedAt, status: 'running' }} />}
  </article>;
}

function ToolGlyph() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
    </svg>
  );
}

function ActivityToolRow({ item }: { item: Extract<ActivityItem, { kind: 'tool' }> }) {
  const target = toolTarget(item.call);
  const failed = Boolean(item.result?.is_error);
  const dotClass = failed ? 'activity-dot err' : item.result ? 'activity-dot ok' : 'activity-dot pending';
  const row = <>
    <span className={dotClass} aria-hidden="true" />
    <span className="activity-tool-icon" aria-hidden="true"><ToolGlyph /></span>
    <span className="activity-tool-name">{item.call.name}</span>
    {target && <span className="activity-tool-target">{target}</span>}
  </>;
  if (!item.result?.content) return <div className={`activity-tool${failed ? ' error' : ''}`}>{row}</div>;
  return (
    <details className={`activity-tool${failed ? ' error' : ''}`}>
      <summary>{row}</summary>
      <pre className="activity-pre">{item.result.content}</pre>
    </details>
  );
}

function ActivityGroup({ items }: { items: ActivityItem[] }) {
  const toolCount = items.filter((item) => item.kind === 'tool').length;
  const errorCount = items.filter((item) => item.kind === 'tool' && item.result?.is_error).length;
  const hasReasoning = items.some((item) => item.kind === 'reasoning');
  // 纯思考的工作段摘要已是「思考过程」，内容直接平铺展示，不再嵌套同名折叠框；
  // 混合段（思考 + 工具）里思考块才需要可折叠的独立区块与工具行区分。
  const onlyReasoning = items.every((item) => item.kind === 'reasoning');
  const names = summarizeToolNames(items);

  let summary: string;
  if (toolCount === 0) summary = '思考过程';
  else if (hasReasoning) summary = `完成思考，执行了 ${toolCount} 项操作`;
  else summary = `执行了 ${toolCount} 项操作`;
  if (errorCount > 0) summary += ` · ${errorCount} 项失败`;

  const groupDot = errorCount > 0 ? 'activity-dot err' : toolCount === 0 ? 'activity-dot think' : 'activity-dot ok';

  return (
    <details className="activity-group" open={errorCount > 0}>
      <summary>
        <span className={groupDot} aria-hidden="true" />
        <span className="activity-summary">{summary}</span>
        {names && <span className="activity-names">{names}</span>}
      </summary>
      <div className="activity-items">
        {items.map((item, index) => item.kind === 'reasoning' ? (
          onlyReasoning
            ? <div className="activity-reasoning-content" key={`reasoning-${index}`}>{item.text}</div>
            : <details className="activity-reasoning" key={`reasoning-${index}`}>
              <summary>
                <span className="activity-dot think" aria-hidden="true" />
                <span className="activity-reasoning-label">思考过程</span>
              </summary>
              <div className="activity-reasoning-content">{item.text}</div>
            </details>
        ) : (
          <ActivityToolRow item={item} key={item.call.id || `tool-${index}`} />
        ))}
      </div>
    </details>
  );
}

// 贴底判定阈值（像素）：容忍滚动圆整与惯性滚动的末端过冲。
const BOTTOM_STICK_THRESHOLD = 32;

export function ConversationView({ snapshot, parts, showActivity = true, onInspect, onFork, exportUrl, sendSignal = 0 }: {
  snapshot: SessionSnapshot | null;
  parts: Record<string, StreamingPart>;
  showActivity?: boolean;
  onInspect: (partID: string) => void;
  /** 分叉当前会话（未提供时隐藏分叉按钮） */
  onFork?: () => void;
  /** 会话导出（JSON 下载）地址（未提供时隐藏导出按钮） */
  exportUrl?: string;
  /** 发送信号：Composer 每次提交消息时递增，触发强制回底（挂载时不触发） */
  sendSignal?: number;
}) {
  // 泵状态：泵未追平的 part 即使快照已到达也继续由流式气泡渲染（聚合上游的 delta→快照
  // 只有几十毫秒，不打断打字机）；同时压制对应 turn 的快照正文，避免双显示，追平后同帧切换。
  const [pumping, setPumping] = useState<ReadonlySet<string>>(new Set());
  const onPumpState = useCallback((partID: string, isPumping: boolean) => {
    setPumping((prev) => {
      if (isPumping === prev.has(partID)) return prev;
      const next = new Set(prev);
      if (isPumping) next.add(partID); else next.delete(partID);
      return next;
    });
  }, []);
  // ---------- 跟随滚动（对齐 TUI auto-scroll 契约） ----------
  // 贴底：内容增长持续跟随；上翻：脱离跟随、保留用户位置；回底：恢复跟随并清未读。
  // 滚动容器通过 callback ref 入 state：首帧 snapshot 为 null 走 empty-state 分支，
  // 容器在数据到达后才挂载——普通 ref + deps=[] 的 effect 会错过绑定时机。
  // state 仅用于在容器挂载后触发 effect；DOM 写操作统一走 nodeRef，避免直接修改 state 值。
  const [scrollEl, setScrollEl] = useState<HTMLDivElement | null>(null);
  const nodeRef = useRef<HTMLDivElement | null>(null);
  const scrollRef = useCallback((el: HTMLDivElement | null) => {
    nodeRef.current = el;
    setScrollEl(el);
  }, []);
  const stickRef = useRef(true);
  const [stickToBottom, setStickToBottom] = useState(true);
  const [pendingCount, setPendingCount] = useState(0);
  const assistantCountRef = useRef(0);

  // 会话切换的重置由调用方 key={sessionID} 重挂载完成（避免在 effect 中同步 setState）。
  const blocks = snapshot ? buildBlocks(snapshot) : [];
  const assistantContentCount = blocks.filter((block) => block.type === 'assistant-text').length
    + Object.values(parts).filter((part) => part.kind === 'assistant' && part.text !== '').length;

  // 新 assistant 消息到达时若未贴底，累计「↓ N 条新消息」提示（TUI new_message_notice 同款）。
  useEffect(() => {
    const previous = assistantCountRef.current;
    assistantCountRef.current = assistantContentCount;
    if (!stickRef.current && assistantContentCount > previous) {
      setPendingCount((count) => count + assistantContentCount - previous);
    }
  }, [assistantContentCount]);

  // 内容渲染后若跟随中，滚到底部；未跟随时浏览器保留用户偏移（TUI refreshViewportWithBottomState）。
  useLayoutEffect(() => {
    const el = nodeRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  });

  // 滚动感知：贴底→跟随；离开→脱离。窗口大小变化时保持贴底语义（TUI WindowSizeMsg 同款）。
  useEffect(() => {
    if (!scrollEl) return;
    const onScroll = () => {
      const atBottom = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight <= BOTTOM_STICK_THRESHOLD;
      stickRef.current = atBottom;
      setStickToBottom(atBottom);
      if (atBottom) setPendingCount(0);
    };
    scrollEl.addEventListener('scroll', onScroll, { passive: true });
    const resize = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(() => {
        const el = nodeRef.current;
        if (el && stickRef.current) el.scrollTop = el.scrollHeight;
      })
      : null;
    resize?.observe(scrollEl);
    return () => {
      scrollEl.removeEventListener('scroll', onScroll);
      resize?.disconnect();
    };
  }, [scrollEl]);

  const scrollToBottomNow = useCallback(() => {
    const el = nodeRef.current;
    stickRef.current = true;
    setStickToBottom(true);
    setPendingCount(0);
    if (el) el.scrollTop = el.scrollHeight;
  }, []);

  // 发送消息时强制回到底部：用户主动提交即表达了关注最新内容的意图，
  // 即使此前上翻脱离跟随，也恢复贴底并清未读（TUI submit 回底同款契约）。
  // 首挂载不触发：ref 以当前信号为基线，仅信号递增时执行。
  const lastSendSignalRef = useRef(sendSignal);
  useLayoutEffect(() => {
    if (lastSendSignalRef.current === sendSignal) return;
    lastSendSignalRef.current = sendSignal;
    scrollToBottomNow();
  }, [sendSignal, scrollToBottomNow]);

  const jumpToLatest = scrollToBottomNow;
  void stickToBottom;

  if (!snapshot) return <div className="empty-state">选择一个会话开始查看对话</div>;
  // 流式视图：SSE delta 实时增长，但快照内容只在回合完成后出现。
  // 这里直接把尚未被快照接管的流式 part 渲染出来；一旦对应 turn 在快照里出现了
  // 可见 assistant 内容（正文/思考/工具），快照接管，流式视图退出，交接无闪烁。
  const snapshotContentTurns = new Set<string>();
  for (const turn of snapshot.turns) {
    if (turn.messages.some((message) => message.role === 'assistant' && isVisible(message))) {
      snapshotContentTurns.add(turn.turn_id);
    }
  }
  const pumpingTurns = new Set(Object.values(parts).filter((part) => pumping.has(part.part_id)).map((part) => part.turn_id));
  const streamingTexts = Object.values(parts).filter((part) => part.kind === 'assistant' && part.text !== '' && (!snapshotContentTurns.has(part.turn_id) || pumping.has(part.part_id)));
  // 对话模式下 reasoning 阶段（模型长考、正文未出）也给出即时反馈：临时思考卡，
  // 回合结束或快照接管后自动消失，不会在结尾残留。
  const liveReasoning = Object.values(parts).filter((part) => part.kind === 'reasoning' && !snapshotContentTurns.has(part.turn_id) && snapshot.active_turn_id === part.turn_id);
  return <div className="conversation-wrap">
    <div className="conversation-view" ref={scrollRef}>
    {blocks.map((block) => {
      if (block.type === 'user-text') {
        // 气泡只承载正文；时间戳等页脚信息与操作条放在气泡外右下角。
        return <article className="message user" key={block.key}>
          <div className="message-role">你</div>
          <div className="user-bubble"><MarkdownContent text={block.text} /></div>
          <div className="message-footer">
            <MessageActions text={block.text} />
            {block.meta && <MessageMetaRow meta={block.meta} />}
          </div>
        </article>;
      }
      if (block.type === 'assistant-text') {
        // 该 turn 的流式气泡仍在打字：由其继续渲染，快照正文先行压制。
        if (pumpingTurns.has(block.turnID)) return null;
        return <article className="message assistant" key={block.key}>
          <div className="message-role">Paw</div>
          <MarkdownContent text={block.text} />
          <div className="message-footer">
            <MessageActions text={block.text} onFork={onFork} exportUrl={exportUrl} />
            {block.meta && <MessageMetaRow meta={block.meta} />}
          </div>
        </article>;
      }
      // 工作段始终挂载在 DOM 中，通过外壳的 grid-rows/透明度过渡动画
      // 在「对话」（隐藏）与「轨迹」（显示）之间平滑插入与移除。
      return <div className={`activity-shell${showActivity ? ' open' : ''}`} key={block.key} aria-hidden={!showActivity}>
        <div className="activity-shell-inner">
          <ActivityGroup items={block.items} />
        </div>
      </div>;
    })}
    {streamingTexts.map((part, index) => {
      const turn = snapshot.turns.find((item) => item.turn_id === part.turn_id);
      const active = snapshot.active_turn_id === part.turn_id;
      const isLast = index === streamingTexts.length - 1;
      return <LiveTextBubble key={`stream-${part.part_id}`} part={part} active={active} startedAt={turn?.started_at} showMeta={isLast} onPumpState={onPumpState} />;
    })}
    {/* 过程卡：轨迹模式展示全部流式片段；对话模式只展示进行中的思考过程（正文已由打字机气泡呈现）。 */}
    {(showActivity ? Object.values(parts) : liveReasoning).map((part) => <button className={`process-card ${part.kind}`} type="button" onClick={() => onInspect(part.part_id)} key={part.part_id}>
      <span>{part.kind === 'reasoning' ? '思考过程' : '实时响应'}</span><small>{part.text.slice(0, 140) || '等待内容…'}</small>
    </button>)}
    </div>
    {pendingCount > 0 && (
      <button type="button" className="scroll-to-latest" title="回到底部" onClick={jumpToLatest}>
        ↓ {pendingCount} 条新消息
      </button>
    )}
  </div>;
}
