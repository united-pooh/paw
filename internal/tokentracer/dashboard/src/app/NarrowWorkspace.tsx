import { useState } from 'react';
import type { ComponentType } from 'react';
import type { PanelID } from '../trace/types';
import { PanelErrorBoundary } from '../components/PanelErrorBoundary';
import { TopBar } from './TopBar';
import { CallsTable } from '../panels/CallsTable';
import { TokenHeatmap } from '../panels/TokenHeatmap';
import { FoldedFlame } from '../panels/FoldedFlame';
import { Inspector } from '../panels/Inspector';
import { Events } from '../panels/Events';

const PANELS: { id: PanelID; title: string; component: ComponentType }[] = [
  { id: 'calls', title: 'Calls Table', component: CallsTable },
  { id: 'heatmap', title: 'Token Heatmap', component: TokenHeatmap },
  { id: 'flame', title: 'Folded Flame', component: FoldedFlame },
  { id: 'inspector', title: 'Inspector', component: Inspector },
  { id: 'events', title: 'Events', component: Events },
];

export function NarrowWorkspace() {
  const [active, setActive] = useState<PanelID>('calls');
  const activePanel = PANELS.find((panel) => panel.id === active) ?? PANELS[0];
  return (
    <div className="app-shell">
      <TopBar
        openPanelIds={new Set()}
        onAddPanel={() => undefined}
        onResetLayout={() => undefined}
        onUndoReset={() => undefined}
        undoAvailable={false}
        narrow
      />
      <main className="app-main" aria-label="Token Tracer workspace">
        <div className="narrow-tabs">
          <div role="tablist" aria-label="Token Tracer panels" className="narrow-tablist">
            {PANELS.map((panel) => (
              <button
                key={panel.id}
                type="button"
                role="tab"
                aria-selected={active === panel.id}
                onClick={() => setActive(panel.id)}
              >
                {panel.title}
              </button>
            ))}
          </div>
          <div className="narrow-panel" role="tabpanel" data-testid={`panel-${activePanel.id}`}>
            <PanelErrorBoundary title={activePanel.title}>
              <activePanel.component />
            </PanelErrorBoundary>
          </div>
        </div>
      </main>
    </div>
  );
}
