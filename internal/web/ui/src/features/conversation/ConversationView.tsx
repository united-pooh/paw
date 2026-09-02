import type { MessagePart, SessionSnapshot, StreamingPart, ToolCall, ToolResult } from '../../api/types';
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
  | { type: 'user-text'; key: string; text: string; meta?: TurnMeta }
  | { type: 'assistant-text'; key: string; text: string; meta?: TurnMeta }
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
        blocks.push({ type: 'user-text', key: baseKey, text: message.content ?? '', meta: { started_at: meta.started_at, status: meta.status } });
      } else if (message.role === 'assistant' && content !== '') {
        flushActivity();
        blocks.push({ type: 'assistant-text', key: baseKey, text: message.content ?? '', meta: index === lastAssistantIndex ? meta : { started_at: meta.started_at, status: meta.status } });
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
          <details className="activity-reasoning" key={`reasoning-${index}`}>
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

export function ConversationView({ snapshot, parts, showActivity = true, onInspect }: { snapshot: SessionSnapshot | null; parts: Record<string, StreamingPart>; showActivity?: boolean; onInspect: (partID: string) => void }) {
  if (!snapshot) return <div className="empty-state">选择一个会话开始查看对话</div>;
  const blocks = buildBlocks(snapshot);
  return <div className="conversation-view">
    {blocks.map((block) => {
      if (block.type === 'user-text') {
        return <article className="message user" key={block.key}>
          <div className="message-role">你</div>
          <MarkdownContent text={block.text} />
          {block.meta && <MessageMetaRow meta={block.meta} />}
        </article>;
      }
      if (block.type === 'assistant-text') {
        return <article className="message assistant" key={block.key}>
          <div className="message-role">Paw</div>
          <MarkdownContent text={block.text} />
          {block.meta && <MessageMetaRow meta={block.meta} />}
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
    {Object.values(parts).map((part) => <button className={`process-card ${part.kind}`} type="button" onClick={() => onInspect(part.part_id)} key={part.part_id}>
      <span>{part.kind === 'reasoning' ? '思考过程' : '实时响应'}</span><small>{part.text.slice(0, 140) || '等待内容…'}</small>
    </button>)}
  </div>;
}
