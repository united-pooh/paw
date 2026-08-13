import { render, screen } from '@testing-library/react';
import { App } from './App';

test('renders the fixed Token Tracer shell', () => {
  render(<App />);
  expect(screen.getByRole('banner')).toHaveTextContent('Token Tracer');
  expect(screen.getByRole('main')).toBeInTheDocument();
});
