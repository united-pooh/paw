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
	Kind          string          `json:"kind,omitempty"` // runtime(sys.*) | domain；空=legacy，视为 domain
	Payload       json.RawMessage `json:"payload"`
}

// 事件命名空间（actor 运行时两段式 fold 使用；spec ADR-9）。
const (
	KindRuntime = "runtime"
	KindDomain  = "domain"
)

// ValidKinds 校验事件命名空间：空（legacy 视为 domain）、runtime、domain。
func validKind(kind string) bool {
	switch kind {
	case "", KindRuntime, KindDomain:
		return true
	}
	return false
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
	if !validKind(e.Kind) {
		return fmt.Errorf("es: invalid kind %q", e.Kind)
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
