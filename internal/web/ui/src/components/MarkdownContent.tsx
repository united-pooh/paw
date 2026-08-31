import type { ReactNode } from 'react';

function safeLink(href: string): string | null {
  try {
    const url = new URL(href, window.location.origin);
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
}

export function MarkdownContent({ text }: { text: string }) {
  const blocks = text.split(/\n{2,}/);
  return (
    <div className="markdown-content">
      {blocks.map((block, index) => {
        if (block.startsWith('```') && block.endsWith('```')) {
          return <pre key={index}>{block.slice(3, -3).trim()}</pre>;
        }
        const nodes: ReactNode[] = [];
        const pattern = /\[([^\]]+)\]\(([^)]+)\)/g;
        let offset = 0;
        for (const match of block.matchAll(pattern)) {
          nodes.push(block.slice(offset, match.index));
          const href = safeLink(match[2]);
          nodes.push(href ? <a key={`${index}-${match.index}`} href={href} rel="noreferrer" target="_blank">{match[1]}</a> : <span key={`${index}-${match.index}`}>{match[1]}</span>);
          offset = (match.index ?? 0) + match[0].length;
        }
        nodes.push(block.slice(offset));
        return <p key={index}>{nodes}</p>;
      })}
    </div>
  );
}
