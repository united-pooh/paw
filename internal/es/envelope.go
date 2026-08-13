package es

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Envelope 是事件流中的统一信封。seq 在每条流内从 1 开始连续单调递增。
type Envelope struct {
	Seq           int64           `json:"seq"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// Validate 校验信封结构。seq 允许 0 基线（session 域历史流从 0 开始）；
// schema_version=0 表示 legacy 记录，允许零值 occurred_at；新事件
// （schema_version>0）必须携带时间戳。
func (e Envelope) Validate() error {
	if e.Seq < 0 {
		return fmt.Errorf("es: seq must not be negative, got %d", e.Seq)
	}
	if e.Type == "" {
		return fmt.Errorf("es: type is required")
	}
	if e.SchemaVersion < 0 {
		return fmt.Errorf("es: schema_version must not be negative, got %d", e.SchemaVersion)
	}
	if e.SchemaVersion > 0 && e.OccurredAt.IsZero() {
		return fmt.Errorf("es: occurred_at is required for schema_version %d", e.SchemaVersion)
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("es: payload is required")
	}
	trimmed := bytes.TrimSpace(e.Payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("es: payload must be a JSON object, got %s", truncated(trimmed, 32))
	}
	return nil
}

func truncated(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
