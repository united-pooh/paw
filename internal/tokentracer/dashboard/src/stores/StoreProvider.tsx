import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { useSyncExternalStore } from 'react';
import type { PanelID, TraceSnapshot } from '../trace/types';
import type { TraceState } from './TraceStore';
import { TraceStore } from './TraceStore';
import { SelectionStore } from './SelectionStore';
import type { SelectionState } from './SelectionStore';
import { FilterStore } from './FilterStore';
import type { TraceFilters } from '../trace/projections';

export interface StoreContextValue {
  traceState: TraceState;
  trace: TraceStore;
  selectionState: SelectionState;
  selection: SelectionStore;
  filters: TraceFilters;
  filtersStore: FilterStore;
  openPanel: (panel: PanelID) => void;
}

const StoreContext = createContext<StoreContextValue | null>(null);

export interface StoreProviderProps {
  children: ReactNode;
  trace?: TraceStore;
  selection?: SelectionStore;
  filters?: FilterStore;
  openPanel?: (panel: PanelID) => void;
}

export function StoreProvider({ children, trace, selection, filters, openPanel }: StoreProviderProps) {
  const [ownedTrace] = useState(() => trace ?? new TraceStore());
  const [ownedSelection] = useState(() => selection ?? new SelectionStore());
  const [ownedFilters] = useState(() => filters ?? new FilterStore());
  const [openPanelRef] = useState(() => openPanel ?? (() => undefined));

  useEffect(() => {
    void ownedTrace.start();
    return () => {
      ownedTrace.dispose();
    };
  }, [ownedTrace]);

  const traceState = useSyncExternalStore(
    (listener) => ownedTrace.subscribe(listener),
    () => ownedTrace.getSnapshot(),
  );
  const selectionState = useSyncExternalStore(
    (listener) => ownedSelection.subscribe(listener),
    () => ownedSelection.getSnapshot(),
  );
  const filterState = useSyncExternalStore(
    (listener) => ownedFilters.subscribe(listener),
    () => ownedFilters.getSnapshot(),
  );

  useEffect(() => {
    const snapshot: TraceSnapshot | null = traceState.snapshot;
    if (snapshot === null) {
      return;
    }
    ownedSelection.reconcile(
      new Set(snapshot.timeline.rows.map((row) => row.id)),
      new Set(snapshot.events.map((event) => event.seq)),
    );
  }, [ownedSelection, traceState.snapshot]);

  const value = useMemo<StoreContextValue>(
    () => ({
      traceState,
      trace: ownedTrace,
      selectionState,
      selection: ownedSelection,
      filters: filterState,
      filtersStore: ownedFilters,
      openPanel: openPanelRef,
    }),
    [traceState, selectionState, filterState, ownedTrace, ownedSelection, ownedFilters, openPanelRef],
  );

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useStore(): StoreContextValue {
  const value = useContext(StoreContext);
  if (value === null) {
    throw new Error('useStore must be used inside StoreProvider');
  }
  return value;
}

export function useTraceState(): TraceState {
  return useStore().traceState;
}

export function useSelection(): SelectionState {
  return useStore().selectionState;
}

export function useFilters(): TraceFilters {
  return useStore().filters;
}
