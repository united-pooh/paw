import type { RecentWorkspace } from '../../api/types';

/** 工作区头像配色：按名称哈希从暖调莫兰迪色板中确定性取色 */
const AVATAR_COLORS = ['#c15f3c', '#7a6ff0', '#4c8a5f', '#b0782a', '#3f7d8c', '#a04e68'];

function avatarColor(seed: string): string {
  let hash = 0;
  for (const ch of seed) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  return AVATAR_COLORS[hash % AVATAR_COLORS.length];
}

/**
 * 工作区图标导轨（Slack / VS Code 式）：
 * 低频的工作区切换收敛为窄导轨上的字母头像，一键直达；
 * 高频的会话列表交给旁边的面板，整体占用远小于双等宽列。
 */
export function WorkspaceSidebar({ workspaces, selected, onSelect, onOpen }: { workspaces: RecentWorkspace[]; selected?: string; onSelect: (id: string) => void; onOpen?: () => void }) {
  return (
    <aside className="workspace-rail" aria-label="工作区">
      <div className="rail-brand" title="Paw"><span className="brand-mark">P</span></div>
      <nav className="rail-workspaces">
        {workspaces.map((workspace) => (
          <button
            key={workspace.id}
            type="button"
            className={`rail-avatar${workspace.id === selected ? ' selected' : ''}`}
            style={{ background: avatarColor(workspace.name || workspace.path) }}
            title={`${workspace.name}\n${workspace.path}`}
            aria-label={`工作区 ${workspace.name}`}
            onClick={() => onSelect(workspace.id)}
          >
            {(workspace.name || '?').trim().charAt(0).toUpperCase()}
          </button>
        ))}
      </nav>
      {onOpen && (
        <button type="button" className="rail-add" title="打开工作区" aria-label="打开工作区" onClick={onOpen}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      )}
    </aside>
  );
}
