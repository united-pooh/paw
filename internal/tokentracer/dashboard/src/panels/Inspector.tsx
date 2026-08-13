import { useMemo } from 'react';
import { useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import type { TraceEvent, TimelineRow, TraceSnapshot } from '../trace/types';
import { formatCount, formatDuration, formatPercent, formatThroughput, tokenTotal, usageParts } from '../trace/format';

export interface InspectorProps {
  snapshot?: TraceSnapshot | null;
}

export function Inspector({ snapshot }: InspectorProps) {
  const traceState = useTraceState();
  const data = snapshot ?? traceState.snapshot;
  const selectionState = useSelection();
  const { selection } = useStore();

  const rowsById = useMemo(() => {
    const map = new Map<string, TimelineRow>();
    if (data !== null) {
      for (const row of data.timeline.rows) {
        map.set(row.id, row);
      }
    }
    return map;
  }, [data]);

  const eventsBySeq = useMemo(() => {
    const map = new Map<number, TraceEvent>();
    if (data !== null) {
      for (const event of data.events) {
        map.set(event.seq, event);
      }
    }
    return map;
  }, [data]);

  if (data === null) {
    return <div className="inspector"><div className="empty-state">正在加载追踪数据…</div></div>;
  }

  const selectedRow = selectionState.selectedRowID !== null ? rowsById.get(selectionState.selectedRowID) ?? null : null;
  const selectedEvent = selectionState.selectedEventSeq !== null ? eventsBySeq.get(selectionState.selectedEventSeq) ?? null : null;
  const range = selectionState.selectedTimeRange;

  if (selectedRow === null && selectedEvent === null && range === null) {
    return (
      <div className="inspector">
        <div className="empty-state">选择调用、事件或时间桶查看详情</div>
      </div>
    );
  }

  const intersectingRows =
    range !== null
      ? data.timeline.rows.filter((row) => {
          const start = Date.parse(row.start_time);
          const end = Date.parse(row.end_time);
          return end >= range.startMS && start <= range.endMS;
        })
      : [];

  return (
    <div className="inspector">
      {selectedRow !== null ? (
        <RowDetails row={selectedRow} data={data} eventsBySeq={eventsBySeq} />
      ) : null}
      {selectedEvent !== null ? (
        <EventDetails
          event={selectedEvent}
          data={data}
          rowsById={rowsById}
          onSelectRow={(rowID) => selection.selectRow(rowID, 'inspector')}
        />
      ) : null}
      {range !== null ? (
        <section className="inspector-section">
          <h3>时间范围 {formatDuration(range.startMS)} – {formatDuration(range.endMS)}</h3>
          {intersectingRows.length === 0 ? (
            <div className="empty-state">该范围内没有调用</div>
          ) : (
            <div className="row-list">
              {intersectingRows.map((row) => (
                <button
                  key={row.id}
                  type="button"
                  className="row-item"
                  onClick={() => selection.selectRow(row.id, 'inspector')}
                >
                  <span className="k">{row.display_name ?? row.name}</span>
                  <span className="v">{formatCount(tokenTotal(row.usage))} tokens</span>
                </button>
              ))}
            </div>
          )}
        </section>
      ) : null}
    </div>
  );
}

function RowDetails({ row, data, eventsBySeq }: { row: TimelineRow; data: TraceSnapshot; eventsBySeq: Map<number, TraceEvent> }) {
  const parts = usageParts(row.usage);
  const relatedEvents = Array.from(eventsBySeq.values()).filter((event) => {
    const eventData = event.data ?? {};
    return (
      (typeof eventData.stage_id === 'string' && eventData.stage_id === row.stage_id) ||
      (typeof eventData.agent_id === 'string' && eventData.agent_id === row.agent_id)
    );
  });
  return (
    <section className="inspector-section">
      <h3>{row.display_name ?? row.name}</h3>
      <div className="inspector-grid">
        <Field k="ID" v={row.id} />
        <Field k="类型" v={row.kind} />
        <Field k="阶段" v={row.stage_name ?? row.stage_id ?? '—'} />
        <Field k="Agent" v={row.agent_id ?? '—'} />
        <Field k="角色" v={row.role ?? '—'} />
        <Field k="会话" v={row.session_id ?? '—'} />
        <Field k="Provider" v={row.provider ?? '—'} />
        <Field k="模型" v={row.model ?? '—'} />
        <Field k="调用次数" v={formatCount(row.calls)} />
        <Field k="状态" v={row.status} />
        <Field k="开始" v={formatDuration(Date.parse(row.start_time))} />
        <Field k="结束" v={formatDuration(Date.parse(row.end_time))} />
        <Field k="耗时" v={formatDuration(row.duration_ms)} />
        <Field k="总 Token" v={formatCount(row.token_grand_total)} />
        <Field k="占比" v={formatPercent(row.token_share)} />
        <Field k="吞吐" v={formatThroughput(row.token_grand_total / Math.max(row.duration_ms / 1000, 0.001))} />
      </div>
      <div className="token-parts">
        {parts.map((part) => (
          <span key={part.key} className="part">
            <span className="swatch" style={{ background: `var(--${part.key})` }} />
            <span className="part-label">{part.label}</span>
            <span className="part-value">{formatCount(part.value)}</span>
          </span>
        ))}
      </div>
      {row.error !== undefined && row.error !== '' ? <p className="error-text">{row.error}</p> : null}
      {row.markers !== undefined && row.markers.length > 0 ? (
        <ul className="marker-list">
          {row.markers.map((marker, index) => (
            <li key={index}>
              {marker.label} · {formatDuration(Date.parse(marker.time))}
              {marker.detail !== undefined && marker.detail !== '' ? ` · ${marker.detail}` : ''}
            </li>
          ))}
        </ul>
      ) : null}
      {relatedEvents.length > 0 ? (
        <>
          <h3>相关事件</h3>
          <ul className="event-list">
            {relatedEvents.slice(0, 20).map((event) => (
              <li key={event.seq}>
                #{event.seq} {event.type} · {formatDuration(Date.parse(event.timestamp))}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <h3>运行</h3>
      <div className="inspector-grid">
        <Field k="Pipeline" v={data.pipeline.name} />
        <Field k="Run ID" v={data.run_id} />
        <Field k="调用数" v={formatCount(data.pipeline.calls)} />
        <Field k="总 Token" v={formatCount(data.timeline.token_grand_total)} />
      </div>
      {data.timeline.error !== undefined && data.timeline.error !== '' ? (
        <p className="error-text">{data.timeline.error}</p>
      ) : null}
    </section>
  );
}

function Field({ k, v }: { k: string; v: string }) {
  return (
    <div className="field">
      <span className="k">{k}</span>
      <span className="v" title={v}>{v}</span>
    </div>
  );
}

function EventDetails({
  event,
  data,
  rowsById,
  onSelectRow,
}: {
  event: TraceEvent;
  data: TraceSnapshot;
  rowsById: Map<string, TimelineRow>;
  onSelectRow: (rowID: string) => void;
}) {
  const eventData = event.data ?? {};
  const relatedRowID =
    typeof eventData.agent_id === 'string' && typeof eventData.stage_id === 'string'
      ? `agent:${eventData.stage_id}:${eventData.agent_id}:0`
      : typeof eventData.stage_id === 'string'
        ? `stage:${eventData.stage_id}`
        : undefined;
  const relatedRow = relatedRowID !== undefined ? rowsById.get(relatedRowID) ?? null : null;
  return (
    <section className="inspector-section">
      <h3>事件 #{event.seq} · {event.type}</h3>
      <div className="inspector-grid">
        <Field k="序号" v={String(event.seq)} />
        <Field k="类型" v={event.type} />
        <Field k="时间" v={event.timestamp} />
      </div>
      {Object.keys(eventData).length > 0 ? (
        <ul className="marker-list">
          {Object.entries(eventData).map(([key, value]) => (
            <li key={key}>{key}: {typeof value === 'string' ? value : JSON.stringify(value)}</li>
          ))}
        </ul>
      ) : null}
      {relatedRow !== null ? (
        <button type="button" className="row-item" onClick={() => onSelectRow(relatedRow.id)}>
          <span className="k">关联调用</span>
          <span className="v">{relatedRow.display_name ?? relatedRow.name}</span>
        </button>
      ) : null}
    </section>
  );
}
