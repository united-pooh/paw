import type { SerializedDockview } from 'dockview-react';

export const LAYOUT_KEY = 'paw.tokenTracer.layout.v1';
export const RECOVERY_PREFIX = 'paw.tokenTracer.layout.recovery.';
export const PANEL_IDS = ['calls', 'heatmap', 'flame', 'inspector', 'events'] as const;

interface LayoutEnvelope {
  schemaVersion: 1;
  savedAt: string;
  layout: SerializedDockview;
}

const UNDO_TTL_MS = 10_000;
const SAVE_DEBOUNCE_MS = 300;

export class LayoutStore {
  private readonly storage: Storage;
  private readonly now: () => Date;
  private saveTimer: ReturnType<typeof setTimeout> | null = null;
  private undo: { layout: SerializedDockview; expiresAt: number } | null = null;

  constructor(storage: Storage = localStorage, now: () => Date = () => new Date()) {
    this.storage = storage;
    this.now = now;
  }

  load(): SerializedDockview | null {
    const raw = this.storage.getItem(LAYOUT_KEY);
    if (raw === null) {
      return null;
    }
    const parsed = this.parseEnvelope(raw);
    if (parsed === null) {
      this.quarantineStoredLayout();
      return null;
    }
    return parsed.layout;
  }

  quarantineStoredLayout(): void {
    const raw = this.storage.getItem(LAYOUT_KEY);
    if (raw !== null) {
      this.backupAndRemove(raw);
    }
  }

  scheduleSave(layout: SerializedDockview): void {
    if (this.saveTimer !== null) {
      clearTimeout(this.saveTimer);
    }
    this.saveTimer = setTimeout(() => {
      this.saveTimer = null;
      this.saveNow(layout);
    }, SAVE_DEBOUNCE_MS);
  }

  saveNow(layout: SerializedDockview): void {
    const envelope: LayoutEnvelope = {
      schemaVersion: 1,
      savedAt: this.now().toISOString(),
      layout,
    };
    this.storage.setItem(LAYOUT_KEY, JSON.stringify(envelope));
  }

  rememberForUndo(layout: SerializedDockview): void {
    this.undo = { layout, expiresAt: this.now().getTime() + UNDO_TTL_MS };
  }

  takeUndo(): SerializedDockview | null {
    if (this.undo === null) {
      return null;
    }
    if (this.now().getTime() > this.undo.expiresAt) {
      this.undo = null;
      return null;
    }
    const layout = this.undo.layout;
    this.undo = null;
    return layout;
  }

  dispose(): void {
    if (this.saveTimer !== null) {
      clearTimeout(this.saveTimer);
      this.saveTimer = null;
    }
    this.undo = null;
  }

  private parseEnvelope(raw: string): LayoutEnvelope | null {
    let envelope: unknown;
    try {
      envelope = JSON.parse(raw);
    } catch {
      return null;
    }
    if (typeof envelope !== 'object' || envelope === null) {
      return null;
    }
    const candidate = envelope as Record<string, unknown>;
    if (candidate.schemaVersion !== 1 || typeof candidate.savedAt !== 'string' || candidate.layout === null || typeof candidate.layout !== 'object') {
      return null;
    }
    if (Number.isNaN(Date.parse(candidate.savedAt))) {
      return null;
    }
    const layout = candidate.layout as Record<string, unknown>;
    if (layout.panels === null || typeof layout.panels !== 'object') {
      return null;
    }
    const panels = layout.panels as Record<string, unknown>;
    for (const key of Object.keys(panels)) {
      if (!(PANEL_IDS as readonly string[]).includes(key)) {
        return null;
      }
      const panel = panels[key] as Record<string, unknown> | null;
      if (typeof panel !== 'object' || panel === null) {
        return null;
      }
      if (panel.id !== key || panel.contentComponent !== key) {
        return null;
      }
    }
    return envelope as LayoutEnvelope;
  }

  private backupAndRemove(raw: string): void {
    const recoveryKey = `${RECOVERY_PREFIX}${this.now().toISOString()}`;
    this.storage.setItem(recoveryKey, raw);
    const prefixLength = RECOVERY_PREFIX.length;
    const staleKeys = Object.keys(this.storage)
      .filter((key) => key.startsWith(RECOVERY_PREFIX) && key !== recoveryKey)
      .sort();
    for (const staleKey of staleKeys) {
      this.storage.removeItem(staleKey);
    }
    this.storage.removeItem(LAYOUT_KEY);
  }
}
