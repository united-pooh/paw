import { reducer, initialState, type StoreAction, type WorkbenchState } from './reducer';

export class WorkbenchStore {
  private state: WorkbenchState = initialState;
  private readonly listeners = new Set<() => void>();

  getSnapshot = (): WorkbenchState => this.state;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  dispatch(action: StoreAction): void {
    this.state = reducer(this.state, action);
    for (const listener of this.listeners) listener();
  }
}
