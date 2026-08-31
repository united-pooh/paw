import { WorkbenchShell } from '../components/WorkbenchShell';
import type { RecentWorkspace, SessionSnapshot, SessionSummary } from '../api/types';

const workspaces: RecentWorkspace[] = [{ id: 'local', name: '当前工作区', path: '.', last_opened_at: new Date().toISOString() }];
const sessions: SessionSummary[] = [];
const snapshot: SessionSnapshot | null = null;

export function App() {
  return <WorkbenchShell workspaces={workspaces} sessions={sessions} snapshot={snapshot} parts={{}} />;
}
