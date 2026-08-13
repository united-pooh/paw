import { useState } from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DockingWorkspace } from './DockingWorkspace';
import { App } from './App';
import { LayoutStore, LAYOUT_KEY } from '../stores/LayoutStore';
import { TraceStore } from '../stores/TraceStore';
import { SelectionStore } from '../stores/SelectionStore';
import { FilterStore } from '../stores/FilterStore';
import { StoreProvider } from '../stores/StoreProvider';
import { fixtureSnapshot, fakeTraceIO } from '../test/fixtures';
import { Orientation } from 'dockview-react';
import type { SerializedDockview } from 'dockview-react';

const dockviewMock = vi.hoisted(() => ({
  api: null as unknown,
  createApi: null as (() => unknown) | null,
}));

vi.mock('dockview-react', async () => {
  const { createElement, useEffect } = await import('react');
  // The mock deliberately uses `any` types: it substitutes Dockview's DOM and
  // API surface, and its shape is validated by the component tests themselves.

  class FakeDockviewApi {
    panelMap = new Map<string, unknown>();
    groupMap = new Map<string, { id: string; panels: unknown[] }>();
    listeners: (() => void)[] = [];
    serialized: SerializedDockview | null = null;
    width = 1200;
    height = 800;
    private groupCounter = 0;

    activePanelId: string | null = null;

    get panels(): unknown[] {
      return Array.from(this.panelMap.values());
    }

    get activePanel(): unknown {
      if (this.activePanelId !== null && this.panelMap.has(this.activePanelId)) {
        return this.panelMap.get(this.activePanelId);
      }
      return this.panels[0] ?? undefined;
    }

    setActivePanel(id: string): void {
      this.activePanelId = id;
      this.emit();
    }

    onDidLayoutChange(fn: () => void): { dispose(): void } {
      this.listeners.push(fn);
      return {
        dispose: () => {
          this.listeners = this.listeners.filter((other) => other !== fn);
        },
      };
    }

    onDidMaximizedGroupChange(): { dispose(): void } {
      return { dispose: () => undefined };
    }

    private emit(): void {
      for (const fn of this.listeners) {
        fn();
      }
    }

    getPanel(id: string): unknown {
      return this.panelMap.get(id) ?? null;
    }

    removePanel(id: string): void {
      const panel = this.panelMap.get(id) as FakePanel | undefined;
      if (panel === undefined) {
        return;
      }
      this.panelMap.delete(id);
      const group = this.groupMap.get(panel.group.id);
      if (group !== undefined) {
        group.panels = group.panels.filter((other) => (other as FakePanel).id !== id);
      }
      this.emit();
    }

    clear(): void {
      this.panelMap.clear();
      this.groupMap.clear();
      this.serialized = null;
      this.emit();
    }

    addPanel(options: {
      id: string;
      component: string;
      title: string;
      position?: { direction: string; referencePanel: string };
    }): unknown {
      const existing = this.panelMap.get(options.id);
      if (existing !== undefined) {
        return existing;
      }
      let groupId: string;
      if (options.position?.direction === 'within' && options.position.referencePanel !== undefined) {
        const reference = this.panelMap.get(options.position.referencePanel) as FakePanel | undefined;
        groupId = reference !== undefined ? reference.group.id : `group-${++this.groupCounter}`;
      } else {
        groupId = `group-${++this.groupCounter}`;
      }
      const panel = new FakePanel(options.id, options.title, groupId, this);
      this.panelMap.set(options.id, panel);
      let group = this.groupMap.get(groupId);
      if (group === undefined) {
        group = { id: groupId, panels: [] };
        this.groupMap.set(groupId, group);
      }
      group.panels.push(panel);
      this.emit();
      return panel;
    }

    addFloatingGroup(panel: FakePanel): void {
      panel.location = { type: 'floating' };
      this.emit();
    }

    toJSON(): SerializedDockview {
      if (this.serialized !== null) {
        return this.serialized;
      }
      const panels: Record<string, { id: string; contentComponent: string; title: string }> = {};
      const views: string[] = [];
      for (const entry of this.panelMap.values()) {
        const panel = entry as FakePanel;
        panels[panel.id] = { id: panel.id, contentComponent: panel.id, title: panel.title };
        views.push(panel.id);
      }
      return {
        grid: {
          root: { type: 'leaf', data: { id: 'group-main', views, activeView: views[0] } },
          height: 800,
          width: 1200,
          orientation: Orientation.HORIZONTAL,
        },
        panels,
        activeGroup: 'group-main',
      };
    }

    fromJSON(json: SerializedDockview): void {
      this.serialized = json;
      this.panelMap.clear();
      this.groupMap.clear();
      const walk = (node: { type: string; data: unknown }): void => {
        if (node.type === 'leaf') {
          const data = node.data as { id?: string; views?: string[] };
          const groupId = data.id ?? 'group';
          const group = { id: groupId, panels: [] as unknown[] };
          this.groupMap.set(groupId, group);
          for (const viewId of data.views ?? []) {
            const state = json.panels[viewId];
            const panel = new FakePanel(viewId, state?.title ?? viewId, groupId, this);
            this.panelMap.set(viewId, panel);
            group.panels.push(panel);
          }
        } else if (Array.isArray(node.data)) {
          for (const child of node.data) {
            walk(child as { type: string; data: unknown });
          }
        }
      };
      walk(json.grid.root);
      this.emit();
    }
  }

  class FakePanel {
    id: string;
    title: string;
    group: { id: string };
    location: { type: string };
    api: FakePanelApi;

    constructor(id: string, title: string, groupId: string, owner: FakeDockviewApi) {
      this.id = id;
      this.title = title;
      this.group = { id: groupId };
      this.location = { type: 'grid' };
      this.api = new FakePanelApi(this, owner);
    }
  }

  class FakePanelApi {
    private panel: FakePanel;
    private owner: FakeDockviewApi;
    isMaximized = vi.fn(() => false);
    maximize = vi.fn();
    exitMaximized = vi.fn();
    setActive = vi.fn(() => {
      this.owner.setActivePanel(this.panel.id);
    });
    setSize = vi.fn();

    constructor(panel: FakePanel, owner: FakeDockviewApi) {
      this.panel = panel;
      this.owner = owner;
    }

    get id(): string {
      return this.panel.id;
    }

    get title(): string {
      return this.panel.title;
    }

    get isActive(): boolean {
      return this.owner.activePanelId === this.panel.id;
    }

    get group(): { id: string } {
      return this.panel.group;
    }

    get location(): { type: string } {
      return this.panel.location;
    }

    close(): void {
      this.owner.removePanel(this.panel.id);
    }

    onDidLocationChange(): { dispose(): void } {
      return { dispose: () => undefined };
    }
  }

  const DockviewReact = (props: any): any => {
    const HeaderActions = props.rightHeaderActionsComponent;
    useEffect(() => {
      props.onReady({ api: dockviewMock.api });
    }, []);
    const api = dockviewMock.api as FakeDockviewApi;
    const children: any[] = [];
    if (HeaderActions !== undefined) {
      children.push(
        createElement(HeaderActions, {
          api: { id: 'group-main' },
          containerApi: api,
          panels: api.panels,
          activePanel: api.activePanel,
          isGroupActive: true,
          group: { id: 'group-main' },
          headerPosition: 'top',
        }),
      );
    }
    for (const panel of api.panels) {
      const fakePanel = panel as FakePanel;
      const Component = props.components[fakePanel.id];
      if (Component !== undefined) {
        children.push(createElement(Component, { key: fakePanel.id, api: fakePanel.api, containerApi: api }));
      }
    }
    return createElement('div', { 'data-testid': 'dockview' }, children);
  };

  dockviewMock.createApi = () => new FakeDockviewApi();

  return {
    DockviewReact,
    DockviewDefaultTab: () => createElement('div'),
    Orientation: { HORIZONTAL: 'HORIZONTAL', VERTICAL: 'VERTICAL' },
  };
});

const fixedClock = () => new Date('2026-08-13T00:00:00.000Z');

function memoryStorage(initial: Record<string, string> = {}): Storage {
  const backing = new Map(Object.entries(initial));
  return {
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
}

const customLayout: SerializedDockview = {
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

function panelGroup(api: { getPanel: (id: string) => unknown }, id: string): string | null {
  const panel = api.getPanel(id) as { group: { id: string } } | null;
  return panel === null ? null : panel.group.id;
}

function TestWorkspace() {
  const [stores] = useState(() => ({
    trace: new TraceStore(fakeTraceIO(fixtureSnapshot)),
    selection: new SelectionStore(),
    filters: new FilterStore(),
  }));
  return (
    <StoreProvider trace={stores.trace} selection={stores.selection} filters={stores.filters}>
      <DockingWorkspace />
    </StoreProvider>
  );
}

function renderWorkspaceWithSavedLayout(saved: SerializedDockview) {
  const storage = memoryStorage();
  const layoutStore = new LayoutStore(storage, fixedClock);
  layoutStore.saveNow(saved);
  const trace = new TraceStore(fakeTraceIO(fixtureSnapshot));
  const selectionStore = new SelectionStore();
  const filterStore = new FilterStore();
  render(
    <StoreProvider trace={trace} selection={selectionStore} filters={filterStore}>
      <DockingWorkspace layoutStore={layoutStore} />
    </StoreProvider>,
  );
  return { api: dockviewMock.api as { getPanel: (id: string) => unknown; toJSON: () => SerializedDockview }, layoutStore };
}

async function renderAtWidth(width: number, savedDesktopLayout: SerializedDockview) {
  vi.spyOn(window, 'matchMedia').mockImplementation(
    (query: string) =>
      ({
        matches: width <= 959,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        onchange: null,
        dispatchEvent: vi.fn(),
      }) as unknown as MediaQueryList,
  );
  const storage = memoryStorage();
  const layoutStore = new LayoutStore(storage, fixedClock);
  layoutStore.saveNow(savedDesktopLayout);
  const tracked = { layoutStore, saveCount: 0 };
  const originalSaveNow = layoutStore.saveNow.bind(layoutStore);
  vi.spyOn(layoutStore, 'saveNow').mockImplementation((layout) => {
    tracked.saveCount++;
    originalSaveNow(layout);
  });
  const trace = new TraceStore(fakeTraceIO(fixtureSnapshot));
  render(
    <App
      layoutStore={layoutStore}
      trace={trace}
      selection={new SelectionStore()}
      filters={new FilterStore()}
    />,
  );
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
  return tracked;
}

beforeEach(() => {
  dockviewMock.api = dockviewMock.createApi!();
});

afterEach(() => {
  vi.restoreAllMocks();
});

test('opens each registered panel at most once and re-enables it after close', async () => {
  const user = userEvent.setup();
  render(<TestWorkspace />);
  await user.click(screen.getByRole('button', { name: '添加面板' }));
  expect(screen.getByRole('menuitem', { name: 'Calls Table' })).toBeDisabled();
  await user.click(screen.getByLabelText('关闭 Calls Table'));
  await user.click(screen.getByRole('button', { name: '添加面板' }));
  expect(screen.getByRole('menuitem', { name: 'Calls Table' })).toBeEnabled();
});

test('reset applies defaults and exposes a one-shot undo', async () => {
  const user = userEvent.setup();
  const { api } = renderWorkspaceWithSavedLayout(customLayout);
  await user.click(screen.getByRole('button', { name: '恢复默认布局' }));
  expect(panelGroup(api, 'inspector')).toBe(panelGroup(api, 'events'));
  await user.click(screen.getByRole('button', { name: '撤销布局恢复' }));
  expect(api.toJSON()).toEqual(customLayout);
});

test('narrow mode does not save over the desktop layout', async () => {
  const tracked = await renderAtWidth(760, customLayout);
  expect(screen.getByRole('tablist', { name: 'Token Tracer panels' })).toBeInTheDocument();
  expect(tracked.saveCount).toBe(0);
});

test('floats a panel through the header action', async () => {
  const user = userEvent.setup();
  render(<TestWorkspace />);
  const api = dockviewMock.api as {
    getPanel: (id: string) => unknown;
    setActivePanel: (id: string) => void;
  };
  const events = api.getPanel('events') as { api: { setActive(): void } };
  act(() => {
    events.api.setActive();
  });
  await user.click(screen.getByLabelText('浮动 Events'));
  const floated = api.getPanel('events') as { location: { type: string } };
  expect(floated.location.type).toBe('floating');
});
