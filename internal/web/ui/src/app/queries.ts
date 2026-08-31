import { api } from '../api/client';
import type { WorkbenchStore } from './store';

export async function loadSession(store: WorkbenchStore, workspaceID: string, sessionID: string): Promise<void> {
  store.dispatch({ type: 'connection.changed', connection: 'connecting' });
  const snapshot = await api.sessionSnapshot(workspaceID, sessionID);
  store.dispatch({ type: 'snapshot.loaded', snapshot });
}
