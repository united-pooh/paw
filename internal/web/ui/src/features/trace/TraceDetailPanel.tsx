import type { StreamingPart } from '../../api/types';

export function TraceDetailPanel({ part, onClose }: { part?: StreamingPart; onClose: () => void }) {
  if (!part) return null;
  return <aside className="trace-detail" aria-label="轨迹详情"><header><strong>{part.kind}</strong><button type="button" onClick={onClose}>×</button></header><dl><dt>Part ID</dt><dd>{part.part_id}</dd><dt>Turn</dt><dd>{part.turn_id}</dd></dl><pre>{part.text}</pre></aside>;
}
