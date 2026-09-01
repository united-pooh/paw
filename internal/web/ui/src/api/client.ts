import type { BootstrapResponse, CommandReceipt, SessionMutationResult, SessionPage, SessionSnapshot, WorkspaceResponse } from './types';

export class APIError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

async function requestJSON<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const response = await fetch(input, { credentials: 'same-origin', ...init });
  if (!response.ok) {
    const body = await response.text();
    throw new APIError(response.status, body || response.statusText);
  }
  return (await response.json()) as T;
}

export const api = {
  bootstrap: (): Promise<BootstrapResponse> => requestJSON('/api/bootstrap'),
  openWorkspace: (path: string): Promise<WorkspaceResponse> => requestJSON('/api/workspaces/open', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path })
  }),
  sessions: (workspaceID: string): Promise<SessionPage> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions`),
  sessionSnapshot: (workspaceID: string, sessionID: string): Promise<SessionSnapshot> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}`),
  createSession: (workspaceID: string, commandID: string): Promise<SessionMutationResult> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ command_id: commandID })
    }),
  submitMessage: (workspaceID: string, sessionID: string, command: { command_id: string; session_version: number; text: string }): Promise<CommandReceipt> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/messages`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  steer: (workspaceID: string, sessionID: string, command: { command_id: string; active_turn_id: string; text: string }): Promise<CommandReceipt> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/steer`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  queue: (workspaceID: string, sessionID: string, command: { command_id: string; active_turn_id: string; text: string }): Promise<CommandReceipt> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/queue`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  cancel: (workspaceID: string, sessionID: string, command: { command_id: string; active_turn_id: string }): Promise<CommandReceipt> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/cancel`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  answerQuestion: (workspaceID: string, sessionID: string, requestID: string, command: { cancelled?: boolean; selected_option?: string }): Promise<void> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/interactions/${encodeURIComponent(requestID)}/answer`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  decidePermission: (workspaceID: string, sessionID: string, requestID: string, command: { decision: 'allow_once' | 'deny' }): Promise<void> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/interactions/${encodeURIComponent(requestID)}/decision`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  exchangeBootstrap: (token: string): Promise<{ status: string }> =>
    requestJSON('/api/auth/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token })
    })
};
