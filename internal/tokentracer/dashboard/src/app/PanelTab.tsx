import { DockviewDefaultTab } from 'dockview-react';
import type { IDockviewPanelHeaderProps } from 'dockview-react';

export function PanelTab(props: IDockviewPanelHeaderProps) {
  return (
    <div data-testid={`panel-tab-${props.api.id}`} className="trace-tab">
      <DockviewDefaultTab {...props} />
    </div>
  );
}
