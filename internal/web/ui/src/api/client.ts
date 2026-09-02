import type { BootstrapResponse, CommandReceipt, CompletionResponse, ModelOptionsResponse, PickWorkspaceResponse, SessionMutationResult, SessionPage, SessionSnapshot, WorkspaceResponse } from './types';

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
  // 后端 Go nil slice 会序列化为 null，这里统一归一化为空数组，避免上层取 [0] 崩溃。
  bootstrap: async (): Promise<BootstrapResponse> => {
    const response = await requestJSON<BootstrapResponse>('/api/bootstrap');
    return { ...response, recent_workspaces: response.recent_workspaces ?? [], loaded_workspaces: response.loaded_workspaces ?? [] };
  },
  openWorkspace: (path: string): Promise<WorkspaceResponse> => requestJSON('/api/workspaces/open', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path })
  }),
  pickWorkspace: (): Promise<PickWorkspaceResponse> => requestJSON('/api/workspaces/pick', { method: 'POST' }),
completions: async (workspaceID: string, trigger: string, query: string): Promise<CompletionResponse> => {
const params = new URLSearchParams({ trigger, query });
const response = await requestJSON<CompletionResponse>(`/api/workspaces/${encodeURIComponent(workspaceID)}/completions?${params}`);
return { items: response.items ?? [] };
},
modelOptions: async (workspaceID: string): Promise<ModelOptionsResponse> => {
const response = await requestJSON<ModelOptionsResponse>(`/api/workspaces/${encodeURIComponent(workspaceID)}/model-options`);
return { ...response, models: response.models ?? [], effort_options: response.effort_options ?? [] };
},
selectModel: (workspaceID: string, selection: { model_id?: string; effort?: string }): Promise<ModelOptionsResponse> =>
requestJSON(`/api/workspaces/${encodeURIComponent(workspaceID)}/model`, {
method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(selection)
}),
  sessions: async (workspaceID: string): Promise<SessionPage> => {
    const page = await requestJSON<SessionPage>(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions`);
    return { ...page, items: page.items ?? [] };
  },
  sessionSnapshot: async (workspaceID: string, sessionID: string): Promise<SessionSnapshot> => {
    const snapshot = await requestJSON<SessionSnapshot>(`/api/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}`);
    return { ...snapshot, turns: snapshot.turns ?? [], parts: snapshot.parts ?? [], pending: snapshot.pending ?? [], queue: snapshot.queue ?? [] };
  },
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
