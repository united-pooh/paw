import { useEffect, useMemo, useRef, useState } from 'react';
import { useFilters, useSelection, useStore, useTraceState } from '../stores/StoreProvider';
import { formatCount, formatDuration, formatPercent } from '../trace/format';
import type { PanelID, TraceSnapshot, TimelineRow } from '../trace/types';

export interface TopBarProps {
  openPanelIds: Set<PanelID>;
  onAddPanel: (panel: PanelID) => void;
  onResetLayout: () => void;
  onUndoReset: () => void;
  undoAvailable: boolean;
  narrow?: boolean;
}

export function healthScore(rows: TimelineRow[]): number {
  if (rows.length === 0) {
    return 100;
  }
  const failed = rows.filter((row) => row.status === 'failed').length;
  return Math.max(18, Math.round((1 - failed / rows.length) * 100));
}

const PANEL_NAMES: Record<PanelID, string> = {
  calls: 'Calls Table',
  heatmap: 'Token Heatmap',
  flame: 'Folded Flame',
  inspector: 'Inspector',
  events: 'Events',
};

export function TopBar({
  openPanelIds,
  onAddPanel,
  onResetLayout,
  onUndoReset,
  undoAvailable,
  narrow = false,
}: TopBarProps) {
  const traceState = useTraceState();
  const data = traceState.snapshot;
  const filters = useFilters();
  const { filtersStore, selection } = useStore();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuWrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!menuOpen) {
      return undefined;
    }
    const onPointerDown = (event: PointerEvent): void => {
      if (menuWrapRef.current !== null && !menuWrapRef.current.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener('pointerdown', onPointerDown);
    return () => document.removeEventListener('pointerdown', onPointerDown);
  }, [menuOpen]);

  const models = useMemo(() => {
    if (data === null) {
      return [] as string[];
    }
    const unique = new Set<string>();
    for (const row of data.timeline.rows) {
      if (row.model !== undefined && row.model !== '') {
        unique.add(row.model);
      }
    }
    return Array.from(unique).sort();
  }, [data]);

  const connectionLabel =
    traceState.connection === 'live'
      ? '实时'
      : traceState.connection === 'reconnecting'
        ? '重新连接中'
        : traceState.connection === 'error'
          ? '连接失败'
          : '连接中';

  const exportJSON = (snapshot: TraceSnapshot): void => {
    const blob = new Blob([JSON.stringify(snapshot, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `token-tracer-${snapshot.run_id}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <header className="topbar" role="banner">
      <div className="topbar-brand">
        <span className="topbar-title">Token Tracer</span>
        <span className="topbar-pipeline" title={data?.pipeline.name ?? ''}>
          {data?.pipeline.name ?? '—'} · {data?.pipeline.status ?? '—'}
        </span>
      </div>
      <div className="topbar-kpis">
        <span className={`conn-badge`}>
          <span className={`conn-dot ${traceState.connection}`} />
          {connectionLabel}
        </span>
        <span className="kpi">时长 <b>{data !== null ? formatDuration(data.timeline.duration_ms) : '—'}</b></span>
        <span className="kpi">调用 <b>{data !== null ? formatCount(data.pipeline.calls) : '—'}</b></span>
        <span className="kpi">总上下文 <b>{data !== null ? formatCount(data.timeline.token_total.total_context) : '—'}</b></span>
        <span className="kpi">缓存命中 <b>{data !== null ? formatPercent(data.timeline.token_total.cache_hit_rate) : '—'}</b></span>
        <span className="kpi">输出 <b>{data !== null ? formatCount(data.timeline.token_total.output) : '—'}</b></span>
        <span className="kpi">健康度 <b>{data !== null ? healthScore(data.timeline.rows) : '—'}</b></span>
      </div>
      <div className="topbar-actions">
        <select
          aria-label="Scope 筛选"
          value={filters.scope}
          onChange={(event) => filtersStore.setScope(event.target.value as 'all' | 'stage' | 'agent')}
        >
          <option value="all">全部范围</option>
          <option value="stage">仅阶段</option>
          <option value="agent">仅调用</option>
        </select>
        <select
          aria-label="模型筛选"
          value={filters.model ?? ''}
          onChange={(event) => filtersStore.setModel(event.target.value === '' ? null : event.target.value)}
        >
          <option value="">全部模型</option>
          {models.map((model) => (
            <option key={model} value={model}>{model}</option>
          ))}
        </select>
        <button
          type="button"
          aria-pressed={filters.errorsOnly}
          onClick={() => filtersStore.setErrorsOnly(!filters.errorsOnly)}
        >
          仅异常
        </button>
        <button type="button" aria-label="清除选择" onClick={() => selection.clear()}>
          清除选择
        </button>
        <button type="button" aria-label="导出 JSON" disabled={data === null} onClick={() => data !== null && exportJSON(data)}>
          导出 JSON
        </button>
        {!narrow ? (
          <>
            <div className="menu-wrap" ref={menuWrapRef}>
              <button type="button" aria-haspopup="menu" aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
                添加面板
              </button>
              {menuOpen ? (
                <div role="menu" className="panel-menu">
                  {(Object.keys(PANEL_NAMES) as PanelID[]).map((id) => (
                    <button
                      key={id}
                      type="button"
                      role="menuitem"
                      disabled={openPanelIds.has(id)}
                      onClick={() => {
                        onAddPanel(id);
                        setMenuOpen(false);
                      }}
                    >
                      {PANEL_NAMES[id]}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
            <button type="button" aria-label="恢复默认布局" onClick={onResetLayout}>
              恢复默认布局
            </button>
            {undoAvailable ? (
              <button type="button" aria-label="撤销布局恢复" onClick={onUndoReset}>
                撤销布局恢复
              </button>
            ) : null}
          </>
        ) : null}
      </div>
    </header>
  );
}
