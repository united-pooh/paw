import { useEffect, useMemo, useRef, useState } from 'react';
import { useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import { buildFlameTree, flattenFlame } from '../trace/projections';
import type { FlameNode } from '../trace/projections';
import type { TimeRange, TraceSnapshot } from '../trace/types';
import { formatCount, formatDuration } from '../trace/format';

export interface FoldedFlameProps {
  snapshot?: TraceSnapshot | null;
  width?: number;
}

type WidthMode = 'tokens' | 'duration';

const NODE_HEIGHT = 28;
const MAX_NODES = 800;

interface LaidNode {
  node: FlameNode;
  x: number;
  y: number;
  w: number;
}

export function FoldedFlame({ snapshot, width: widthProp }: FoldedFlameProps) {
  const traceState = useTraceState();
  const data = snapshot ?? traceState.snapshot;
  const selectionState = useSelection();
  const { selection } = useStore();
  const [mode, setMode] = useState<WidthMode>('tokens');
  const [drillStack, setDrillStack] = useState<string[]>([]);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const [measuredWidth, setMeasuredWidth] = useState<number | null>(null);

  const root = useMemo(() => (data === null ? null : buildFlameTree(data, mode)), [data, mode]);
  const nodesById = useMemo(() => {
    const map = new Map<string, FlameNode>();
    if (root !== null) {
      for (const node of flattenFlame(root)) {
        map.set(node.id, node);
      }
    }
    return map;
  }, [root]);

  useEffect(() => {
    const wrap = wrapRef.current;
    if (wrap === null || widthProp !== undefined) {
      return;
    }
    const measure = (): void => {
      const rect = wrap.getBoundingClientRect();
      setMeasuredWidth((current) => (current === rect.width ? current : rect.width));
    };
    measure();
    if (typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(measure);
      observer.observe(wrap);
      return () => observer.disconnect();
    }
    return undefined;
  }, [widthProp]);

  const width = widthProp ?? measuredWidth ?? 600;
  const drillId = drillStack.length > 0 ? drillStack[drillStack.length - 1] : null;
  const currentRoot = root === null ? null : drillId !== null ? (nodesById.get(drillId) ?? root) : root;

  const laid = useMemo(() => {
    if (currentRoot === null) {
      return [];
    }
    const weightOf = (node: FlameNode): number =>
      mode === 'tokens' ? Math.max(node.tokenTotal, 1) : Math.max(node.durationMS, 1);
    const result: LaidNode[] = [];
    const layout = (node: FlameNode, depth: number, parentWidth: number, offsetX: number): void => {
      if (result.length >= MAX_NODES) {
        return;
      }
      result.push({ node, x: offsetX, y: depth * NODE_HEIGHT, w: parentWidth });
      const children = capChildren(
        node.children,
        weightOf,
        Math.max(5, Math.min(40, MAX_NODES - result.length)),
      );
      const totalWeight = children.reduce((sum, child) => sum + weightOf(child), 0);
      let cursor = offsetX;
      for (const child of children) {
        if (result.length >= MAX_NODES) {
          break;
        }
        const childWidth = totalWeight > 0 ? (weightOf(child) / totalWeight) * parentWidth : parentWidth / Math.max(children.length, 1);
        layout(child, depth + 1, childWidth, cursor);
        cursor += childWidth;
      }
    };
    layout(currentRoot, 0, width, 0);
    return result;
  }, [currentRoot, width, mode]);

  if (data === null || currentRoot === null) {
    return <div className="flame-wrap" ref={wrapRef}><div className="empty-state">正在加载追踪数据…</div></div>;
  }

  const drill = (node: FlameNode): void => {
    if (node.children.length > 0) {
      setDrillStack((stack) => [...stack, node.id]);
    }
  };

  const drillUp = (): void => {
    setDrillStack((stack) => stack.slice(0, -1));
  };

  const selectNode = (node: FlameNode): void => {
    if (node.rowID !== undefined) {
      selection.selectRow(node.rowID, 'flame');
    }
  };

  const totalHeight = Math.max(
    60,
    Math.max(...laid.map((entry) => entry.y + NODE_HEIGHT)),
  );

  return (
    <div className="flame-wrap" ref={wrapRef}>
      <div className="panel-toolbar">
        <button
          type="button"
          aria-label="按 Token 宽度"
          aria-pressed={mode === 'tokens'}
          onClick={() => setMode('tokens')}
        >
          按 Token 宽度
        </button>
        <button
          type="button"
          aria-label="按耗时宽度"
          aria-pressed={mode === 'duration'}
          onClick={() => setMode('duration')}
        >
          按耗时宽度
        </button>
        <span className="spacer" />
        {drillId !== null ? (
          <button type="button" aria-label="返回上层" onClick={drillUp}>
            返回上层
          </button>
        ) : null}
      </div>
      <div className="flame-breadcrumb">
        <span>{currentRoot.label}</span>
        <span className="flame-crumb-muted">{mode === 'tokens' ? '· Token 宽度' : '· 耗时宽度'}</span>
      </div>
      <div className="flame-canvas" style={{ position: 'relative', flex: 1, minHeight: 0 }}>
        <svg
          className="flame-svg"
          width={width}
          height={totalHeight}
          role="img"
          aria-label="Folded Flame"
        >
          {laid.map((entry) => (
            <NodeRect
              key={entry.node.id}
              node={entry.node}
              x={entry.x}
              y={entry.y}
              w={entry.w}
              selected={selectionState.selectedRowID !== null && entry.node.rowID === selectionState.selectedRowID}
              inRange={inRange(entry.node, selectionState.selectedTimeRange)}
              onSelect={() => selectNode(entry.node)}
              onDrill={() => drill(entry.node)}
              isRootNode={entry.node.id === currentRoot.id}
            />
          ))}
        </svg>
        {laid
          .filter((entry) => entry.node.children.length > 0)
          .map((entry) => (
            <button
              key={`drill-${entry.node.id}`}
              type="button"
              className="flame-drill"
              aria-label={`进入 ${entry.node.label}`}
              style={{
                left: entry.x,
                top: entry.y,
                width: Math.min(entry.w, 140),
                height: NODE_HEIGHT,
              }}
              onClick={() => drill(entry.node)}
            />
          ))}
      </div>
    </div>
  );
}

function capChildren(children: FlameNode[], weightOf: (node: FlameNode) => number, budget: number): FlameNode[] {
  if (children.length <= budget) {
    return children;
  }
  const sorted = [...children].sort((a, b) => weightOf(a) - weightOf(b));
  const kept = sorted.slice(Math.max(0, sorted.length - (budget - 1)));
  const merged = sorted.slice(0, Math.max(0, sorted.length - (budget - 1)));
  const cluster: FlameNode = {
    id: 'cluster:overflow',
    kind: 'event_cluster',
    label: `其他 ${merged.length} 项`,
    status: merged.some((node) => node.status === 'failed') ? 'failed' : 'completed',
    startMS: 0,
    endMS: 0,
    durationMS: merged.reduce((sum, node) => sum + node.durationMS, 0),
    tokenTotal: merged.reduce((sum, node) => sum + node.tokenTotal, 0),
    usage: merged.reduce(
      (acc, node) => ({
        input: acc.input + node.usage.input,
        cache_read: acc.cache_read + node.usage.cache_read,
        cache_creation: acc.cache_creation + node.usage.cache_creation,
        output: acc.output + node.usage.output,
        total_context: acc.total_context + node.usage.total_context,
        cache_hit_rate: 0,
      }),
      { input: 0, cache_read: 0, cache_creation: 0, output: 0, total_context: 0, cache_hit_rate: 0 },
    ),
    children: [],
  };
  return [...kept, cluster];
}

function inRange(node: FlameNode, range: TimeRange | null): boolean {
  if (range === null) {
    return true;
  }
  return node.endMS >= range.startMS && node.startMS <= range.endMS;
}

interface NodeRectProps {
  node: FlameNode;
  x: number;
  y: number;
  w: number;
  selected: boolean;
  inRange: boolean;
  onSelect: () => void;
  onDrill: () => void;
  isRootNode?: boolean;
}

function NodeRect({ node, x, y, w, selected, inRange, onSelect, onDrill, isRootNode = false }: NodeRectProps) {
  const failed = node.status === 'failed';
  const testID = node.kind === 'api_call' ? `flame-node-${node.id}` : `flame-node-${node.rowID ?? node.id}`;
  const label = truncateLabel(node.label, w);
  const fill = failed
    ? 'rgba(161, 45, 33, 0.18)'
    : node.kind === 'run'
      ? 'rgba(63, 111, 77, 0.16)'
      : 'rgba(143, 62, 41, 0.2)';
  return (
    <g
      className={`flame-node${failed ? ' failed' : ''}${selected ? ' selected' : ''}${inRange ? '' : ' range-out'}`}
      onClick={onSelect}
      onDoubleClick={onDrill}
    >
      <rect
        data-testid={testID}
        className={`${failed ? 'failed' : ''}${selected ? ' selected' : ''}${inRange ? '' : ' range-out'}`}
        x={x + 0.5}
        y={y + 0.5}
        width={Math.max(w - 1, 1)}
        height={NODE_HEIGHT - 1}
        rx={3}
        fill={fill}
      >
        <title>
          {`${node.label}\n${node.kind} · ${node.status}\n${formatDuration(node.durationMS)} · 总 Token ${formatCount(node.tokenTotal)}\ninput ${formatCount(node.usage.input)} · cache read ${formatCount(node.usage.cache_read)} · cache creation ${formatCount(node.usage.cache_creation)} · output ${formatCount(node.usage.output)}`}
        </title>
      </rect>
      {label !== '' ? (
        <text className="label" x={x + 6} y={y + NODE_HEIGHT / 2 + 3}>
          {label}
        </text>
      ) : null}
      {failed && w >= 60 && !isRootNode ? (
        <text className="failed-label" x={x + w - 6} y={y + NODE_HEIGHT / 2 + 3} textAnchor="end">
          failed
        </text>
      ) : null}
    </g>
  );
}

function truncateLabel(label: string, width: number): string {
  const maxChars = Math.max(0, Math.floor((width - 10) / 6.2));
  if (label.length <= maxChars) {
    return label;
  }
  if (maxChars <= 1) {
    return '';
  }
  return `${label.slice(0, maxChars - 1)}…`;
}
