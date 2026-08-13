import { useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { ReactNode, Ref } from 'react';

export interface VirtualListHandle {
  scrollIntoView(index: number): void;
}

interface VirtualListProps<T> {
  items: T[];
  rowHeight: number;
  height: number;
  overscan: number;
  getKey: (item: T) => string;
  renderRow: (item: T, index: number) => ReactNode;
  listRef?: Ref<VirtualListHandle>;
}

export function VirtualList<T>({
  items,
  rowHeight,
  height,
  overscan,
  getKey,
  renderRow,
  listRef,
}: VirtualListProps<T>) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [scrollTop, setScrollTop] = useState(0);

  useImperativeHandle(listRef, () => ({
    scrollIntoView(index: number): void {
      const element = scrollRef.current;
      if (element === null) {
        return;
      }
      const targetTop = index * rowHeight;
      if (targetTop < element.scrollTop) {
        element.scrollTop = targetTop;
        setScrollTop(targetTop);
      } else if (targetTop + rowHeight > element.scrollTop + height) {
        element.scrollTop = targetTop + rowHeight - height;
        setScrollTop(element.scrollTop);
      }
    },
  }));

  const start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const end = Math.min(items.length, Math.ceil((scrollTop + height) / rowHeight) + overscan);

  const visible = useMemo(() => {
    const slice: { item: T; index: number }[] = [];
    for (let index = start; index < end; index++) {
      slice.push({ item: items[index], index });
    }
    return slice;
  }, [items, start, end]);

  return (
    <div
      className="vt-scroll"
      ref={scrollRef}
      onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
    >
      <div className="vt-spacer" style={{ height: items.length * rowHeight }}>
        {visible.map(({ item, index }) => (
          <div key={getKey(item)} style={{ position: 'absolute', top: index * rowHeight, left: 0, right: 0 }}>
            {renderRow(item, index)}
          </div>
        ))}
      </div>
    </div>
  );
}
