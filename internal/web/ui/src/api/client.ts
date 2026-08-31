import type { BootstrapResponse, CommandReceipt, SessionPage, SessionSnapshot } from './types';

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
  sessions: (workspaceID: string): Promise<SessionPage> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions`),
  sessionSnapshot: (workspaceID: string, sessionID: string): Promise<SessionSnapshot> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}`),
  submitMessage: (workspaceID: string, sessionID: string, command: { command_id: string; session_version: number; text: string }): Promise<CommandReceipt> =>
    requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/messages`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(command)
    }),
  exchangeBootstrap: (token: string): Promise<{ status: string }> =>
    requestJSON('/api/auth/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token })
    })
};
