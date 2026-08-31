import type { StreamingPart } from '../../api/types';

export function TraceView({ parts, selected, onSelect }: { parts: Record<string, StreamingPart>; selected?: string; onSelect: (id: string) => void }) {
  return <section className="trace-view" aria-label="轨迹">
    <h2>运行轨迹</h2>
    {Object.values(parts).map((part) => <button type="button" key={part.part_id} className={part.part_id === selected ? 'selected' : ''} onClick={() => onSelect(part.part_id)}>
      <span className="trace-dot" /><div><strong>{part.kind}</strong><small>{part.completed ? '已完成' : '流式进行中'}</small></div>
    </button>)}
  </section>;
}
