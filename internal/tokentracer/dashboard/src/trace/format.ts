import type { TokenPart, Usage } from './types';

const countFormat = new Intl.NumberFormat();

export function formatCount(value: number): string {
  return countFormat.format(Number.isFinite(value) ? value : 0);
}

export function formatDuration(ms: number): string {
  ms = Math.max(0, Math.round(Number.isFinite(ms) ? ms : 0));
  if (ms < 1000) {
    return `${ms}ms`;
  }
  const seconds = ms / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds % 60);
  return `${minutes}m${rest}s`;
}

const percentFormat = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

export function formatPercent(value: number): string {
  return `${percentFormat.format(Number.isFinite(value) ? value : 0)}%`;
}

export function formatThroughput(tokensPerSecond: number): string {
  return `${formatCount(Math.round(Number.isFinite(tokensPerSecond) ? tokensPerSecond : 0))}/s`;
}

export function tokenTotal(usage: Usage): number {
  return usage.input + usage.cache_read + usage.cache_creation + usage.output;
}

export interface UsagePart {
  key: TokenPart;
  label: string;
  value: number;
}

const PART_LABELS: Record<TokenPart, string> = {
  input: 'input',
  cache_read: 'cache read',
  cache_creation: 'cache creation',
  output: 'output',
};

export function usageParts(usage: Usage): UsagePart[] {
  return (['input', 'cache_read', 'cache_creation', 'output'] as const).map((key) => ({
    key,
    label: PART_LABELS[key],
    value: usage[key],
  }));
}
