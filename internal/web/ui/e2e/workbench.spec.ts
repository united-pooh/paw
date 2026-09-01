import { expect, test, type Page } from '@playwright/test';
import { readFileSync, existsSync } from 'node:fs';

// The fixture server prints a single-use bootstrap token to its log. The
// token is exchanged once through the API request context (which shares the
// browser context's cookie jar), so both tests reuse the same session.

function bootstrapToken(): string {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (existsSync('/tmp/paw-webfixture.log')) {
      const match = readFileSync('/tmp/paw-webfixture.log', 'utf8').match(/https?:\/\/\S+#bootstrap=\S+/);
      if (match) {
        return new URLSearchParams(match[0].trim().split('#')[1] ?? '').get('bootstrap') ?? '';
      }
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
  }
  throw new Error('fixture bootstrap URL did not appear in /tmp/paw-webfixture.log');
}

let sessionCookie: { name: string; value: string } | null = null;

async function authenticate(page: Page): Promise<void> {
  if (sessionCookie) {
    await page.context().addCookies([{ ...sessionCookie, domain: '127.0.0.1', path: '/' }]);
    return;
  }
  // The HttpOnly session cookie is not visible to page JS and the
  // APIRequestContext does not share the browser's cookie jar, so the
  // Set-Cookie value is copied into the context explicitly.
  const response = await page.request.post('/api/auth/exchange', {
    data: { token: bootstrapToken() },
    headers: { 'Content-Type': 'application/json' }
  });
  if (!response.ok) throw new Error(`bootstrap exchange failed: ${response.status}`);
  const setCookie = response.headers()['set-cookie'] ?? '';
  await response.dispose();
  const [pair] = setCookie.split(';');
  const [name, value] = pair.split('=');
  if (!name || !value) throw new Error('fixture did not return a session cookie');
  sessionCookie = { name, value };
  await page.context().addCookies([{ ...sessionCookie, domain: '127.0.0.1', path: '/' }]);
}

async function openWorkbench(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto('/');
}

test('workbench lists the seeded session and replays its conversation', async ({ page }) => {
  await openWorkbench(page);
  await expect(page.getByText('工作区包含 README.md 与 internal/ 目录。')).toBeVisible({ timeout: 15_000 });
});

test('composer submits a message and streams the fixture response', async ({ page }) => {
  await openWorkbench(page);
  // 第一个快照在 bootstrap 后异步加载；等待消息输入框变为可写，
  // 说明会话快照（含正确 session_version）已就绪。
  await expect(page.getByLabel('消息')).toBeEditable({ timeout: 15_000 });
  const composer = page.getByLabel('消息');
  await composer.fill('hello fixture');
  await composer.press('Enter');
  // fixture runner 同步完成 turn；等待用户消息出现说明提交已生效。
  await expect(page.locator('article.message.assistant', { hasText: 'hello fixture' })).toBeVisible({ timeout: 15_000 });
  // 提交后立刻检查用户消息的 article 是否包含助手回复；fixture 是同步完成。
  // 提交后刷新由 App.refreshNow 触发；助手回复渲染到对话视图后断言。
  await expect(page.locator('.conversation-view', { hasText: 'fixture 回复：已收到消息 hello fixture' })).toBeVisible({ timeout: 15_000 });
});
