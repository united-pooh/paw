import { render, screen } from '@testing-library/react';
import { MarkdownContent } from './MarkdownContent';

it('renders HTML as text and removes dangerous links', () => {
  render(<MarkdownContent text={'<script>window.hacked=1</script>\n\n[bad](javascript:alert(1))\n\n[good](https://example.com)'} />);
  expect(screen.queryByRole('script')).not.toBeInTheDocument();
  expect(screen.getByText(/window.hacked/)).toBeInTheDocument();
  expect(screen.getByText('bad')).not.toHaveAttribute('href');
  expect(screen.getByRole('link', { name: 'good' })).toHaveAttribute('href', 'https://example.com/');
});

it('renders headings and bold text instead of raw markdown syntax', () => {
  render(<MarkdownContent text={'## 1. 求驻点\n\n核心是 **Hessian 矩阵**，用于判断驻点。'} />);
  expect(screen.getByRole('heading', { name: '1. 求驻点' })).toBeInTheDocument();
  expect(screen.getByText('Hessian 矩阵').tagName).toBe('STRONG');
  expect(screen.queryByText(/## 1/)).not.toBeInTheDocument();
  expect(screen.queryByText(/\*\*Hessian/)).not.toBeInTheDocument();
});

it('renders markdown tables, lists, math blocks and blockquotes', () => {
  const text = [
    '| 条件 | 结论 |',
    '|------|------|',
    '| $D > 0$ | 极小值 |',
    '',
    '- 第一项',
    '- 第二项',
    '',
    '$$H(x,y) = \\begin{pmatrix} a & b \\end{pmatrix}$$',
    '',
    '> 引用说明'
  ].join('\n');
  render(<MarkdownContent text={text} />);
  expect(screen.getByRole('table')).toBeInTheDocument();
  expect(screen.getByRole('columnheader', { name: '条件' })).toBeInTheDocument();
  expect(screen.getByRole('cell', { name: '极小值' })).toBeInTheDocument();
  expect(screen.getByRole('list')).toBeInTheDocument();
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(document.querySelector('.math-block')).toHaveTextContent('H(x,y) = \\begin{pmatrix} a & b \\end{pmatrix}');
  expect(document.querySelector('blockquote')).toHaveTextContent('引用说明');
});
