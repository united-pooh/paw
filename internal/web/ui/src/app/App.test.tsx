import { render, screen } from '@testing-library/react';
import { App } from './App';

it('renders the workbench shell', () => {
  render(<App />);
  expect(screen.getByRole('heading', { name: '浏览器工作台' })).toBeInTheDocument();
  expect(screen.getByLabelText('消息')).toBeInTheDocument();
});
