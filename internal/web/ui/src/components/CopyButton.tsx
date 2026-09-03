import { useState } from 'react';

function CopyGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </svg>
  );
}

function CheckGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M20 6 9 17l-5-5" />
    </svg>
  );
}

/** 通用复制按钮：写入剪贴板后短暂显示对勾反馈。 */
export function CopyButton({ text, label = '复制', className = '' }: { text: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // 剪贴板不可用（权限/非安全上下文）时静默失败，按钮状态不变
      return;
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button type="button" className={`copy-btn${copied ? ' copied' : ''}${className ? ` ${className}` : ''}`}
      aria-label={copied ? '已复制' : label} title={copied ? '已复制' : label} onClick={() => void copy()}>
      {copied ? <CheckGlyph /> : <CopyGlyph />}
    </button>
  );
}
