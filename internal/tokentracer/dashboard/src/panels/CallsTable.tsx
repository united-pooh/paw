import { useMemo, useRef, useState } from 'react';
import type { KeyboardEvent } from 'react';
import { useFilters, useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import { projectRows } from '../trace/projections';
import type { ProjectedRow } from '../trace/projections';
import type { TraceSnapshot } from '../trace/types';
import { formatCount, formatDuration, formatThroughput, tokenTotal } from '../trace/format';
import { VirtualList } from '../components/VirtualList';
import type { VirtualListHandle } from '../components/VirtualList';

type SortKey = 'start' | 'name' | 'duration' | 'tokens' | 'output' | 'throughput' | 'status';

interface SortState {
  key: SortKey;
  dir: 'asc' | 'desc';
}

const NUMERIC_KEYS: SortKey[] = ['duration', 'tokens', 'output', 'throughput'];

const ROW_HEIGHT = 24;

export interface CallsTableProps {
  snapshot?: TraceSnapshot | null;
}

function statusGlyph(status: string): { glyph: string; className: string } {
  if (status === 'failed') {
    return { glyph: '!', className: 'status-bad' };
  }
  if (status === 'completed') {
    return { glyph: '✓', className: 'status-ok' };
  }
  return { glyph: '…', className: 'status-live' };
}

function compareRows(a: ProjectedRow, b: ProjectedRow, key: SortKey): number {
  switch (key) {
    case 'start':
      return a.startMS - b.startMS;
    case 'name':
      return (a.display_name ?? a.name).localeCompare(b.display_name ?? b.name);
    case 'duration':
      return a.duration_ms - b.duration_ms;
    case 'tokens':
      return a.token_grand_total - b.token_grand_total;
    case 'output':
      return a.usage.output - b.usage.output;
    case 'throughput':
      return a.throughput - b.throughput;
    case 'status':
      return a.status.localeCompare(b.status);
  }
}

interface HeaderCell {
  label: string;
  sortKey?: SortKey;
  ariaLabel?: string;
  align: 'left' | 'right';
}

const HEADER: HeaderCell[] = [
  { label: '#', sortKey: 'start', ariaLabel: '按开始排序', align: 'left' },
  { label: '调用', sortKey: 'name', ariaLabel: '按名称排序', align: 'left' },
  { label: '时间轴', sortKey: 'duration', ariaLabel: '按耗时排序', align: 'left' },
  { label: 'Token 构成', sortKey: 'tokens', ariaLabel: '按总 Token 排序', align: 'left' },
  { label: '输入', align: 'right' },
  { label: '缓存读', align: 'right' },
  { label: '缓存建', align: 'right' },
  { label: '输出', sortKey: 'output', ariaLabel: '按输出排序', align: 'right' },
  { label: '吞吐', sortKey: 'throughput', ariaLabel: '按吞吐排序', align: 'right' },
  { label: '状态', sortKey: 'status', ariaLabel: '按状态排序', align: 'left' },
];

export function CallsTable({ snapshot }: CallsTableProps) {
  const traceState = useTraceState();
  const data = snapshot ?? traceState.snapshot;
  const selectionState = useSelection();
  const { selection } = useStore();
  const filters = useFilters();
  const { openPanel } = useStore();
  const [sort, setSort] = useState<SortState>({ key: 'start', dir: 'asc' });
  const virtualRef = useRef<VirtualListHandle>(null);

  const rows = useMemo(() => {
    if (data === null) {
      return [];
    }
    const projected = projectRows(data.timeline.rows, filters, selectionState.selectedTimeRange);
    const direction = sort.dir === 'asc' ? 1 : -1;
    projected.sort((a, b) => compareRows(a, b, sort.key) * direction);
    return projected;
  }, [data, filters, selectionState.selectedTimeRange, sort]);

  const clickSort = (key: SortKey): void => {
    setSort((current) => {
      if (current.key === key) {
        return { key, dir: current.dir === 'asc' ? 'desc' : 'asc' };
      }
      return { key, dir: NUMERIC_KEYS.includes(key) ? 'desc' : 'asc' };
    });
  };

  if (data === null) {
    return (
      <div className="calls-table">
        <div className="ct-rows">
          <div className="empty-state">正在加载追踪数据…</div>
        </div>
      </div>
    );
  }

  const timelineStart = Date.parse(data.timeline.start_time ?? data.pipeline.start_time);
  const timelineEnd = Date.parse(data.timeline.end_time ?? data.timeline.start_time ?? data.pipeline.start_time);
  const timelineSpan = Math.max(timelineEnd - timelineStart, 1);
  const selectedIndex = rows.findIndex((row) => row.id === selectionState.selectedRowID);

  const moveSelection = (event: KeyboardEvent): void => {
    if (rows.length === 0 || (event.key !== 'ArrowDown' && event.key !== 'ArrowUp')) {
      return;
    }
    event.preventDefault();
    const base = Math.max(0, selectedIndex);
    const next = event.key === 'ArrowDown' ? Math.min(rows.length - 1, base + 1) : Math.max(0, base - 1);
    selection.selectRow(rows[next].id, 'calls');
    virtualRef.current?.scrollIntoView(next);
  };

  const activateRow = (row: ProjectedRow): void => {
    selection.selectRow(row.id, 'calls');
    openPanel('inspector');
  };

  return (
    <div className="calls-table" onKeyDown={moveSelection} tabIndex={0}>
      <div className="ct-header" role="row">
        {HEADER.map((column, index) => (
          <div key={index} className={`th th-${index}`} role="columnheader">
            {column.sortKey !== undefined ? (
              <button
                type="button"
                aria-label={column.ariaLabel}
                aria-pressed={sort.key === column.sortKey}
                onClick={() => clickSort(column.sortKey!)}
              >
                {column.label}
                {sort.key === column.sortKey ? (sort.dir === 'asc' ? ' ↑' : ' ↓') : ''}
              </button>
            ) : (
              column.label
            )}
          </div>
        ))}
      </div>
      <div className="ct-rows">
        <VirtualList
          listRef={virtualRef}
          items={rows}
          rowHeight={ROW_HEIGHT}
          height={300}
          overscan={6}
          getKey={(row) => row.id}
          renderRow={(row, index) => (
            <div
              role="row"
              aria-selected={row.id === selectionState.selectedRowID}
              aria-label={row.display_name ?? row.name}
              data-in-range={row.inRange}
              className="ct-row"
              onClick={() => selection.selectRow(row.id, 'calls')}
              onDoubleClick={() => activateRow(row)}
              title={`${row.display_name ?? row.name}\n开始 ${formatDuration(row.startMS)}\n耗时 ${formatDuration(row.duration_ms)}\n总 Token ${formatCount(row.token_grand_total)}`}
            >
              <div className="cell">{index + 1}</div>
              <div className="cell name">
                {row.display_name ?? row.name}
                <span className="kind-tag">{row.kind}</span>
              </div>
              <div className="cell">
                <div className="mini-time" title={`${formatDuration(row.startMS)} → ${formatDuration(row.endMS)}`}>
                  <div
                    className={`bar${row.status === 'failed' ? ' failed' : ''}`}
                    style={{
                      left: `${((row.startMS - timelineStart) / timelineSpan) * 100}%`,
                      width: `${Math.max((row.duration_ms / timelineSpan) * 100, 0.4)}%`,
                    }}
                  />
                </div>
              </div>
              <div className="cell">
                <TokenStack row={row} />
              </div>
              <div className="cell num" title={`input ${formatCount(row.usage.input)}`}>
                {formatCount(row.usage.input)}
              </div>
              <div className="cell num" title={`cache read ${formatCount(row.usage.cache_read)}`}>
                {formatCount(row.usage.cache_read)}
              </div>
              <div className="cell num" title={`cache creation ${formatCount(row.usage.cache_creation)}`}>
                {formatCount(row.usage.cache_creation)}
              </div>
              <div className="cell num" title={`output ${formatCount(row.usage.output)}`}>
                {formatCount(row.usage.output)}
              </div>
              <div className="cell num" title={`${formatThroughput(row.throughput)}`}>
                {formatThroughput(row.throughput)}
              </div>
              <div className="cell status-glyph">
                <span
                  className={statusGlyph(row.status).className}
                  title={row.status === 'failed' && row.error !== undefined ? row.error : row.status}
                >
                  {statusGlyph(row.status).glyph}
                  <span className="sr-only">{row.status}</span>
                </span>
              </div>
            </div>
          )}
        />
      </div>
    </div>
  );
}

function TokenStack({ row }: { row: ProjectedRow }) {
  const total = Math.max(tokenTotal(row.usage), 1);
  const parts = [
    { key: 'input', value: row.usage.input },
    { key: 'cache_read', value: row.usage.cache_read },
    { key: 'cache_creation', value: row.usage.cache_creation },
    { key: 'output', value: row.usage.output },
  ];
  return (
    <div
      className="token-stack"
      title={`input ${formatCount(row.usage.input)} · cache read ${formatCount(row.usage.cache_read)} · cache creation ${formatCount(row.usage.cache_creation)} · output ${formatCount(row.usage.output)}`}
    >
      {parts.map((part) =>
        part.value > 0 ? (
          <div key={part.key} className={`seg ${part.key}`} style={{ width: `${(part.value / total) * 100}%` }} />
        ) : null,
      )}
    </div>
  );
}
