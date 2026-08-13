import type { PanelID, TimeRange } from '../trace/types';

export interface SelectionState {
  selectedRowID: string | null;
  selectedEventSeq: number | null;
  selectedTimeRange: TimeRange | null;
  source: PanelID | null;
}

export class SelectionStore {
  private state: SelectionState = {
    selectedRowID: null,
    selectedEventSeq: null,
    selectedTimeRange: null,
    source: null,
  };
  private readonly listeners = new Set<() => void>();

  selectRow(rowID: string, source: PanelID): void {
    this.setState({
      selectedRowID: rowID,
      selectedEventSeq: null,
      source,
    });
  }

  selectEvent(seq: number, rowID: string | null, source: PanelID): void {
    this.setState({
      selectedEventSeq: seq,
      selectedRowID: rowID,
      source,
    });
  }

  selectRange(range: TimeRange, source: PanelID): void {
    this.setState({ selectedTimeRange: range, source });
  }

  clear(): void {
    this.setState({
      selectedRowID: null,
      selectedEventSeq: null,
      selectedTimeRange: null,
      source: null,
    });
  }

  reconcile(rowIDs: Set<string>, eventSeqs: Set<number>): void {
    const patch: Partial<SelectionState> = {};
    if (this.state.selectedRowID !== null && !rowIDs.has(this.state.selectedRowID)) {
      patch.selectedRowID = null;
    }
    if (this.state.selectedEventSeq !== null && !eventSeqs.has(this.state.selectedEventSeq)) {
      patch.selectedEventSeq = null;
    }
    if (Object.keys(patch).length > 0) {
      this.setState(patch);
    }
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  getSnapshot(): SelectionState {
    return this.state;
  }

  private setState(patch: Partial<SelectionState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) {
      listener();
    }
  }
}
