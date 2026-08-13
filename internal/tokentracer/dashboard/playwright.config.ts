import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';

const tokenTracerDir = fileURLToPath(new URL('..', import.meta.url));

export default defineConfig({
  testDir: './e2e',
  timeout: 45_000,
  use: {
    baseURL: 'http://127.0.0.1:18999',
    viewport: { width: 1440, height: 1000 },
    colorScheme: 'light',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'go run ./testdata/dashboardfixture -port 18999',
    url: 'http://127.0.0.1:18999/healthz',
    cwd: tokenTracerDir,
    reuseExistingServer: false,
    timeout: 60_000
  }
});
