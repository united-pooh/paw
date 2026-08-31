import { render, screen } from '@testing-library/react';
import { MarkdownContent } from './MarkdownContent';

it('renders HTML as text and removes dangerous links', () => {
  render(<MarkdownContent text={'<script>window.hacked=1</script>\n\n[bad](javascript:alert(1))\n\n[good](https://example.com)'} />);
  expect(screen.queryByRole('script')).not.toBeInTheDocument();
  expect(screen.getByText(/window.hacked/)).toBeInTheDocument();
  expect(screen.getByText('bad')).not.toHaveAttribute('href');
  expect(screen.getByRole('link', { name: 'good' })).toHaveAttribute('href', 'https://example.com/');
});
