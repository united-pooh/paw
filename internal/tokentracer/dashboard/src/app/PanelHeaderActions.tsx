import { useEffect, useState } from 'react';
import type { IDockviewHeaderActionsProps } from 'dockview-react';

export function PanelHeaderActions({ activePanel, containerApi }: IDockviewHeaderActionsProps) {
  const [, setMaximizedRevision] = useState(0);

  useEffect(() => {
    const disposable = containerApi.onDidMaximizedGroupChange(() => {
      setMaximizedRevision((revision) => revision + 1);
    });
    return () => disposable.dispose();
  }, [containerApi]);

  if (activePanel === undefined) {
    return null;
  }
  const title = activePanel.api.title ?? activePanel.id;
  const maximized = activePanel.api.isMaximized();
  return (
    <div className="panel-header-actions">
      <span className="panel-header-actions-title" title={title}>{title}</span>
      <button
        type="button"
        aria-label={maximized ? `恢复 ${title}` : `最大化 ${title}`}
        onClick={() => {
          if (maximized) {
            activePanel.api.exitMaximized();
          } else {
            activePanel.api.maximize();
          }
        }}
      >
        {maximized ? '▣' : '⛶'}
      </button>
      <button
        type="button"
        aria-label={`浮动 ${title}`}
        onClick={() => containerApi.addFloatingGroup(activePanel)}
      >
        ◱
      </button>
      <button
        type="button"
        aria-label={`关闭 ${title}`}
        onClick={() => activePanel.api.close()}
      >
        ×
      </button>
    </div>
  );
}
