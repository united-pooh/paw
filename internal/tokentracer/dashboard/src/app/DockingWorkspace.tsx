import { useEffect, useRef, useState } from 'react';
import { DockviewReact } from 'dockview-react';
import type { DockviewApi, IDockviewPanel, IDockviewPanelProps } from 'dockview-react';
import type { FunctionComponent } from 'react';
import type { PanelID } from '../trace/types';
import { LayoutStore } from '../stores/LayoutStore';
import { PanelErrorBoundary } from '../components/PanelErrorBoundary';
import { PanelTab } from './PanelTab';
import { PanelHeaderActions } from './PanelHeaderActions';
import { TopBar } from './TopBar';
import { CallsTable } from '../panels/CallsTable';
import { TokenHeatmap } from '../panels/TokenHeatmap';
import { FoldedFlame } from '../panels/FoldedFlame';
import { Inspector } from '../panels/Inspector';
import { Events } from '../panels/Events';

export const PANEL_DEFINITIONS = {
  calls: { title: 'Calls Table', component: CallsTable },
  heatmap: { title: 'Token Heatmap', component: TokenHeatmap },
  flame: { title: 'Folded Flame', component: FoldedFlame },
  inspector: { title: 'Inspector', component: Inspector },
  events: { title: 'Events', component: Events },
} satisfies Record<PanelID, { title: string; component: FunctionComponent }>;

export function addPanelOnce(api: DockviewApi, id: PanelID): IDockviewPanel {
  const existing = api.getPanel(id);
  if (existing !== null && existing !== undefined) {
    existing.api.setActive();
    return existing;
  }
  return api.addPanel({ id, component: id, title: PANEL_DEFINITIONS[id].title, tabComponent: 'traceTab' });
}

function PanelFrame({ id }: { id: PanelID }) {
  const Definition = PANEL_DEFINITIONS[id];
  return function PanelFrameInner(props: IDockviewPanelProps) {
    const [locationRevision, setLocationRevision] = useState(0);
    useEffect(() => {
      const disposable = props.api.onDidLocationChange(() => {
        setLocationRevision((revision) => revision + 1);
      });
      return () => disposable.dispose();
    }, [props.api]);
    void locationRevision;
    return (
      <section
        className="panel-body"
        data-testid={`panel-${id}`}
        data-group-id={props.api.group.id}
        data-location={props.api.location.type}
      >
        <PanelErrorBoundary title={Definition.title}>
          <Definition.component />
        </PanelErrorBoundary>
      </section>
    );
  };
}

const components: Record<string, FunctionComponent<IDockviewPanelProps>> = {
  calls: PanelFrame({ id: 'calls' }),
  heatmap: PanelFrame({ id: 'heatmap' }),
  flame: PanelFrame({ id: 'flame' }),
  inspector: PanelFrame({ id: 'inspector' }),
  events: PanelFrame({ id: 'events' }),
};

function applyDefaultLayout(api: DockviewApi): void {
  api.clear();
  api.addPanel({ id: 'heatmap', component: 'heatmap', title: PANEL_DEFINITIONS.heatmap.title, tabComponent: 'traceTab' });
  api.addPanel({
    id: 'calls',
    component: 'calls',
    title: PANEL_DEFINITIONS.calls.title,
    tabComponent: 'traceTab',
    position: { direction: 'below', referencePanel: 'heatmap' },
  });
  api.addPanel({
    id: 'flame',
    component: 'flame',
    title: PANEL_DEFINITIONS.flame.title,
    tabComponent: 'traceTab',
    position: { direction: 'right', referencePanel: 'heatmap' },
  });
  api.addPanel({
    id: 'inspector',
    component: 'inspector',
    title: PANEL_DEFINITIONS.inspector.title,
    tabComponent: 'traceTab',
    position: { direction: 'below', referencePanel: 'flame' },
  });
  api.addPanel({
    id: 'events',
    component: 'events',
    title: PANEL_DEFINITIONS.events.title,
    tabComponent: 'traceTab',
    position: { direction: 'within', referencePanel: 'inspector' },
  });
}

function applySizing(api: DockviewApi): void {
  try {
    const heatmap = api.getPanel('heatmap');
    const calls = api.getPanel('calls');
    if (heatmap !== null && calls !== null && heatmap !== undefined && calls !== undefined) {
      const leftWidth = Math.max(Math.round((api.width * 64) / 100), 320);
      heatmap.api.setSize({ width: leftWidth, height: Math.round(heatmap.api.height * 0.3) });
      calls.api.setSize({ width: leftWidth, height: Math.round(calls.api.height * 0.7) });
    }
  } catch {
    // sizing is a best-effort refinement of the default split
  }
}

export interface DockingWorkspaceProps {
  layoutStore?: LayoutStore;
}

export function DockingWorkspace({ layoutStore }: DockingWorkspaceProps) {
  const [ownedLayoutStore] = useState(() => layoutStore ?? new LayoutStore());
  const apiRef = useRef<DockviewApi | null>(null);
  const disposablesRef = useRef<{ dispose(): void }[]>([]);
  const restoredRef = useRef(false);
  const sizedRef = useRef(false);
  const [openPanelIds, setOpenPanelIds] = useState<Set<PanelID>>(new Set());
  const [undoAvailable, setUndoAvailable] = useState(false);
  const undoTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const disposables = disposablesRef.current;
    return () => {
      for (const disposable of disposables) {
        disposable.dispose();
      }
      disposables.length = 0;
      if (undoTimerRef.current !== null) {
        clearTimeout(undoTimerRef.current);
      }
    };
  }, []);

  const refreshOpenPanels = (api: DockviewApi): void => {
    const serialized = api.toJSON();
    const ids = new Set<PanelID>();
    if (serialized !== null && serialized !== undefined && serialized.panels !== undefined) {
      for (const key of Object.keys(serialized.panels)) {
        if ((['calls', 'heatmap', 'flame', 'inspector', 'events'] as string[]).includes(key)) {
          ids.add(key as PanelID);
        }
      }
    }
    setOpenPanelIds(ids);
  };

  const restoreLayout = (api: DockviewApi): void => {
    apiRef.current = api;
    let restored = false;
    const saved = ownedLayoutStore.load();
    if (saved !== null) {
      try {
        api.fromJSON(saved, { reuseExistingPanels: false });
        restored = true;
      } catch {
        ownedLayoutStore.quarantineStoredLayout();
      }
    }
    if (!restored) {
      applyDefaultLayout(api);
    }
    restoredRef.current = true;
    api.getPanel('calls')?.api.setActive();
    refreshOpenPanels(api);
    ownedLayoutStore.scheduleSave(api.toJSON());
    disposablesRef.current.push(
      api.onDidLayoutChange(() => {
        if (!restoredRef.current) {
          return;
        }
        refreshOpenPanels(api);
        ownedLayoutStore.scheduleSave(api.toJSON());
        if (!sizedRef.current) {
          sizedRef.current = true;
          if (!restored) {
            applySizing(api);
          }
        }
      }),
    );
  };

  const handleReset = (): void => {
    const api = apiRef.current;
    if (api === null) {
      return;
    }
    ownedLayoutStore.rememberForUndo(api.toJSON());
    applyDefaultLayout(api);
    ownedLayoutStore.saveNow(api.toJSON());
    setUndoAvailable(true);
    if (undoTimerRef.current !== null) {
      clearTimeout(undoTimerRef.current);
    }
    undoTimerRef.current = setTimeout(() => setUndoAvailable(false), 10_000);
  };

  const handleUndo = (): void => {
    const api = apiRef.current;
    const undo = ownedLayoutStore.takeUndo();
    setUndoAvailable(false);
    if (api === null || undo === null) {
      return;
    }
    try {
      api.fromJSON(undo, { reuseExistingPanels: false });
    } catch {
      ownedLayoutStore.quarantineStoredLayout();
    }
  };

  const handleAddPanel = (id: PanelID): void => {
    const api = apiRef.current;
    if (api !== null) {
      addPanelOnce(api, id);
    }
  };
  return (
    <div className="app-shell">
      <TopBar
        openPanelIds={openPanelIds}
        onAddPanel={handleAddPanel}
        onResetLayout={handleReset}
        onUndoReset={handleUndo}
        undoAvailable={undoAvailable}
      />
      <main className="app-main" aria-label="Token Tracer workspace">
        <DockviewReact
          className="dv-theme-tokentracer"
          dndStrategy="pointer"
          components={components}
          tabComponents={{ traceTab: PanelTab }}
          rightHeaderActionsComponent={PanelHeaderActions}
          onReady={(event) => restoreLayout(event.api)}
        />
      </main>
    </div>
  );
}
