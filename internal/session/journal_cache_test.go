package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"paw/internal/message"
)

// TestJournalCache_SequenceContinuity 验证连续多次追加时 sequence 连续且
// 与磁盘记录一致（单线程场景）。
func TestJournalCache_SequenceContinuity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "seq-cont"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	var wantFirst, wantLast int64 = 0, -1
	for batch := 0; batch < 5; batch++ {
		first, last, err := store.AppendWithSequences(ctx, sessionID,
			message.Message{Role: message.RoleUser, Content: fmt.Sprintf("m%d", batch*2)},
			message.Message{Role: message.RoleAssistant, Content: fmt.Sprintf("a%d", batch*2+1)},
		)
		if err != nil {
			t.Fatalf("AppendWithSequences batch %d: %v", batch, err)
		}
		if first != wantFirst {
			t.Fatalf("batch %d firstSeq = %d, want %d", batch, first, wantFirst)
		}
		wantLast = wantFirst + 1
		if last != wantLast {
			t.Fatalf("batch %d lastSeq = %d, want %d", batch, last, wantLast)
		}
		wantFirst = wantLast + 1
	}

	records, err := store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadResolvedJournalRecords: %v", err)
	}
	if len(records) != 10 {
		t.Fatalf("records = %d, want 10", len(records))
	}
	for i, rec := range records {
		if rec.Seq != int64(i) {
			t.Fatalf("record %d seq = %d, want %d", i, rec.Seq, i)
		}
	}
}

// TestJournalCache_ConcurrentAppends 验证并发追加（同一 session）时 sequence
// 仍然连续、无重复、无丢失。
func TestJournalCache_ConcurrentAppends(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "conc"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := store.Append(ctx, sessionID, message.Message{
					Role:    message.RoleUser,
					Content: fmt.Sprintf("w%d-%d", w, i),
				}); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent append error: %v", err)
	}

	records, err := store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadResolvedJournalRecords: %v", err)
	}
	if len(records) != workers*perWorker {
		t.Fatalf("records = %d, want %d", len(records), workers*perWorker)
	}
	seen := make(map[int64]bool, len(records))
	for _, rec := range records {
		if seen[rec.Seq] {
			t.Fatalf("duplicate seq %d", rec.Seq)
		}
		seen[rec.Seq] = true
	}
	for i := 0; i < len(records); i++ {
		if !seen[int64(i)] {
			t.Fatalf("missing seq %d", i)
		}
	}
}

// TestJournalCache_NewStoreRescansAfterRestart 模拟进程重启：用新的
// JSONLStore 实例指向同一目录，sequence 必须从磁盘重新扫描，不能依赖
// 旧进程的内存缓存。
func TestJournalCache_NewStoreRescansAfterRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	store1, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "restart"
	if _, err := store1.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store1.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("old-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	// 新实例 = 新进程，无内存缓存，必须重扫。
	store2, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, last, err := store2.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "after-restart"})
	if err != nil {
		t.Fatalf("AppendWithSequences on fresh store: %v", err)
	}
	if first != 3 || last != 3 {
		t.Fatalf("fresh store seq = (%d,%d), want (3,3)", first, last)
	}

	// 恢复后 sequence 仍连续。
	records, err := store2.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4", len(records))
	}
	for i, rec := range records {
		if rec.Seq != int64(i) {
			t.Fatalf("record %d seq = %d, want %d", i, rec.Seq, i)
		}
	}
}

// TestJournalCache_ExternalWriteInvalidates 验证外部进程（直接写 transcript
// 文件）后，缓存因文件大小变化而失效并重新扫描。
func TestJournalCache_ExternalWriteInvalidates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "ext-write"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "ours"}); err != nil {
		t.Fatal(err)
	}

	// 外部进程直接追加一条记录（模拟另一个进程写入同一个 journal）。
	ext := Record{Seq: 1, Kind: JournalMessage, Message: message.Message{Role: message.RoleUser, Content: "external"}}
	line, err := json.Marshal(ext)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.transcriptPath(sessionID), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// 缓存已失效（大小不匹配）：下一次 append 必须从 seq 2 继续。
	first, last, err := store.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "after-external"})
	if err != nil {
		t.Fatalf("AppendWithSequences: %v", err)
	}
	if first != 2 || last != 2 {
		t.Fatalf("seq = (%d,%d), want (2,2) after external write", first, last)
	}

	records, err := store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	if records[1].Message.Content != "external" || records[2].Message.Content != "after-external" {
		t.Fatalf("records = %#v, want external then after-external", records)
	}
}

// TestJournalCache_ForkResolvedHistory 验证 fork session 的 resolved history
// 与自身 append 的 sequence 不受缓存影响：fork 子会话有自己的 transcript，
// 其 sequence 从 0 开始，resolved history = 父会话前缀 + 子会话记录。
func TestJournalCache_ForkResolvedHistory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	parentID := "parent"
	childID := "child"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: parentID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := store.Append(ctx, parentID, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("p%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.Fork(ctx, ForkRequest{ParentSessionID: parentID, SessionID: childID, ForkFromSeq: 2}); err != nil {
		t.Fatalf("Fork: %v", err)
	}
	first, last, err := store.AppendWithSequences(ctx, childID,
		message.Message{Role: message.RoleUser, Content: "c0"})
	if err != nil {
		t.Fatalf("child append: %v", err)
	}
	if first != 0 || last != 0 {
		t.Fatalf("child first append seq = (%d,%d), want (0,0)", first, last)
	}

	history, err := store.LoadResolvedHistory(ctx, childID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("resolved history = %d, want 3 (2 parent + 1 child)", len(history))
	}
	if history[0].Content != "p0" || history[1].Content != "p1" || history[2].Content != "c0" {
		t.Fatalf("resolved history contents = %#v", history)
	}

	// 子会话继续 append，sequence 连续（子会话自身 0,1）。
	_, last, err = store.AppendWithSequences(ctx, childID,
		message.Message{Role: message.RoleUser, Content: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if last != 1 {
		t.Fatalf("child second append lastSeq = %d, want 1", last)
	}
}

// TestJournalCache_AppendToMissingTranscript 验证首次 append（transcript
// 文件尚不存在）与缓存状态初始化正确，后续 append 不再重扫。
func TestJournalCache_AppendToMissingTranscript(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "lazy-no-transcript"

	// 直接 append（CreateRoot 由 appendRecords 自动完成）。
	first, last, err := store.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "first"})
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	if first != 0 || last != 0 {
		t.Fatalf("first seq = (%d,%d), want (0,0)", first, last)
	}

	first, last, err = store.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "second"})
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if first != 1 || last != 1 {
		t.Fatalf("second seq = (%d,%d), want (1,1)", first, last)
	}
}

// TestJournalCache_ListSessionsUsesUncachedScan 验证 ListSessions 仍然通过
// 直接读取磁盘（readOwnRecords）计算 FirstMessage，不受 journal 缓存影响。
func TestJournalCache_ListSessionsUsesUncachedScan(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "list-scan"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: strings.Repeat("x", 100)}); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(summaries))
	}
	if got := summaries[0].FirstMessage; got != strings.Repeat("x", 80) {
		t.Fatalf("FirstMessage = %q, want 80 x's", got)
	}
	if summaries[0].TranscriptSize <= 0 {
		t.Fatalf("TranscriptSize = %d, want > 0", summaries[0].TranscriptSize)
	}
}

// TestJournalCache_ReadOwnRecordsStillTolerant 保证 readOwnRecords 的容错
// 语义（末尾撕裂行被忽略）在缓存重扫路径下保持不变。
func TestJournalCache_ReadOwnRecordsStillTolerant(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "torn-tail"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "ok"}); err != nil {
		t.Fatal(err)
	}
	// 追加一段无换行符的撕裂行。
	path := store.transcriptPath(sessionID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":1,"message":{"role":"user","content":"torn"}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	records, err := store.readOwnRecords(ctx, sessionID)
	if err != nil {
		t.Fatalf("readOwnRecords with torn tail: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1 (torn tail ignored)", len(records))
	}
	if records[0].Message.Content != "ok" {
		t.Fatalf("record content = %q, want ok", records[0].Message.Content)
	}

	// 通过 AppendWithSequences 走缓存路径：撕裂行导致 size 无法匹配缓存，
	// 应触发重扫并安全忽略撕裂行，sequence 从 1 继续。
	first, last, err := store.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "after-torn"})
	if err != nil {
		t.Fatalf("append after torn tail: %v", err)
	}
	if first != 1 || last != 1 {
		t.Fatalf("seq after torn tail = (%d,%d), want (1,1)", first, last)
	}
}

// TestJournalCache_SequenceOrderingAcrossSessions 验证多个 session 各自维护
// 独立缓存，互不干扰。
func TestJournalCache_SequenceOrderingAcrossSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	ids := []string{"s-a", "s-b", "s-c"}
	for _, id := range ids {
		if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for round := 0; round < 3; round++ {
		for _, id := range ids {
			if err := store.Append(ctx, id, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("%s-%d", id, round)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range ids {
		records, err := store.LoadResolvedJournalRecords(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 3 {
			t.Fatalf("%s records = %d, want 3", id, len(records))
		}
		for i, rec := range records {
			if rec.Seq != int64(i) {
				t.Fatalf("%s record %d seq = %d, want %d", id, i, rec.Seq, i)
			}
		}
		// 每个 session 的内容独立。
		var contents []string
		for _, rec := range records {
			contents = append(contents, rec.Message.Content)
		}
		sort.Strings(contents)
		for i := range contents {
			if contents[i] != fmt.Sprintf("%s-%d", id, i) {
				t.Fatalf("%s contents = %v", id, contents)
			}
		}
	}
}

// TestJournalCache_ExternalTruncateInvalidates 验证外部删除/截断 transcript
// 后缓存失效，sequence 从 0 重新开始（保持磁盘事实优先）。
func TestJournalCache_ExternalTruncateInvalidates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "ext-truncate"

	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "gone"}); err != nil {
		t.Fatal(err)
	}

	// 外部进程清空 transcript 文件。
	if err := os.WriteFile(store.transcriptPath(sessionID), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	first, last, err := store.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "fresh"})
	if err != nil {
		t.Fatalf("append after truncate: %v", err)
	}
	if first != 0 || last != 0 {
		t.Fatalf("seq after truncate = (%d,%d), want (0,0)", first, last)
	}

	// 与磁盘事实一致：文件里只有 fresh 一条。
	records, err := store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Message.Content != "fresh" {
		t.Fatalf("records after truncate = %#v", records)
	}
	_ = filepath.Join // keep import used
}
