import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { vi } from 'vitest';
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

it('代码块带复制按钮，点击后写入剪贴板并给出反馈', async () => {
  const user = userEvent.setup();
  const spy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
  render(<MarkdownContent text={'```js\nconst a = 1;\n```'} />);
  const pre = document.querySelector('.code-block pre');
  expect(pre).toHaveTextContent('const a = 1;');
  await user.click(screen.getByLabelText('复制代码'));
  expect(spy).toHaveBeenCalledWith('const a = 1;');
  expect(screen.getByLabelText('已复制')).toBeInTheDocument();
  spy.mockRestore();
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
  // 公式经 KaTeX 渲染：不再是原始 latex 源码文本
  expect(document.querySelector('.math-block .katex')).not.toBeNull();
  expect(document.querySelector('.math-block')).not.toHaveTextContent('\\begin{pmatrix}');
  expect(document.querySelector('blockquote')).toHaveTextContent('引用说明');
});

it('行内公式经 KaTeX 渲染（不再是紫色等宽源码）', () => {
  render(<MarkdownContent text={'爱因斯坦的质能方程 $E = mc^2$ 很重要'} />);
  expect(document.querySelector('.katex')).not.toBeNull();
  expect(document.querySelector('code.math-inline')).toBeNull();
});

it('公式解析失败（流式残式）时回退原样源码展示', () => {
  // $$ 未闭合的多行块在解析器中被吃为一整个 math 块，残缺 latex 无法被 KaTeX 解析
  render(<MarkdownContent text={'$$\\frac{a}{$$'} />);
  expect(document.querySelector('.math-block .katex')).toBeNull();
  expect(document.querySelector('.math-block pre')).toHaveTextContent('\\frac{a}{');
});
