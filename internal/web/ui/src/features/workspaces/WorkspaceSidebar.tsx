import type { RecentWorkspace } from '../../api/types';

export function WorkspaceSidebar({ workspaces, selected, onSelect, onOpen }: { workspaces: RecentWorkspace[]; selected?: string; onSelect: (id: string) => void; onOpen?: () => void }) {
  return (
    <aside className="workspace-sidebar" aria-label="工作区">
      <div className="brand-row"><span className="brand-mark">P</span><strong>Paw</strong></div>
      <div className="sidebar-label"><span>工作区</span>{onOpen && <button type="button" onClick={onOpen}>打开</button>}</div>
      <nav>{workspaces.map((workspace) => <button key={workspace.id} className={workspace.id === selected ? 'selected' : ''} onClick={() => onSelect(workspace.id)} type="button"><span>{workspace.name}</span><small>{workspace.path}</small></button>)}</nav>
    </aside>
  );
}
