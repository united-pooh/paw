package app

import (
	"errors"
	"strings"
	"testing"
)

func TestTraceDetailStorePutGetAndTruncation(t *testing.T) {
	store := NewTraceDetailStore(nil)
	if _, ok := store.Put("tool_result", "  "); ok {
		t.Fatal("empty detail accepted")
	}
	id, ok := store.Put("tool_result", "full tool output")
	if !ok {
		t.Fatal("valid detail rejected")
	}
	detail, err := store.Get(nil, id)
	if err != nil || detail.Content != "full tool output" || detail.Truncated || detail.Kind != "tool_result" {
		t.Fatalf("detail = %#v, %v", detail, err)
	}

	big := strings.Repeat("x", TraceDetailMaxBytes+10)
	truncatedID, ok := store.Put("tool_result", big)
	if !ok {
		t.Fatal("oversized detail rejected")
	}
	truncated, err := store.Get(nil, truncatedID)
	if err != nil || !truncated.Truncated || len(truncated.Content) != TraceDetailMaxBytes {
		t.Fatalf("truncated detail = %#v, %v", truncated, err)
	}
}

func TestTraceDetailStoreRejectsInvalidID(t *testing.T) {
	store := NewTraceDetailStore(nil)
	if _, ok := store.Put("tool_result", "content"); !ok {
		t.Fatal("valid detail rejected")
	}
	for _, id := range []string{"", "../escape", "detail_gone"} {
		if _, err := store.Get(nil, id); !errors.Is(err, ErrTraceDetailNotFound) {
			t.Fatalf("Get(%q) error = %v", id, err)
		}
	}
}
