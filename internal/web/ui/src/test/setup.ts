import '@testing-library/jest-dom/vitest';

const storage = new Map<string, string>();
const localStorageStub: Storage = {
  get length() { return storage.size; },
  clear: () => storage.clear(),
  getItem: (key) => storage.get(key) ?? null,
  key: (index) => Array.from(storage.keys())[index] ?? null,
  removeItem: (key) => { storage.delete(key); },
  setItem: (key, value) => { storage.set(key, String(value)); }
};
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: localStorageStub });

if (!globalThis.crypto?.randomUUID) {
  Object.defineProperty(globalThis, 'crypto', {
    configurable: true,
    value: { randomUUID: () => '00000000-0000-4000-8000-000000000000' }
  });
}
