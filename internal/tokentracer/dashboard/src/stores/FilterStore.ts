import type { TraceFilters } from '../trace/projections';
import { defaultFilters } from '../trace/projections';

export class FilterStore {
  private state: TraceFilters = { ...defaultFilters };
  private readonly listeners = new Set<() => void>();

  setScope(scope: TraceFilters['scope']): void {
    this.setState({ scope });
  }

  setModel(model: string | null): void {
    this.setState({ model });
  }

  setErrorsOnly(errorsOnly: boolean): void {
    this.setState({ errorsOnly });
  }

  reset(): void {
    this.setState({ ...defaultFilters });
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  getSnapshot(): TraceFilters {
    return this.state;
  }

  private setState(patch: Partial<TraceFilters>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) {
      listener();
    }
  }
}
