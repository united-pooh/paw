import { useEffect, useState } from 'react';
import { StoreProvider, useStore, useTraceState } from '../stores/StoreProvider';
import type { StoreProviderProps } from '../stores/StoreProvider';
import { DockingWorkspace } from './DockingWorkspace';
import { NarrowWorkspace } from './NarrowWorkspace';
import type { LayoutStore } from '../stores/LayoutStore';

export interface AppProps {
  layoutStore?: LayoutStore;
  trace?: StoreProviderProps['trace'];
  selection?: StoreProviderProps['selection'];
  filters?: StoreProviderProps['filters'];
}

function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia('(max-width: 959px)').matches
      : false,
  );
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined;
    }
    const media = window.matchMedia('(max-width: 959px)');
    const onChange = (event: MediaQueryListEvent): void => setNarrow(event.matches);
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, []);
  return narrow;
}

function Shell({ layoutStore }: { layoutStore?: LayoutStore }) {
  const traceState = useTraceState();
  const { trace } = useStore();
  const narrow = useNarrow();

  if (traceState.snapshot === null) {
    const failed = traceState.connection === 'error';
    return (
      <div className="app-shell">
        <header role="banner" className="topbar">
          <div className="topbar-brand">
            <span className="topbar-title">Token Tracer</span>
          </div>
          <div className="topbar-kpis">
            <span className="conn-badge">
              <span className={`conn-dot ${traceState.connection}`} />
              {failed ? '连接失败' : '连接中'}
            </span>
          </div>
        </header>
        <main className="app-main" aria-label="Token Tracer workspace">
          {failed ? (
            <div className="error-state" role="alert">
              <div>无法加载追踪数据</div>
              <div className="error-summary">{traceState.error}</div>
              <button type="button" onClick={() => void trace.retry()}>
                重试
              </button>
            </div>
          ) : (
            <div className="loading-state">正在加载追踪数据…</div>
          )}
        </main>
      </div>
    );
  }

  if (narrow) {
    return <NarrowWorkspace />;
  }
  return <DockingWorkspace layoutStore={layoutStore} />;
}

export function App({ layoutStore, trace, selection, filters }: AppProps) {
  return (
    <StoreProvider trace={trace} selection={selection} filters={filters}>
      <Shell layoutStore={layoutStore} />
    </StoreProvider>
  );
}
