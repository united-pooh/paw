import { render, screen } from '@testing-library/react';
import { App } from './App';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) === '/api/bootstrap') {
      return new Response(JSON.stringify({ schema_version: 1, recent_workspaces: [], loaded_workspaces: [], loaded_runtimes: 0 }), { status: 200 });
    }
    return new Response('{}', { status: 404 });
  }));
});

afterEach(() => vi.unstubAllGlobals());

it('renders the empty workspace entry after bootstrap', async () => {
  render(<App />);
  expect(await screen.findByRole('heading', { name: '浏览器工作台' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '打开工作区' })).toBeInTheDocument();
});
