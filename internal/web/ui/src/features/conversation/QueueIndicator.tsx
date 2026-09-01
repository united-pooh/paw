export function QueueIndicator({ count }: { count: number }) {
  if (count <= 0) return null;
  return <div className="queue-indicator" role="status">已排队 {count} 条消息</div>;
}
