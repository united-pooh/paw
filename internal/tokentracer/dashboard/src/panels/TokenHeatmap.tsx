import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent, WheelEvent } from 'react';
import { useFilters, useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import { buildHeatmap, projectRows } from '../trace/projections';
import type { HeatRow } from '../trace/projections';
import type { TimeRange, TraceSnapshot } from '../trace/types';
import { formatCount, formatDuration, formatThroughput, tokenTotal } from '../trace/format';

export interface TokenHeatmapProps {
  snapshot?: TraceSnapshot | null;
}

const TOKEN_COLOR = '143, 62, 41';
const FAILED_COLOR = '161, 45, 33';
const SELECTED_COLOR = '158, 69, 47';
const BAND_COLOR = '221, 220, 213';

interface BrushState {
  startX: number;
  startY: number;
  endX: number;
  endY: number;
}

interface HoverState {
  x: number;
  y: number;
  text: string;
}

export function TokenHeatmap({ snapshot }: TokenHeatmapProps) {
  const traceState = useTraceState();
  const data = snapshot ?? traceState.snapshot;
  const selectionState = useSelection();
  const { selection } = useStore();
  const filters = useFilters();
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [domain, setDomain] = useState<TimeRange | null>(null);
  const [brush, setBrush] = useState<BrushState | null>(null);
  const [hover, setHover] = useState<HoverState | null>(null);
  const brushStart = useRef<{ x: number; y: number } | null>(null);
  const rafRef = useRef<number | null>(null);

  const timelineStart = data !== null ? Date.parse(data.timeline.start_time ?? data.pipeline.start_time) : 0;
  const timelineEnd =
    data !== null
      ? Date.parse(data.timeline.end_time ?? data.timeline.start_time ?? data.pipeline.start_time)
      : 1;
  const effectiveDomain: TimeRange | null = useMemo(() => {
    if (domain !== null && timelineEnd > timelineStart) {
      return domain;
    }
    return { startMS: timelineStart, endMS: Math.max(timelineEnd, timelineStart + 1) };
  }, [domain, timelineStart, timelineEnd]);

  const heatRows: HeatRow[] = useMemo(() => {
    if (data === null || size.width <= 0) {
      return [];
    }
    const rows = projectRows(data.timeline.rows, filters, null);
    return buildHeatmap(data, rows, Math.floor(size.width));
  }, [data, filters, size.width]);

  useLayoutEffect(() => {
    const wrap = wrapRef.current;
    if (wrap === null) {
      return;
    }
    const rect = wrap.getBoundingClientRect();
    setSize((current) =>
      current.width === rect.width && current.height === rect.height ? current : { width: rect.width, height: rect.height },
    );
  }, [data]);

  useEffect(() => {
    const wrap = wrapRef.current;
    if (wrap === null || typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(() => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
      }
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        const rect = wrap.getBoundingClientRect();
        setSize((current) =>
          current.width === rect.width && current.height === rect.height ? current : { width: rect.width, height: rect.height },
        );
      });
    });
    observer.observe(wrap);
    return () => {
      observer.disconnect();
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
      }
    };
  }, []);

  useEffect(() => {
    if (data !== null) {
      setDomain(null);
    }
  }, [data]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (canvas === null || data === null || size.width <= 0 || size.height <= 0) {
      return;
    }
    const dpr = typeof window !== 'undefined' && window.devicePixelRatio > 0 ? window.devicePixelRatio : 1;
    const backingWidth = Math.max(1, Math.round(size.width * dpr));
    const backingHeight = Math.max(1, Math.round(size.height * dpr));
    if (canvas.width !== backingWidth) {
      canvas.width = backingWidth;
    }
    if (canvas.height !== backingHeight) {
      canvas.height = backingHeight;
    }
    const context = canvas.getContext('2d');
    if (context === null) {
      return;
    }
    drawHeatmap(context, {
      width: size.width,
      height: size.height,
      dpr,
      heatRows,
      domain: effectiveDomain,
      selection: selectionState,
      brush,
      data,
    });
  }, [data, size, heatRows, effectiveDomain, selectionState, brush]);

  if (data === null) {
    return <div className="heatmap-wrap"><div className="empty-state">正在加载追踪数据…</div></div>;
  }

  const plotWidth = Math.max(size.width, 1);
  const plotHeight = Math.max(size.height, 1);
  const rowHeight = plotHeight / Math.max(heatRows.length, 1);

  const timeAt = (x: number): number => {
    const ratio = Math.min(1, Math.max(0, x / plotWidth));
    return effectiveDomain.startMS + ratio * (effectiveDomain.endMS - effectiveDomain.startMS);
  };

  const columnAt = (x: number): number => {
    const columns = Math.floor(plotWidth);
    return Math.min(columns - 1, Math.max(0, Math.floor((x / plotWidth) * columns)));
  };

  const handlePointerDown = (event: ReactPointerEvent<HTMLCanvasElement>): void => {
    const rect = event.currentTarget.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    brushStart.current = { x, y };
    setBrush({ startX: x, startY: y, endX: x, endY: y });
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // jsdom does not implement pointer capture
    }
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLCanvasElement>): void => {
    const rect = event.currentTarget.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    if (brushStart.current !== null) {
      setBrush((current) => (current === null ? current : { ...current, endX: x, endY: y }));
      return;
    }
    const rowIndex = Math.min(heatRows.length - 1, Math.max(0, Math.floor(y / rowHeight)));
    const column = columnAt(x);
    const heatRow = heatRows[rowIndex];
    const cell = heatRow?.cells.find((candidate) => candidate.column === column);
    if (cell === undefined) {
      setHover(null);
      return;
    }
    const usage = cell.usage;
    const lines = [
      `${formatDuration(cell.startMS)} – ${formatDuration(cell.endMS)}`,
      `总 Token ${formatCount(cell.tokenTotal)}`,
      `input ${formatCount(usage.input)} · cache read ${formatCount(usage.cache_read)} · cache creation ${formatCount(usage.cache_creation)} · output ${formatCount(usage.output)}`,
      `${heatRow.label} · ${formatThroughput(cell.tokenTotal / Math.max((cell.endMS - cell.startMS) / 1000, 0.001))}`,
    ];
    if (cell.status === 'failed') {
      lines.push('failed');
    }
    setHover({ x, y, text: lines.join('\n') });
  };

  const handlePointerUp = (event: ReactPointerEvent<HTMLCanvasElement>): void => {
    const rect = event.currentTarget.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    const start = brushStart.current;
    brushStart.current = null;
    if (start === null) {
      setBrush(null);
      return;
    }
    const dragged = Math.abs(x - start.x) >= 3 || Math.abs(y - start.y) >= 3;
    if (!dragged) {
      const column = columnAt(x);
      const cells = heatRows.flatMap((row) => row.cells.filter((cell) => cell.column === column));
      if (cells.length > 0) {
        const top = cells.reduce((best, cell) => (cell.tokenTotal > best.tokenTotal ? cell : best));
        selection.selectRange({ startMS: top.startMS, endMS: top.endMS }, 'heatmap');
        selection.selectRow(top.rowID, 'heatmap');
      }
    } else {
      const startMS = timeAt(start.x);
      const endMS = timeAt(x);
      selection.selectRange(
        startMS <= endMS ? { startMS, endMS } : { startMS: endMS, endMS: startMS },
        'heatmap',
      );
    }
    setBrush(null);
  };

  const zoom = (factor: number): void => {
    if (effectiveDomain === null) {
      return;
    }
    const span = effectiveDomain.endMS - effectiveDomain.startMS;
    const newSpan = Math.max(span * factor, 1);
    const center = (effectiveDomain.startMS + effectiveDomain.endMS) / 2;
    setDomain({
      startMS: Math.max(timelineStart, center - newSpan / 2),
      endMS: Math.min(timelineEnd, center + newSpan / 2),
    });
  };

  const handleWheel = (event: WheelEvent<HTMLCanvasElement>): void => {
    if (!event.ctrlKey && !event.metaKey) {
      return;
    }
    event.preventDefault();
    zoom(event.deltaY > 0 ? 1.25 : 0.8);
  };

  const selectedRowName =
    selectionState.selectedRowID !== null
      ? (data.timeline.rows.find((row) => row.id === selectionState.selectedRowID)?.display_name ??
        selectionState.selectedRowID)
      : null;
  const description = [
    'Token 活跃度热力图',
    selectedRowName !== null ? `选中行：${selectedRowName}` : '未选择行',
    selectionState.selectedTimeRange !== null
      ? `时间范围 ${formatDuration(selectionState.selectedTimeRange.startMS)} – ${formatDuration(selectionState.selectedTimeRange.endMS)}`
      : '无时间范围',
  ].join('；');

  return (
    <div className="heatmap-wrap" ref={wrapRef}>
      <canvas
        ref={canvasRef}
        className="heatmap-canvas"
        role="img"
        aria-label="Token Heatmap"
        aria-describedby="heatmap-desc"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerLeave={() => {
          if (brushStart.current === null) {
            setHover(null);
          }
        }}
        onWheel={handleWheel}
      />
      <div id="heatmap-desc" className="sr-only">{description}</div>
      <div className="heatmap-zoom">
        <button type="button" aria-label="放大时间轴" onClick={() => zoom(0.8)}>+</button>
        <button type="button" aria-label="缩小时间轴" onClick={() => zoom(1.25)}>−</button>
      </div>
      {hover !== null ? (
        <div className="heatmap-tooltip" style={{ left: Math.min(hover.x + 10, plotWidth - 160), top: hover.y + 10 }}>
          {hover.text.split('\n').map((line, index) => (
            <div key={index} className={index === 0 ? 'tt-title' : 'tt-muted'}>{line}</div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

interface DrawContext {
  width: number;
  height: number;
  dpr: number;
  heatRows: HeatRow[];
  domain: TimeRange | null;
  selection: { selectedRowID: string | null; selectedTimeRange: TimeRange | null };
  brush: BrushState | null;
  data: TraceSnapshot;
}

function drawHeatmap(context: CanvasRenderingContext2D, draw: DrawContext): void {
  const { width, height, dpr, heatRows, domain, selection, brush } = draw;
  context.save();
  context.scale(dpr, dpr);
  context.clearRect(0, 0, width, height);

  const rowHeight = height / Math.max(heatRows.length, 1);
  const columns = Math.max(1, Math.floor(width));
  let maxToken = 0;
  for (const row of heatRows) {
    for (const cell of row.cells) {
      maxToken = Math.max(maxToken, cell.tokenTotal);
    }
  }
  const logMax = Math.log(1 + Math.max(maxToken, 1));

  for (let rowIndex = 0; rowIndex < heatRows.length; rowIndex++) {
    const y = rowIndex * rowHeight;
    if (rowIndex % 2 === 1) {
      context.fillStyle = `rgba(${BAND_COLOR}, 0.035)`;
      context.fillRect(0, y, width, rowHeight);
    }
    for (const cell of heatRows[rowIndex].cells) {
      const intensity = Math.log(1 + cell.tokenTotal) / logMax;
      const alpha = 0.14 + 0.86 * intensity;
      context.fillStyle = `rgba(${TOKEN_COLOR}, ${alpha.toFixed(3)})`;
      context.fillRect(cell.column, y + 0.5, 1, Math.max(rowHeight - 1, 1));
      if (cell.status === 'failed') {
        context.strokeStyle = `rgba(${FAILED_COLOR}, 0.9)`;
        context.lineWidth = 1;
        context.beginPath();
        context.moveTo(cell.column, y + rowHeight - 1);
        context.lineTo(cell.column + 1, y);
        context.stroke();
        context.fillStyle = `rgba(${FAILED_COLOR}, 0.95)`;
        context.font = `${Math.max(9, Math.min(11, rowHeight - 4))}px ui-monospace, monospace`;
        context.fillText('!', cell.column - 2, y + rowHeight - 1);
      }
    }
    if (selection.selectedRowID !== null && heatRows[rowIndex].rowID === selection.selectedRowID) {
      context.strokeStyle = `rgba(${SELECTED_COLOR}, 0.9)`;
      context.lineWidth = 1;
      context.strokeRect(0.5, y + 0.5, width - 1, rowHeight - 1);
    }
  }

  if (selection.selectedTimeRange !== null && domain !== null && domain.endMS > domain.startMS) {
    const startRatio = (selection.selectedTimeRange.startMS - domain.startMS) / (domain.endMS - domain.startMS);
    const endRatio = (selection.selectedTimeRange.endMS - domain.startMS) / (domain.endMS - domain.startMS);
    const startX = Math.min(1, Math.max(0, startRatio)) * width;
    const endX = Math.min(1, Math.max(0, endRatio)) * width;
    if (startX > 0) {
      context.fillStyle = 'rgba(48, 51, 47, 0.55)';
      context.fillRect(0, 0, startX, height);
    }
    if (endX < width) {
      context.fillStyle = 'rgba(48, 51, 47, 0.55)';
      context.fillRect(endX, 0, width - endX, height);
    }
  }

  if (brush !== null) {
    const x = Math.min(brush.startX, brush.endX);
    const w = Math.abs(brush.endX - brush.startX);
    context.fillStyle = 'rgba(158, 69, 47, 0.18)';
    context.fillRect(x, 0, w, height);
    context.strokeStyle = 'rgba(158, 69, 47, 0.7)';
    context.strokeRect(x + 0.5, 0.5, w - 1, height - 1);
  }

  context.restore();
}
