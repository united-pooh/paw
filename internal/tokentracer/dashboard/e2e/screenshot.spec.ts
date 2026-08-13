import { test, expect } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const repoRoot = fileURLToPath(new URL('../../../..', import.meta.url));
const visualDir = path.join(repoRoot, '.agent', 'visual');

test('captures desktop and narrow workspace screenshots', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await page.waitForTimeout(900);
  await page.screenshot({
    path: path.join(visualDir, 'token-tracer-desktop.png'),
  });

  await page.setViewportSize({ width: 760, height: 900 });
  await page.waitForTimeout(500);
  await page.screenshot({
    path: path.join(visualDir, 'token-tracer-narrow.png'),
  });
});
