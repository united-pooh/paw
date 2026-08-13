package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"paw/internal/session"
)

// Searcher 抽象 transcript 检索能力（session.JSONLStore 实现）。
type Searcher interface {
	SearchTranscript(ctx context.Context, sessionID, query string, limit int) ([]session.TranscriptHit, int, error)
}

// Tool 是 search_transcript 工具：按关键字检索当前会话的历史对话
// （设计文档 §5.3 / D11——显式返回命中数与可检索范围，0 命中提示
// 「查不到 ≠ 不存在」）。
type Tool struct {
	store     Searcher
	sessionID string
}

func New(store Searcher, sessionID string) *Tool {
	return &Tool{store: store, sessionID: sessionID}
}

// Bind 在 store/sessionID 就绪后绑定（cmd/agent 延迟注入模式）。
// 未绑定时 Run 返回明确错误。
func (t *Tool) Bind(store Searcher, sessionID string) {
	t.store = store
	t.sessionID = sessionID
}

func (t *Tool) Name() string { return "search_transcript" }

func (t *Tool) Description() string {
	return "在历史对话（transcript）中按关键字检索，用于取回窗口外被省略的旧对话细节（用户原话、被否决方案、旧错误信息等）。返回命中记录与可检索范围。"
}

func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "检索关键字（大小写不敏感）"},
			"limit": {"type": "integer", "description": "最多返回条数，默认 20，最大 50"}
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

type input struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if t == nil || t.store == nil || t.sessionID == "" {
		return "", fmt.Errorf("search_transcript: store or session unavailable")
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("search_transcript: 解析参数失败: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("search_transcript: query 不能为空")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	hits, searched, err := t.store.SearchTranscript(ctx, t.sessionID, in.Query, limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("未找到匹配 %q 的记录（已检索 %d 条）。注意：查不到 ≠ 不存在——可能超出检索范围或措辞不同，可换关键词重试。", in.Query, searched), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "matched=%d searched=%d\n", len(hits), searched)
	for _, h := range hits {
		timeStr := h.Time.Format("2006-01-02T15:04:05")
		role := h.Role
		if role == "" {
			role = "-"
		}
		content := h.Content
		if len([]rune(content)) > 200 {
			content = string([]rune(content)[:200]) + "…"
		}
		fmt.Fprintf(&b, "[%s %s %s] %s\n", h.TurnID, role, timeStr, strings.ReplaceAll(content, "\n", " "))
	}
	return b.String(), nil
}
