import type { ReactNode } from 'react';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import hljs from 'highlight.js/lib/common';
import 'highlight.js/styles/atom-one-dark.css';
import { CopyButton } from './CopyButton';

/** KaTeX 渲染数学公式。解析失败时（如流式打字机的未闭合残式）回退原样源码展示。 */
function renderMath(source: string, displayMode: boolean): ReactNode {
  try {
    // output: 'html' 不生成 MathML 节点：jsdom 的无障碍树计算无法在 MathML 上工作，
    // 且纯 HTML 模式下 KaTeX 本身已提供 aria-label，屏幕阅读器体验不降级。
    const html = katex.renderToString(source, { displayMode, throwOnError: true, strict: 'ignore', output: 'html' });
    return <span className={displayMode ? 'math-display' : 'math-tex'} dangerouslySetInnerHTML={{ __html: html }} />;
  } catch {
    return displayMode
      ? <pre>{source}</pre>
      : <code className="math-inline">{source}</code>;
  }
}

function safeLink(href: string): string | null {
  try {
    const url = new URL(href, window.location.origin);
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
}

/** 行内语法：代码、粗体、斜体、行内公式、链接 */
function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  const pattern = /(`[^`]+`)|\*\*([^*]+)\*\*|\*([^*\n]+)\*|\$([^$\n]+)\$|\[([^\]]+)\]\(([^)]+)\)/g;
  let offset = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index ?? 0;
    if (index > offset) nodes.push(text.slice(offset, index));
    const key = `${keyPrefix}-${index}`;
    const [, code, bold, italic, math, linkText, linkHref] = match;
    if (code !== undefined) {
      nodes.push(<code key={key}>{code.slice(1, -1)}</code>);
    } else if (bold !== undefined) {
      nodes.push(<strong key={key}>{bold}</strong>);
    } else if (italic !== undefined) {
      nodes.push(<em key={key}>{italic}</em>);
    } else if (math !== undefined) {
      nodes.push(<span key={key}>{renderMath(math, false)}</span>);
    } else if (linkText !== undefined && linkHref !== undefined) {
      const href = safeLink(linkHref);
      nodes.push(href
        ? <a key={key} href={href} rel="noreferrer" target="_blank">{linkText}</a>
        : <span key={key}>{linkText}</span>);
    }
    offset = index + match[0].length;
  }
  if (offset < text.length) nodes.push(text.slice(offset));
  return nodes;
}

type Block =
  | { type: 'code'; content: string; lang: string }
  | { type: 'math'; content: string }
  | { type: 'heading'; level: number; content: string }
  | { type: 'table'; header: string[]; rows: string[][] }
  | { type: 'list'; ordered: boolean; items: string[] }
  | { type: 'quote'; content: string }
  | { type: 'hr' }
  | { type: 'paragraph'; content: string };

function splitTableRow(line: string): string[] {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((cell) => cell.trim());
}

function isTableSeparator(line: string): boolean {
  return /^\|?[\s:|-]+\|?$/.test(line.trim()) && line.includes('-');
}

function isBlockStarter(line: string): boolean {
  const trimmed = line.trim();
  return (
    trimmed === '' ||
    trimmed.startsWith('```') ||
    trimmed.startsWith('$$') ||
    /^#{1,6}\s/.test(trimmed) ||
    /^([-*]|\d+\.)\s/.test(trimmed) ||
    trimmed.startsWith('>') ||
    /^-{3,}$/.test(trimmed) ||
    trimmed.startsWith('|')
  );
}

function parseBlocks(text: string): Block[] {
  const lines = text.split('\n');
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();
    if (trimmed === '') { i++; continue; }

    // 代码块 ```lang ... ```（语言标识用于语法高亮）
    if (trimmed.startsWith('```')) {
      const lang = trimmed.slice(3).trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) { buf.push(lines[i]); i++; }
      i++; // 跳过收尾 ```
      blocks.push({ type: 'code', content: buf.join('\n'), lang });
      continue;
    }

    // 数学块 $$...$$（支持单行与多行）
    if (trimmed.startsWith('$$')) {
      const first = trimmed.slice(2);
      if (first.endsWith('$$')) {
        blocks.push({ type: 'math', content: first.slice(0, -2).trim() });
        i++;
        continue;
      }
      const buf: string[] = first ? [first] : [];
      i++;
      while (i < lines.length) {
        const current = lines[i].trim();
        i++;
        if (current.endsWith('$$')) {
          const tail = current.slice(0, -2);
          if (tail) buf.push(tail);
          break;
        }
        buf.push(current);
      }
      blocks.push({ type: 'math', content: buf.join('\n').trim() });
      continue;
    }

    // 标题 #..######
    const heading = /^(#{1,6})\s+(.*)$/.exec(trimmed);
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1].length, content: heading[2].trim() });
      i++;
      continue;
    }

    // 分割线 ---
    if (/^-{3,}$/.test(trimmed)) {
      blocks.push({ type: 'hr' });
      i++;
      continue;
    }

    // 表格 | a | b |
    if (trimmed.startsWith('|')) {
      const rows: string[] = [];
      while (i < lines.length && lines[i].trim().startsWith('|')) { rows.push(lines[i]); i++; }
      if (rows.length >= 2 && isTableSeparator(rows[1])) {
        blocks.push({ type: 'table', header: splitTableRow(rows[0]), rows: rows.slice(2).map(splitTableRow) });
      } else {
        blocks.push({ type: 'paragraph', content: rows.join('\n') });
      }
      continue;
    }

    // 列表 - / * / 1.
    const listMatch = /^([-*]|\d+\.)\s+(.*)$/.exec(trimmed);
    if (listMatch) {
      const ordered = /\d+\./.test(listMatch[1]);
      const items: string[] = [];
      while (i < lines.length) {
        const entry = /^([-*]|\d+\.)\s+(.*)$/.exec(lines[i].trim());
        if (!entry) break;
        items.push(entry[2]);
        i++;
      }
      blocks.push({ type: 'list', ordered, items });
      continue;
    }

    // 引用 >
    if (trimmed.startsWith('>')) {
      const buf: string[] = [];
      while (i < lines.length) {
        const quote = /^>\s?(.*)$/.exec(lines[i].trim());
        if (!quote) break;
        buf.push(quote[1]);
        i++;
      }
      blocks.push({ type: 'quote', content: buf.join('\n') });
      continue;
    }

    // 普通段落：累计到空行或下一块级元素
    const paragraph: string[] = [];
    while (i < lines.length && !isBlockStarter(lines[i])) {
      paragraph.push(lines[i]);
      i++;
    }
    blocks.push({ type: 'paragraph', content: paragraph.join('\n') });
  }
  return blocks;
}

const HEADING_TAGS = ['h1', 'h2', 'h3', 'h4', 'h5', 'h6'] as const;

/** 代码块高亮：按 fence 声明的语言走 highlight.js；未声明/未知语言原样展示。 */
function renderCode(content: string, lang: string): ReactNode {
  if (lang && hljs.getLanguage(lang)) {
    const result = hljs.highlight(content, { language: lang, ignoreIllegals: true });
    return <code className={`hljs language-${lang}`} dangerouslySetInnerHTML={{ __html: result.value }} />;
  }
  return content;
}

export function MarkdownContent({ text }: { text: string }) {
  return (
    <div className="markdown-content">
      {parseBlocks(text).map((block, index) => {
        const key = index;
        const keyPrefix = `b${index}`;
        switch (block.type) {
          case 'code':
            return (
              <div className="code-block" key={key}>
                {block.lang && <span className="code-lang">{block.lang}</span>}
                <pre>{renderCode(block.content, block.lang)}</pre>
                <CopyButton text={block.content} label="复制代码" />
              </div>
            );
          case 'math':
            return <div key={key} className="math-block">{renderMath(block.content, true)}</div>;
          case 'heading': {
            const Tag = HEADING_TAGS[Math.min(block.level, 6) - 1];
            return <Tag key={key}>{renderInline(block.content, keyPrefix)}</Tag>;
          }
          case 'table':
            return (
              <table key={key}>
                <thead><tr>{block.header.map((cell, cellIndex) => <th key={cellIndex}>{renderInline(cell, `${keyPrefix}h${cellIndex}`)}</th>)}</tr></thead>
                <tbody>{block.rows.map((row, rowIndex) => (
                  <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex}>{renderInline(cell, `${keyPrefix}r${rowIndex}c${cellIndex}`)}</td>)}</tr>
                ))}</tbody>
              </table>
            );
          case 'list':
            return block.ordered
              ? <ol key={key}>{block.items.map((item, itemIndex) => <li key={itemIndex}>{renderInline(item, `${keyPrefix}-${itemIndex}`)}</li>)}</ol>
              : <ul key={key}>{block.items.map((item, itemIndex) => <li key={itemIndex}>{renderInline(item, `${keyPrefix}-${itemIndex}`)}</li>)}</ul>;
          case 'quote':
            return <blockquote key={key}>{renderInline(block.content, keyPrefix)}</blockquote>;
          case 'hr':
            return <hr key={key} />;
          default:
            return <p key={key}>{renderInline(block.content, keyPrefix)}</p>;
        }
      })}
    </div>
  );
}
