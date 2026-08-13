import { LayoutStore, LAYOUT_KEY, RECOVERY_PREFIX } from './LayoutStore';
import type { SerializedDockview } from 'dockview-react';
import { Orientation } from 'dockview-react';

function memoryStorage(initial: Record<string, string> = {}): Storage & { keys: () => string[] } {
  const backing = new Map(Object.entries(initial));
  const storage: Storage = {
    get length() {
      return backing.size;
    },
    clear: () => backing.clear(),
    getItem: (key) => (backing.has(key) ? backing.get(key)! : null),
    key: (index) => Array.from(backing.keys())[index] ?? null,
    removeItem: (key) => {
      backing.delete(key);
    },
    setItem: (key, value) => {
      backing.set(key, String(value));
    },
  };
  return Object.assign(storage, { keys: () => Array.from(backing.keys()) });
}

const fixedISO = '2026-08-13T00:00:00.000Z';
const fixedClock = () => new Date(fixedISO);

const validDockviewLayout: SerializedDockview = {
  grid: {
    root: {
      type: 'branch',
      data: [
        { type: 'leaf', data: { id: 'group-left', views: ['calls', 'heatmap'], activeView: 'calls' }, size: 100 },
        { type: 'leaf', data: { id: 'group-right', views: ['flame', 'inspector', 'events'], activeView: 'flame' }, size: 100 },
      ],
    },
    height: 900,
    width: 1440,
    orientation: Orientation.HORIZONTAL,
  },
  panels: {
    calls: { id: 'calls', contentComponent: 'calls', title: 'Calls Table' },
    heatmap: { id: 'heatmap', contentComponent: 'heatmap', title: 'Token Heatmap' },
    flame: { id: 'flame', contentComponent: 'flame', title: 'Folded Flame' },
    inspector: { id: 'inspector', contentComponent: 'inspector', title: 'Inspector' },
    events: { id: 'events', contentComponent: 'events', title: 'Events' },
  },
};

function envelopeWithPanel(id: string): string {
  return JSON.stringify({
    schemaVersion: 1,
    savedAt: fixedISO,
    layout: {
      ...validDockviewLayout,
      panels: {
        ...validDockviewLayout.panels,
        [id]: { id, contentComponent: id, title: id },
      },
    },
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(fixedISO));
});

afterEach(() => {
  vi.useRealTimers();
});

test('round-trips only a known-panel Dockview layout', () => {
  const storage = memoryStorage();
  const store = new LayoutStore(storage, fixedClock);
  store.saveNow(validDockviewLayout);
  expect(store.load()).toEqual(validDockviewLayout);
  expect(storage.getItem(LAYOUT_KEY)).not.toContain('failed request body');
});

test.each([
  ['bad json', '{'],
  ['wrong version', JSON.stringify({ schemaVersion: 2, savedAt: fixedISO, layout: validDockviewLayout })],
  ['unknown panel', JSON.stringify(envelopeWithPanel('shell'))],
])('backs up %s once and returns null', (_name, value) => {
  const storage = memoryStorage({ [LAYOUT_KEY]: value });
  const store = new LayoutStore(storage, fixedClock);
  expect(store.load()).toBeNull();
  expect(storage.keys().filter((key) => key.startsWith(RECOVERY_PREFIX))).toHaveLength(1);
  expect(storage.getItem(LAYOUT_KEY)).toBeNull();
});

test('quarantine keeps a single recovery key and removes the stored value', () => {
  const storage = memoryStorage({ [LAYOUT_KEY]: '{' });
  const store = new LayoutStore(storage, fixedClock);
  store.quarantineStoredLayout();
  expect(storage.getItem(LAYOUT_KEY)).toBeNull();
  expect(storage.keys().filter((key) => key.startsWith(RECOVERY_PREFIX))).toHaveLength(1);
});

test('scheduleSave debounces writes', () => {
  const storage = memoryStorage();
  const store = new LayoutStore(storage, fixedClock);
  store.scheduleSave(validDockviewLayout);
  vi.advanceTimersByTime(299);
  expect(storage.getItem(LAYOUT_KEY)).toBeNull();
  vi.advanceTimersByTime(1);
  expect(storage.getItem(LAYOUT_KEY)).not.toBeNull();
  store.dispose();
});

test('reset can be undone exactly once', () => {
  const store = new LayoutStore(memoryStorage(), fixedClock);
  store.rememberForUndo(validDockviewLayout);
  expect(store.takeUndo()).toEqual(validDockviewLayout);
  expect(store.takeUndo()).toBeNull();
});

test('undo expires after ten seconds', () => {
  const store = new LayoutStore(memoryStorage());
  store.rememberForUndo(validDockviewLayout);
  vi.advanceTimersByTime(10_001);
  expect(store.takeUndo()).toBeNull();
});
