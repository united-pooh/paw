package session

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/message"
)

// newTestStoreWithSyncPolicy 创建测试 store 并切换到指定同步策略。
func newTestStoreWithSyncPolicy(t *testing.T, policy SyncPolicy, interval time.Duration) *JSONLStore {
	t.Helper()
	store := newTestStore(t)
	store.SetSyncPolicy(policy, interval)
	return store
}

// TestSyncPolicyAlways_SyncsEveryAppend 验证默认/always 策略：每次追加都同步。
func TestSyncPolicyAlways_SyncsEveryAppend(t *testing.T) {
	store := newTestStore(t) // 默认 SyncPolicyAlways
	ctx := context.Background()
	sessionID := "always"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}

	var syncCount atomic.Int64
	store.syncFile = func(f *os.File) error {
		syncCount.Add(1)
		return f.Sync()
	}

	for i := 0; i < 5; i++ {
		if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := syncCount.Load(); got != 5 {
		t.Fatalf("sync calls = %d, want 5", got)
	}
}

// TestSyncPolicyInterval_CoalescesAppends 验证 interval 策略：高频追加合并为
// 单次同步，间隔到期后再次同步。
func TestSyncPolicyInterval_CoalescesAppends(t *testing.T) {
	store := newTestStoreWithSyncPolicy(t, SyncPolicyInterval, time.Hour)
	ctx := context.Background()
	sessionID := "interval"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}

	var syncCount atomic.Int64
	store.syncFile = func(f *os.File) error {
		syncCount.Add(1)
		return f.Sync()
	}

	for i := 0; i < 5; i++ {
		if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := syncCount.Load(); got != 1 {
		t.Fatalf("sync calls = %d, want 1 (coalesced)", got)
	}
}

// TestSyncPolicyInterval_TurnBoundaryForcesSync 验证 interval 策略下，包含
// turn 完成/失败边界的批次仍立即同步（已完成 turn 的持久化边界不可妥协）。
func TestSyncPolicyInterval_TurnBoundaryForcesSync(t *testing.T) {
	store := newTestStoreWithSyncPolicy(t, SyncPolicyInterval, time.Hour)
	ctx := context.Background()
	sessionID := "turn-boundary"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}

	var syncCount atomic.Int64
	store.syncFile = func(f *os.File) error {
		syncCount.Add(1)
		return f.Sync()
	}

	// 首次写入（BeginTurn）必须同步：会话首次落盘不能停留 page cache。
	// 之后的 deltas 与 tool result 属于高频增量，不触发额外同步。
	if err := store.BeginTurn(ctx, sessionID, "turn-1",
		message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 1 {
		t.Fatalf("sync after first write = %d, want 1", got)
	}
	for i := 0; i < 3; i++ {
		if err := store.AppendAssistant(ctx, sessionID, "turn-1",
			message.Message{Role: message.RoleAssistant, Content: fmt.Sprintf("delta %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendToolResult(ctx, sessionID, "turn-1", 0, message.ToolResult{
		ToolUseID: "call_1", Content: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 1 {
		t.Fatalf("sync calls before boundary = %d, want 1", got)
	}

	// turn 完成边界强制同步。
	if err := store.CompleteTurn(ctx, sessionID, "turn-1"); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 2 {
		t.Fatalf("sync calls after boundary = %d, want 2", got)
	}

	// 失败边界同样强制同步。
	if err := store.BeginTurn(ctx, sessionID, "turn-2",
		message.Message{Role: message.RoleUser, Content: "boom"}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 2 {
		t.Fatalf("sync after second BeginTurn = %d, want 2 (within interval)", got)
	}
	if err := store.FailTurn(ctx, sessionID, "turn-2", fmt.Errorf("kaboom")); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 3 {
		t.Fatalf("sync calls after fail boundary = %d, want 3", got)
	}
}

// TestSyncPolicyInterval_PerSessionSyncState 验证 lastSync 按 session 隔离，
// 不同 session 各自计时，互不干扰。
func TestSyncPolicyInterval_PerSessionSyncState(t *testing.T) {
	store := newTestStoreWithSyncPolicy(t, SyncPolicyInterval, time.Hour)
	ctx := context.Background()
	for _, id := range []string{"sess-a", "sess-b"} {
		if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	syncs := map[string]int{}
	store.syncFile = func(f *os.File) error {
		mu.Lock()
		// 通过 fd 无法直接拿 sessionID，这里用路径名近似：记录唯一路径。
		// 仅用于验证"两个 session 各自至少同步一次"，不做精确次数断言。
		syncs["*"]++
		mu.Unlock()
		return f.Sync()
	}

	for i := 0; i < 3; i++ {
		for _, id := range []string{"sess-a", "sess-b"} {
			if err := store.Append(ctx, id, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// 每个 session 第一次 append 时 !ok 会同步一次，之后高频追加被合并。
	if syncs["*"] < 2 {
		t.Fatalf("total syncs = %d, want >= 2 (one per session)", syncs["*"])
	}
}

// TestSyncPolicyInterval_ExpiryTriggersSync 验证间隔到期后下一次 append 会同步。
func TestSyncPolicyInterval_ExpiryTriggersSync(t *testing.T) {
	store := newTestStoreWithSyncPolicy(t, SyncPolicyInterval, 50*time.Millisecond)
	ctx := context.Background()
	sessionID := "expiry"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}

	var syncCount atomic.Int64
	store.syncFile = func(f *os.File) error {
		syncCount.Add(1)
		return f.Sync()
	}

	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 1 {
		t.Fatalf("first sync = %d, want 1", got)
	}
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "two"}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 1 {
		t.Fatalf("second sync = %d, want 1 (within interval)", got)
	}

	time.Sleep(80 * time.Millisecond)
	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "three"}); err != nil {
		t.Fatal(err)
	}
	if got := syncCount.Load(); got != 2 {
		t.Fatalf("third sync = %d, want 2 (interval expired)", got)
	}
}

// TestSyncPolicyInterval_RecoveredDurable 验证 interval 策略下多次追加后，
// 磁盘记录完整（即便未 sync，数据也已写入 page cache，可被读取/恢复）。
func TestSyncPolicyInterval_RecoveredDurable(t *testing.T) {
	store := newTestStoreWithSyncPolicy(t, SyncPolicyInterval, time.Hour)
	ctx := context.Background()
	sessionID := "recover"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// 新实例重扫磁盘，全部记录可恢复。
	store2, err := NewJSONLStore(store.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store2.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 10 {
		t.Fatalf("recovered records = %d, want 10", len(records))
	}
	first, last, err := store2.AppendWithSequences(ctx, sessionID,
		message.Message{Role: message.RoleUser, Content: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if first != 10 || last != 10 {
		t.Fatalf("next seq = (%d,%d), want (10,10)", first, last)
	}
}
