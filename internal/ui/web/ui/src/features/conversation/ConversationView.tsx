import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { MessagePart, SessionSnapshot, StreamingPart, ToolCall, ToolCallState, ToolResult } from '../../api/types';
import { CopyButton } from '../../components/CopyButton';
import { MarkdownContent } from '../../components/MarkdownContent';
import { TurnNavigator } from './TurnNavigator';
import { buildTurnNavigation } from './turnNavigation';
import { turnAnchors, useConversationNavigation } from './useConversationNavigation';

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
 * 是段边界。渲染为竖轨时间轴：思考与工具是轨道上的节点，默认折叠穿插在
 * 对话流中，就地展开查看内容。 */

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
  return <article data-turn-id={part.turn_id} tabIndex={-1} className={`message assistant${pumping ? ' stream-typing' : ''}${streaming ? ' live' : ''}`}>
    <div className="message-role">Paw</div>
    <MarkdownContent text={text} />
    {showMeta && active && <MessageMetaRow meta={{ started_at: startedAt, status: 'running' }} />}
  </article>;
}

/** 工具行（竖轨节点内）：状态点 + 名称 + 目标；有结果内容时行内可展开。
 *  静息段与实时段共用：静息段由 ToolCall+ToolResult 驱动，实时段由 ToolCallState 驱动。 */
function RailToolRow({ name, target, failed, pending, content }: {
  name: string; target?: string; failed: boolean; pending: boolean; content?: string;
}) {
  const dotClass = failed ? 'rail-dot err' : pending ? 'rail-dot pending' : 'rail-dot ok';
  const label = <>
    <span className={dotClass} aria-hidden="true" />
    <span className="rail-tool-name">{name}</span>
    {target && <span className="rail-tool-target">{target}</span>}
  </>;
  if (!content) return <div className={`rail-tool${failed ? ' error' : ''}`}><div className="rail-tool-row">{label}</div></div>;
  // 失败行默认展开：错误内容直接可见，无需多点一次
  return <details className={`rail-tool${failed ? ' error' : ''}`} open={failed}>
    <summary>{label}<span className="rail-chev" aria-hidden="true">›</span></summary>
    <pre className="rail-pre">{content}</pre>
  </details>;
}

/** 静息工作段竖轨：一条竖线贯穿过程区，思考与工具是轨道上的节点
 * （紫=思考 / 绿=工具 / 红=失败）。同段多段思考合并为一个思考节点；
 * 对话模式默认折叠，轨迹模式（expanded）默认全部展开；含失败时自动展开。 */
function ActivityRail({ items, expanded }: { items: ActivityItem[]; expanded: boolean }) {
  const reasoning = items.flatMap((item) => item.kind === 'reasoning' ? [item.text] : []).join('\n\n');
  const tools = items.filter((item): item is Extract<ActivityItem, { kind: 'tool' }> => item.kind === 'tool');
  const errorCount = tools.filter((item) => item.result?.is_error).length;
  return (
    <div className="activity-rail">
      {reasoning.trim() !== '' && (
        <details className="rail-node think" open={expanded}>
          <summary><span className="rail-label">思考</span><span className="rail-chev" aria-hidden="true">›</span></summary>
          <div className="rail-body"><div className="rail-think-content">{reasoning}</div></div>
        </details>
      )}
      {tools.length > 0 && (
        <details className={`rail-node tool${errorCount > 0 ? ' error' : ''}`} open={expanded || errorCount > 0}>
          <summary>
            <span className="rail-label">{tools.length} 项操作{errorCount > 0 ? ` · ${errorCount} 项失败` : ''}</span>
            <span className="rail-names">{summarizeToolNames(items)}</span>
            <span className="rail-chev" aria-hidden="true">›</span>
          </summary>
          <div className="rail-body">
            {tools.map((item, index) => (
              <RailToolRow key={item.call.id || `tool-${index}`}
                name={item.call.name} target={toolTarget(item.call)}
                failed={Boolean(item.result?.is_error)} pending={!item.result}
                content={item.result?.content} />
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

/** 实时竖轨：回合进行中以呼吸节点呈现流式思考与工具调用（状态实时翻转），
 *  回合结束（快照接管）后由静息竖轨接替，不会残留。 */
function LiveActivityRail({ reasoningText, hasReasoning, tools }: {
  reasoningText: string; hasReasoning: boolean; tools: ToolCallState[];
}) {
  const runningCount = tools.filter((tool) => tool.status === 'running').length;
  const failedCount = tools.filter((tool) => tool.status === 'failed').length;
  return (
    <div className="activity-rail live">
      {hasReasoning && (
        <details className="rail-node think" open>
          <summary><span className="rail-label">思考中</span><span className="rail-chev" aria-hidden="true">›</span></summary>
          <div className="rail-body"><div className="rail-think-content">{reasoningText.trim() !== '' ? reasoningText : '等待内容…'}</div></div>
        </details>
      )}
      {tools.length > 0 && (
        <details className={`rail-node tool${failedCount > 0 ? ' error' : ''}`} open>
          <summary>
            <span className="rail-label">
              {tools.length} 项操作{runningCount > 0 ? ` · ${runningCount} 进行中` : ''}{failedCount > 0 ? ` · ${failedCount} 项失败` : ''}
            </span>
            <span className="rail-chev" aria-hidden="true">›</span>
          </summary>
          <div className="rail-body">
            {tools.map((tool) => (
              <RailToolRow key={tool.tool_use_id}
                name={tool.name} target={tool.target}
                failed={tool.status === 'failed'} pending={tool.status === 'running'}
                content={tool.status === 'failed' ? (tool.error_message ?? tool.result_summary) : tool.result_summary} />
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

// 贴底判定阈值（像素）：容忍滚动圆整与惯性滚动的末端过冲。
const BOTTOM_STICK_THRESHOLD = 32;

export function ConversationView({ snapshot, parts, tools = {}, showActivity = true, onInspect, onFork, exportUrl, sendSignal = 0, navigationHost }: {
  snapshot: SessionSnapshot | null;
  parts: Record<string, StreamingPart>;
  /** 工具调用的实时聚合（tool.started/completed/failed 事件流）：驱动实时竖轨的工具行 */
  tools?: Record<string, ToolCallState>;
  showActivity?: boolean;
  onInspect: (partID: string) => void;
  /** 分叉当前会话（未提供时隐藏分叉按钮） */
  onFork?: () => void;
  /** 会话导出（JSON 下载）地址（未提供时隐藏导出按钮） */
  exportUrl?: string;
  /** 发送信号：Composer 每次提交消息时递增，触发强制回底（挂载时不触发） */
  sendSignal?: number;
  navigationHost?: HTMLElement | null;
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
  const currentTurnID = useConversationNavigation(scrollEl, stickToBottom);
  const navigationItems = buildTurnNavigation(snapshot?.turns ?? [], parts);
  const selectTurn = useCallback((turnID: string, focusTarget: boolean) => {
    const el = nodeRef.current;
    if (!el) return;
    const anchor = turnAnchors(el).get(turnID);
    if (!anchor) return;
    stickRef.current = false;
    setStickToBottom(false);
    el.scrollTop = Math.max(0, anchor.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop - 24);
    if (focusTarget) anchor.focus({ preventScroll: true });
  }, []);

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
  // 状态在渲染期按 prev 对比模式同步（React 推荐做法，避免 effect 内级联 setState）；
  // ref 与 DOM 滚动仍留在 layout effect。首挂载不触发：state/ref 以当前信号为基线。
  const [lastSendSignal, setLastSendSignal] = useState(sendSignal);
  if (lastSendSignal !== sendSignal) {
    setLastSendSignal(sendSignal);
    setStickToBottom(true);
    setPendingCount(0);
  }
  const lastSendSignalRef = useRef(sendSignal);
  useLayoutEffect(() => {
    if (lastSendSignalRef.current === sendSignal) return;
    lastSendSignalRef.current = sendSignal;
    stickRef.current = true;
    const el = nodeRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [sendSignal]);

  const jumpToLatest = scrollToBottomNow;

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
  // 对话模式下 reasoning 阶段（模型长考、正文未出）也给出即时反馈：实时竖轨，
  // 回合结束或快照接管后自动消失，不会在结尾残留。
  const liveReasoning = Object.values(parts).filter((part) => part.kind === 'reasoning' && !snapshotContentTurns.has(part.turn_id) && snapshot.active_turn_id === part.turn_id);
  // 实时竖轨的工具行：tools 通道跨回合累积，按当前回合过滤；快照接管后退出实时态。
  const liveTools = Object.values(tools).filter((tool) => tool.turn_id !== undefined && tool.turn_id === snapshot.active_turn_id && !snapshotContentTurns.has(tool.turn_id));
  return <div className="conversation-wrap">
    <div className="conversation-view" ref={scrollRef}>
    <div className="conversation-content">
    {blocks.map((block) => {
      if (block.type === 'user-text') {
        // 气泡只承载正文；时间戳等页脚信息与操作条放在气泡外右下角。
        return <article className="message user" key={block.key} data-turn-id={block.turnID} data-turn-question tabIndex={-1}>
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
        return <article className="message assistant" key={block.key} data-turn-id={block.turnID} tabIndex={-1}>
          <div className="message-role">Paw</div>
          <MarkdownContent text={block.text} />
          <div className="message-footer">
            <MessageActions text={block.text} onFork={onFork} exportUrl={exportUrl} />
            {block.meta && <MessageMetaRow meta={block.meta} />}
          </div>
        </article>;
      }
      // 工作段竖轨在两个标签下都渲染：对话模式默认折叠，轨迹模式（showActivity）默认全部展开。
      return <ActivityRail key={block.key} items={block.items} expanded={showActivity} />;
    })}
    {streamingTexts.map((part, index) => {
      const turn = snapshot.turns.find((item) => item.turn_id === part.turn_id);
      const active = snapshot.active_turn_id === part.turn_id;
      const isLast = index === streamingTexts.length - 1;
      return <LiveTextBubble key={`stream-${part.part_id}`} part={part} active={active} startedAt={turn?.started_at} showMeta={isLast} onPumpState={onPumpState} />;
    })}
    {/* 实时竖轨：对话模式下进行中回合的思考与工具以呼吸节点呈现（正文由打字机气泡呈现）。 */}
    {!showActivity && (liveReasoning.length > 0 || liveTools.length > 0) && (
      <LiveActivityRail
        reasoningText={liveReasoning.map((part) => part.text).join('\n\n')}
        hasReasoning={liveReasoning.length > 0}
        tools={liveTools} />
    )}
    {/* 过程卡：轨迹模式展示全部流式片段，点击打开详情抽屉。 */}
    {showActivity && Object.values(parts).map((part) => <button className={`process-card ${part.kind}`} type="button" onClick={() => onInspect(part.part_id)} key={part.part_id}>
      <span>{part.kind === 'reasoning' ? '思考过程' : '实时响应'}</span><small>{part.text.slice(0, 140) || '等待内容…'}</small>
    </button>)}
    </div>
    </div>
    <TurnNavigator items={navigationItems} currentTurnID={currentTurnID} onSelect={selectTurn}
      navigationHost={navigationHost} hasEarlier={Boolean(snapshot.earlier_cursor)} />
    {!stickToBottom && (
      <button type="button" className="scroll-to-latest" title="回到底部" onClick={jumpToLatest}>
        {pendingCount > 0 ? `↓ ${pendingCount} 条新消息` : '↓ 回到最新'}
      </button>
    )}
  </div>;
}
