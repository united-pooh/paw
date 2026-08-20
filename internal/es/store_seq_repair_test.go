package es

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seqTestEnvelope(typ string, n int) Envelope {
	payload, _ := json.Marshal(map[string]int{"n": n})
	return Envelope{
		Type:          typ,
		OccurredAt:    time.Unix(int64(n)+1, 0).UTC(),
		SchemaVersion: 1,
		Kind:          KindDomain,
		Payload:       payload,
	}
}

// TestAppendAcrossStoreInstancesKeepsSeqContiguous 模拟多 paw 进程（两个
// JSONLStore 实例）交错追加同一流：序号必须以磁盘为准连续分配。回归：旧
// 实现每实例缓存 lastSeq，A 写 1-3、B 写 4-5 后，A 仍按缓存的 3 续写，
// 产生重复 seq 4（Load 报 seq gap，actor 激活失败）。
func TestAppendAcrossStoreInstancesKeepsSeqContiguous(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	storeA, err := NewJSONLStore(dir, "task")
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewJSONLStore(dir, "task")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, _, err := storeA.Append(ctx, "agg", []Envelope{seqTestEnvelope("e", i)}); err != nil {
			t.Fatalf("A append %d: %v", i, err)
		}
	}
	first, last, err := storeB.Append(ctx, "agg", []Envelope{seqTestEnvelope("e", 3), seqTestEnvelope("e", 4)})
	if err != nil {
		t.Fatalf("B append: %v", err)
	}
	if first != 4 || last != 5 {
		t.Fatalf("B assigned seqs %d-%d, want 4-5", first, last)
	}
	// A 的进程内状态停留在 3：旧实现此处会写重复 seq 4。新实现以磁盘为准。
	first, last, err = storeA.Append(ctx, "agg", []Envelope{seqTestEnvelope("e", 5)})
	if err != nil {
		t.Fatalf("A append after B: %v", err)
	}
	if first != 6 || last != 6 {
		t.Fatalf("A assigned seqs %d-%d, want 6-6", first, last)
	}

	envs, truncated, err := storeA.Load(ctx, "agg")
	if err != nil {
		t.Fatalf("Load after interleaved appends: %v", err)
	}
	if truncated || len(envs) != 6 {
		t.Fatalf("Load = %d events truncated=%v, want 6 contiguous", len(envs), truncated)
	}
	for i, env := range envs {
		if env.Seq != int64(i+1) {
			t.Fatalf("env[%d].Seq = %d, want %d", i, env.Seq, i+1)
		}
	}
}

// writeCorruptStream 写入一个「合法前缀 + seq 违例尾部」的损坏流
// （多实例双写者交错形态）。
func writeCorruptStream(t *testing.T, path string, validSeqs, tailSeqs []int64) {
	t.Helper()
	var buf strings.Builder
	write := func(seq int64, n int) {
		env := seqTestEnvelope("e", n)
		env.Seq = seq
		line, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	for i, seq := range validSeqs {
		write(seq, i)
	}
	for i, seq := range tailSeqs {
		write(seq, 100+i)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRepairSeqGapsTruncatesViolatingTail 验证截掉首个 seq 违例起的尾部后
// Load 恢复可读，合法前缀完整保留，且流可继续追加。
func TestRepairSeqGapsTruncatesViolatingTail(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := NewJSONLStore(dir, "task")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "task", "agg.events.jsonl")
	// 合法 1-5 之后另一个写者从 3 重写（交错双写形态）。
	writeCorruptStream(t, path, []int64{1, 2, 3, 4, 5}, []int64{3, 4, 5, 6})

	if _, _, err := store.Load(ctx, "agg"); err == nil || !strings.Contains(err.Error(), "seq gap") {
		t.Fatalf("Load before repair should hit seq gap, got %v", err)
	}
	dropped, err := store.RepairSeqGaps(ctx, "agg")
	if err != nil {
		t.Fatalf("RepairSeqGaps: %v", err)
	}
	if dropped != 4 {
		t.Fatalf("dropped = %d, want 4 tail events", dropped)
	}
	envs, _, err := store.Load(ctx, "agg")
	if err != nil {
		t.Fatalf("Load after repair: %v", err)
	}
	if len(envs) != 5 || envs[len(envs)-1].Seq != 5 {
		t.Fatalf("prefix after repair = %d events, want 5 with tail seq 5", len(envs))
	}
	first, _, err := store.Append(ctx, "agg", []Envelope{seqTestEnvelope("e", 999)})
	if err != nil {
		t.Fatalf("Append after repair: %v", err)
	}
	if first != 6 {
		t.Fatalf("Append after repair assigned %d, want 6", first)
	}
	// 再次修复应为 no-op。
	if dropped, err := store.RepairSeqGaps(ctx, "agg"); err != nil || dropped != 0 {
		t.Fatalf("second repair = dropped %d err %v, want no-op", dropped, err)
	}
}

// TestRepairSeqGapsReportsMidStreamCorruption 验证中部 JSON 损坏不被静默截掉。
func TestRepairSeqGapsReportsMidStreamCorruption(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir, "task")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "task", "agg.events.jsonl")
	goodEnv := seqTestEnvelope("e", 0)
	goodEnv.Seq = 1
	goodLine, _ := json.Marshal(goodEnv)
	// seq=1 合法行 + 中部坏行 + 更多行：不是序号问题，必须报错。
	body := string(goodLine) + "\n{\"broken\n" + string(goodLine) + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RepairSeqGaps(context.Background(), "agg"); err == nil {
		t.Fatal("mid-stream corruption should not be silently repaired")
	} else if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v, want malformed event", err)
	}
}
