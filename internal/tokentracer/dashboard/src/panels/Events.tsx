import { useMemo, useRef, useState } from 'react';
import { useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import { compactEvents } from '../trace/projections';
import type { CompactedEvent } from '../trace/projections';
import type { TraceSnapshot } from '../trace/types';
import { formatDuration } from '../trace/format';
import { VirtualList } from '../components/VirtualList';
import type { VirtualListHandle } from '../components/VirtualList';

export interface EventsProps {
  snapshot?: TraceSnapshot | null;
}

const ROW_HEIGHT = 28;

export function Events({ snapshot }: EventsProps) {
  const traceState = useTraceState();
  const data = snapshot ?? traceState.snapshot;
  const selectionState = useSelection();
  const { selection } = useStore();
  const [typeFilter, setTypeFilter] = useState('');
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [expandedSeq, setExpandedSeq] = useState<number | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);
  const virtualRef = useRef<VirtualListHandle>(null);

  const events = useMemo(() => {
    if (data === null) {
      return [];
    }
    const compacted = compactEvents(data.events);
    const needle = typeFilter.trim().toLowerCase();
    return compacted.filter((event) => {
      if (errorsOnly && !event.type.includes('error') && !event.type.includes('failed') && event.data?.error === undefined) {
        return false;
      }
      if (needle !== '' && !event.type.toLowerCase().includes(needle)) {
        return false;
      }
      return true;
    });
  }, [data, typeFilter, errorsOnly]);

  if (data === null) {
    return <div className="events-panel"><div className="empty-state">正在加载追踪数据…</div></div>;
  }

  const listHeight = Math.max(listRef.current?.getBoundingClientRect().height ?? 400, 60);

  const selectEvent = (event: CompactedEvent): void => {
    selection.selectEvent(event.seq, event.relatedRowID ?? null, 'events');
    setExpandedSeq((current) => (current === event.seq ? null : event.seq));
  };

  return (
    <div className="events-panel">
      <div className="events-filters">
        <input
          type="text"
          aria-label="按事件类型筛选"
          placeholder="筛选事件类型…"
          value={typeFilter}
          onChange={(event) => setTypeFilter(event.target.value)}
        />
        <label>
          <input type="checkbox" checked={errorsOnly} onChange={(event) => setErrorsOnly(event.target.checked)} />
          仅异常
        </label>
        <span className="spacer" />
        <span>{events.length} 条</span>
      </div>
      <div className="events-list" ref={listRef}>
        <VirtualList
          listRef={virtualRef}
          items={events}
          rowHeight={ROW_HEIGHT}
          height={listHeight}
          overscan={8}
          getKey={(event) => String(event.seq)}
          renderRow={(event, index) => (
            <EventRow
              event={event}
              index={index}
              expanded={expandedSeq === event.seq}
              selected={selectionState.selectedEventSeq === event.seq}
              onSelect={() => selectEvent(event)}
            />
          )}
        />
      </div>
    </div>
  );
}

function EventRow({
  event,
  index,
  expanded,
  selected,
  onSelect,
}: {
  event: CompactedEvent;
  index: number;
  expanded: boolean;
  selected: boolean;
  onSelect: () => void;
}) {
  const data = event.data ?? {};
  const error =
    typeof data.error === 'string'
      ? redactSensitiveText(data.error)
      : Object.keys(data).length > 0
        ? redactSensitiveText(JSON.stringify(data))
        : null;
  const failed = event.type.includes('error') || event.type.includes('failed') || error !== null;
  const summary = summaryText(event);
  return (
    <div>
      <div
        role="row"
        aria-selected={selected}
        aria-label={`${event.type} ${summary}`}
        className={`event-row${failed ? ' failed' : ''}`}
        onClick={onSelect}
        style={{ height: ROW_HEIGHT }}
      >
        <span className="ev-time">{formatDuration(Date.parse(event.timestamp))}</span>
        <span className="ev-type">{event.type}</span>
        <span className="ev-summary">{summary}</span>
        {event.hiddenCount > 0 ? (
          <span className="ev-hidden">隐藏 {event.hiddenCount} 条重复事件</span>
        ) : null}
      </div>
      {expanded ? (
        <div className="event-detail">
          <div>#{event.seq} · {event.timestamp}</div>
          {error !== null ? <div className="error-text">{error}</div> : null}
          {error !== null ? (
            <button type="button" aria-label="复制错误详情" onClick={() => void copyText(error)}>
              复制错误详情
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function summaryText(event: CompactedEvent): string {
  const data = event.data ?? {};
  if (typeof data.error === 'string') {
    return redactSensitiveText(data.error);
  }
  const fields = ['tool', 'name', 'provider', 'model', 'agent_id', 'stage_id', 'status'];
  for (const key of fields) {
    if (typeof data[key] === 'string' && data[key] !== '') {
      return `${key}=${data[key]}`;
    }
  }
  return '';
}

export function redactSensitiveText(text: string): string {
  return text.replace(/(authorization|api_key|apikey|token|secret)\s*[:=]\s*[^\s,;]+/gi, '[REDACTED]');
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // clipboard unavailable; the copy button remains a no-op
  }
}
