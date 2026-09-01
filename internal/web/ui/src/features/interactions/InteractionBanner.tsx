export type PendingInteractionState =
  | { kind: 'question'; requestID: string; question: { prompt: string; mode: string; options: { id: string; label: string; description?: string }[] } }
  | { kind: 'permission'; requestID: string; permission: { operation: string; canonical_target: string } };

export function InteractionBanner({
  pending,
  onAnswer,
  onDecide,
}: {
  pending: PendingInteractionState | null;
  onAnswer: (requestID: string, selectedOption: string) => void;
  onDecide: (requestID: string, decision: 'allow_once' | 'deny') => void;
}) {
  if (!pending) return null;
  if (pending.kind === 'question' && pending.question) {
    return (
      <div className="interaction-banner" role="alertdialog" aria-label="待回答问题">
        <strong>{pending.question.prompt}</strong>
        <div className="interaction-actions">
          {pending.question.options.map((option) => (
            <button key={option.id} type="button" onClick={() => onAnswer(pending.requestID, option.id)}>
              {option.label}
            </button>
          ))}
        </div>
      </div>
    );
  }
  if (pending.kind === 'permission' && pending.permission) {
    return (
      <div className="interaction-banner" role="alertdialog" aria-label="待确认权限">
        <strong>需要权限：{pending.permission.operation}</strong>
        <div className="permission-target">{pending.permission.canonical_target}</div>
        <div className="interaction-actions">
          <button type="button" onClick={() => onDecide(pending.requestID, 'allow_once')}>允许一次</button>
          <button type="button" onClick={() => onDecide(pending.requestID, 'deny')}>拒绝</button>
        </div>
      </div>
    );
  }
  return null;
}
