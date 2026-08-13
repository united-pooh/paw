import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

type Edge = 'left' | 'right' | 'top' | 'bottom';

async function settleLayout(page: Page): Promise<void> {
  // Initial restoration plus the default split sizing run asynchronously;
  // wait for the layout to stop moving before interacting with panels.
  await expect
    .poll(async () => {
      const first = await page.getByTestId('panel-heatmap').boundingBox();
      await page.waitForTimeout(150);
      const second = await page.getByTestId('panel-heatmap').boundingBox();
      return first !== null && second !== null && first.x === second.x && first.y === second.y && first.width === second.width;
    })
    .toBe(true);
}

async function dockToEdge(page: Page, panelId: string, targetId: string, edge: Edge): Promise<void> {
  const tab = page.getByTestId(`panel-tab-${panelId}`);
  await tab.scrollIntoViewIfNeeded();
  await expect
    .poll(async () => {
      const first = await tab.boundingBox();
      await page.waitForTimeout(120);
      const second = await tab.boundingBox();
      return first !== null && second !== null && first.x === second.x && first.y === second.y;
    })
    .toBe(true);
  let targetBox = await page.getByTestId(`panel-${targetId}`).boundingBox();
  expect(targetBox).not.toBeNull();
  let targetPosition = { x: targetBox!.x + targetBox!.width / 2, y: targetBox!.y + targetBox!.height / 2 };
  if (edge === 'left') {
    targetPosition = { x: targetBox!.x + 8, y: targetBox!.y + targetBox!.height / 2 };
  } else if (edge === 'right') {
    targetPosition = { x: targetBox!.x + targetBox!.width - 8, y: targetBox!.y + targetBox!.height / 2 };
  } else if (edge === 'top') {
    targetPosition = { x: targetBox!.x + targetBox!.width / 2, y: targetBox!.y + 8 };
  } else {
    targetPosition = { x: targetBox!.x + targetBox!.width / 2, y: targetBox!.y + targetBox!.height - 8 };
  }
  const tabBox = await tab.boundingBox();
  expect(tabBox).not.toBeNull();
  await page.mouse.move(tabBox!.x + 12, tabBox!.y + tabBox!.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(120);
  await page.mouse.move(targetBox!.x + targetBox!.width / 2, targetBox!.y + targetBox!.height / 2, { steps: 10 });
  await page.waitForTimeout(300);
  // The drop preview shifts the layout; re-measure the target mid-drag.
  const liveBox = await page.getByTestId(`panel-${targetId}`).boundingBox();
  if (liveBox !== null) {
    targetBox = liveBox;
    targetPosition = { x: liveBox.x + liveBox.width / 2, y: liveBox.y + liveBox.height / 2 };
    if (edge === 'left') {
      targetPosition = { x: liveBox.x + 8, y: liveBox.y + liveBox.height / 2 };
    } else if (edge === 'right') {
      targetPosition = { x: liveBox.x + liveBox.width - 8, y: liveBox.y + liveBox.height / 2 };
    } else if (edge === 'top') {
      targetPosition = { x: liveBox.x + liveBox.width / 2, y: liveBox.y + 8 };
    } else {
      targetPosition = { x: liveBox.x + liveBox.width / 2, y: liveBox.y + liveBox.height - 8 };
    }
  }
  await page.mouse.move(targetPosition.x, targetPosition.y, { steps: 6 });
  await page.waitForTimeout(250);
  await page.mouse.up();
  await expect
    .poll(async () => {
      const box = await page.getByTestId(`panel-${panelId}`).boundingBox();
      const tbox = await page.getByTestId(`panel-${targetId}`).boundingBox();
      if (box === null || tbox === null) {
        return false;
      }
      // Adjacent panel content boxes are separated by the group tab bar
      // (~35px) and sash gutters, so the tolerance covers those gaps.
      switch (edge) {
        case 'left':
          return Math.abs(box.x + box.width - tbox.x) < 60;
        case 'right':
          return Math.abs(tbox.x + tbox.width - box.x) < 60;
        case 'top':
          return Math.abs(box.y + box.height - tbox.y) < 60;
        case 'bottom':
          return Math.abs(tbox.y + tbox.height - box.y) < 60;
      }
    })
    .toBe(true);
}

async function dockAsTab(page: Page, panelId: string, targetId: string): Promise<void> {
  const tab = page.getByTestId(`panel-tab-${panelId}`);
  await tab.scrollIntoViewIfNeeded();
  await expect
    .poll(async () => {
      const first = await tab.boundingBox();
      await page.waitForTimeout(120);
      const second = await tab.boundingBox();
      return first !== null && second !== null && first.x === second.x && first.y === second.y;
    })
    .toBe(true);
  const targetTab = page.getByTestId(`panel-tab-${targetId}`);
  await targetTab.scrollIntoViewIfNeeded();
  let targetBox = await targetTab.boundingBox();
  expect(targetBox).not.toBeNull();
  const tabBox = await tab.boundingBox();
  expect(tabBox).not.toBeNull();
  await page.mouse.move(tabBox!.x + 12, tabBox!.y + tabBox!.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(120);
  await page.mouse.move(targetBox!.x + targetBox!.width / 2, targetBox!.y + targetBox!.height / 2, { steps: 10 });
  await page.waitForTimeout(300);
  // The drop preview shifts the layout; re-measure the target tab mid-drag.
  const liveBox = await targetTab.boundingBox();
  if (liveBox !== null) {
    targetBox = liveBox;
  }
  await page.mouse.move(targetBox!.x + targetBox!.width * 0.25, targetBox!.y + targetBox!.height / 2, { steps: 6 });
  await page.waitForTimeout(250);
  await page.mouse.up();
  await expect(page.getByTestId(`panel-${panelId}`)).toBeVisible();
  await expect
    .poll(async () => {
      const shared = await page.evaluate(
        ([aId, bId]) => {
          const a = document.querySelector(`[data-testid="panel-tab-${aId}"]`);
          const b = document.querySelector(`[data-testid="panel-tab-${bId}"]`);
          if (a === null || b === null) {
            return false;
          }
          const stripA = a.closest('.dv-tabs-container');
          const stripB = b.closest('.dv-tabs-container');
          return stripA !== null && stripA === stripB;
        },
        [panelId, targetId],
      );
      return shared;
    })
    .toBe(true);
}

async function resizeNearestSash(page: Page, panelId: string, delta: number): Promise<void> {
  const box = await page.getByTestId(`panel-${panelId}`).boundingBox();
  expect(box).not.toBeNull();
  const findSash = (): Promise<{ x: number; y: number; horizontal: boolean } | null> =>
    page.evaluate(
      (panel: { x: number; y: number; width: number; height: number }) => {
        let best: { x: number; y: number; horizontal: boolean } | null = null;
        let bestDist = Infinity;
        for (const element of Array.from(document.querySelectorAll('.dv-sash'))) {
          const rect = element.getBoundingClientRect();
          const horizontal = rect.width > rect.height;
          const centerX = rect.x + rect.width / 2;
          const centerY = rect.y + rect.height / 2;
          const dist = horizontal
            ? Math.min(
                Math.abs(centerY - panel.y),
                Math.abs(centerY - (panel.y + panel.height)),
              )
            : Math.min(
                Math.abs(centerX - panel.x),
                Math.abs(centerX - (panel.x + panel.width)),
              );
          if (dist < bestDist) {
            bestDist = dist;
            best = { x: centerX, y: centerY, horizontal };
          }
        }
        return bestDist < 80 ? best : null;
      },
      { x: box!.x, y: box!.y, width: box!.width, height: box!.height },
    );
  let sash: { x: number; y: number; horizontal: boolean } | null = null;
  for (let attempt = 0; attempt < 20 && sash === null; attempt++) {
    sash = await findSash();
    if (sash === null) {
      await page.waitForTimeout(150);
    }
  }
  expect(sash).not.toBeNull();
  await page.mouse.move(sash!.x, sash!.y);
  await page.mouse.down();
  await page.waitForTimeout(100);
  await page.mouse.move(sash!.x + (sash!.horizontal ? 0 : delta), sash!.y + (sash!.horizontal ? delta : 0), {
    steps: 8,
  });
  await page.waitForTimeout(150);
  await page.mouse.up();
  await expect
    .poll(async () => {
      const after = await page.getByTestId(`panel-${panelId}`).boundingBox();
      if (after === null) {
        return false;
      }
      if (sash!.horizontal) {
        return Math.abs(after.height - box!.height) > delta * 0.5;
      }
      return Math.abs(after.width - box!.width) > delta * 0.5;
    })
    .toBe(true);
}

test('docks, tabs, resizes, persists, closes, restores, resets, and undoes', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await settleLayout(page);
  await dockToEdge(page, 'events', 'heatmap', 'left');
  await dockToEdge(page, 'events', 'heatmap', 'right');
  await dockToEdge(page, 'events', 'heatmap', 'top');
  await dockToEdge(page, 'events', 'heatmap', 'bottom');
  await dockAsTab(page, 'events', 'inspector');
  await resizeNearestSash(page, 'calls', 120);
  await page.getByTestId('panel-calls').click();
  await page.getByLabel('最大化 Calls Table').click();
  await expect(page.getByTestId('panel-calls')).toBeVisible();
  await page.getByLabel('恢复 Calls Table').click();
  await page.getByTestId('panel-events').click();
  await page.getByLabel('浮动 Events').click();
  await expect(page.getByTestId('panel-events')).toHaveAttribute('data-location', 'floating');
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1')))
    .not.toBeNull();
  const saved = await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'));
  await page.reload();
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'))).toBe(saved);
  await page.getByTestId('panel-calls').click();
  await page.getByLabel('关闭 Calls Table').click();
  await page.getByRole('button', { name: '添加面板' }).click();
  await page.getByRole('menuitem', { name: 'Calls Table' }).click();
  await page.getByRole('button', { name: '恢复默认布局' }).click();
  await page.getByRole('button', { name: '撤销布局恢复' }).click();
});

test('links selection across table, flame, and inspector without filtering', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await page.getByTestId('panel-tab-inspector').click();
  await expect(page.locator('.inspector')).toContainText('选择调用、事件或时间桶查看详情');
  await page.getByRole('row', { name: 'critic' }).click();
  await expect(page.locator('.inspector')).toContainText('critic');
  await expect(page.locator('.inspector')).toContainText('fixture failure');
  const flameNode = page.getByTestId('flame-node-agent:turn-2:critic:0');
  await expect(flameNode).toHaveClass(/selected/);
  const rowsBefore = await page.locator('.ct-row').count();
  const heatmap = page.getByRole('img', { name: 'Token Heatmap' });
  const box = await heatmap.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box!.x + box!.width * 0.2, box!.y + box!.height * 0.5);
  await page.mouse.down();
  await page.mouse.move(box!.x + box!.width * 0.6, box!.y + box!.height * 0.5, { steps: 8 });
  await page.mouse.up();
  expect(await page.locator('.ct-row').count()).toBe(rowsBefore);
});

test('survives EventSource failures while keeping the last snapshot', async ({ page }) => {
  await page.route('**/events', (route) => route.abort());
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await expect(page.getByText('重新连接中')).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await page.unroute('**/events');
  await expect(page.getByText('实时')).toBeVisible({ timeout: 20_000 });
});

test('recovers from a corrupted layout with a single backup key', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await page.evaluate(() => localStorage.setItem('paw.tokenTracer.layout.v1', '{'));
  await page.reload();
  await expect(page.getByTestId('panel-calls')).toBeVisible();
  await expect(page.getByTestId('panel-heatmap')).toBeVisible();
  await expect(page.getByTestId('panel-flame')).toBeVisible();
  await expect(page.getByTestId('panel-events')).toBeVisible();
  await expect(page.getByTestId('panel-tab-inspector')).toBeVisible();
  await page.getByTestId('panel-tab-inspector').click();
  await expect(page.getByTestId('panel-inspector')).toBeVisible();
  const recoveryKeys = await page.evaluate(() =>
    Object.keys(localStorage).filter((key) => key.startsWith('paw.tokenTracer.layout.recovery.')),
  );
  expect(recoveryKeys).toHaveLength(1);
});

test('narrow mode preserves the desktop layout and returns to it', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1')))
    .not.toBeNull();
  const savedBefore = await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'));
  await page.setViewportSize({ width: 760, height: 900 });
  await expect(page.getByRole('tablist', { name: 'Token Tracer panels' })).toBeVisible();
  await page.getByRole('tab', { name: 'Events' }).click();
  await expect(page.getByTestId('panel-events')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('paw.tokenTracer.layout.v1'))).toBe(savedBefore);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(page.getByTestId('panel-heatmap')).toBeVisible();
  await expect(page.getByTestId('panel-calls')).toBeVisible();
});

test('keeps the events list responsive at 2000 entries', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.topbar-pipeline')).toBeVisible();
  const rows = page.locator('.event-row');
  await expect(rows.first()).toBeVisible();
  const scroll = page.locator('.events-list .vt-scroll');
  await scroll.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  await expect.poll(() => rows.count()).toBeLessThan(50);
  await expect(rows.last()).toBeVisible();
});
