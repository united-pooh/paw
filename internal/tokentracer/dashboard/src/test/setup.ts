import '@testing-library/jest-dom/vitest';

class ResizeObserverStub implements ResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

globalThis.ResizeObserver = ResizeObserverStub;

Object.defineProperty(navigator, 'clipboard', {
  configurable: true,
  value: { writeText: vi.fn() },
});

class EventSourceStub {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  readyState = 0;
  url: string;
  withCredentials = false;
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;

  constructor(url: string) {
    this.url = url;
  }

  addEventListener(): void {}
  removeEventListener(): void {}
  dispatchEvent(): boolean {
    return true;
  }
  close(): void {
    this.readyState = 2;
  }
}

if (typeof globalThis.EventSource === 'undefined') {
  globalThis.EventSource = EventSourceStub as unknown as typeof EventSource;
}

const storageBacking = new Map<string, string>();
const storageStub: Storage = {
  get length() {
    return storageBacking.size;
  },
  clear: () => storageBacking.clear(),
  getItem: (key) => (storageBacking.has(key) ? storageBacking.get(key)! : null),
  key: (index) => Array.from(storageBacking.keys())[index] ?? null,
  removeItem: (key) => {
    storageBacking.delete(key);
  },
  setItem: (key, value) => {
    storageBacking.set(key, String(value));
  },
};
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storageStub });

if (typeof window.matchMedia !== 'function') {
  window.matchMedia = ((query: string): MediaQueryList =>
    ({
      matches: false,
      media: query,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      onchange: null,
      dispatchEvent: () => true,
    }) as unknown as MediaQueryList) as typeof window.matchMedia;
}
