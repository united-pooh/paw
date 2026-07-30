# Transcript Turn Duration Implementation Plan

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist each completed main-session model turn's duration and response time, then render the metadata as a gray footer directly below the corresponding assistant answer without adding it to model context.

**Architecture:** Keep `transcript.jsonl` message-only and add a session sidecar at `.paw/sessions/<session-id>/turns.jsonl`. The loop records timing only after the normal message commit succeeds, while Bubble Tea receives the same result for immediate rendering and decorates restored assistant entries from the sidecar. The existing Runner and SessionStore interfaces remain backward-compatible through optional capability interfaces, so current test doubles and subagent Activity data are unaffected.

**Tech Stack:** Go 1.25, JSONL session storage, Bubble Tea, Lip Gloss, existing `colorContextFree` muted style.

---

### Task 1: Add durable turn metadata and sequence-aware transcript records

**Files:**
- Create: `internal/session/turns.go`
- Modify: `internal/session/jsonl_store.go:175-270,326-345`
- Modify: `internal/session/store.go:30-35`
- Test: `internal/session/turns_test.go`
- Test: `internal/session/jsonl_store_test.go`

- [ ] **Step 1: Write the failing storage tests**

Add tests that create a temporary JSONLStore, append user/assistant messages, append one `TurnMetadata`, and verify the sidecar JSON contains `turn_id`, `assistant_seq`, `started_at`, `response_at`, `duration_ms`, and `status`. Add a second test that writes one malformed sidecar line between two valid JSON lines and verifies `LoadTurnMetadata` returns both valid records in file order without failing.

```go
func TestJSONLStoreTurnMetadataRoundTrip(t *testing.T) {
	store := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil { t.Fatal(err) }
	if err := store.Append(ctx, "s1", message.Message{Role: message.RoleUser, Content: "hi"}, message.Message{Role: message.RoleAssistant, Content: "hello"}); err != nil { t.Fatal(err) }
	seq := int64(1)
	started := time.Date(2026, 7, 30, 7, 45, 0, 0, time.UTC)
	response := started.Add(95 * time.Second)
	want := TurnMetadata{TurnID: "turn-1", AssistantSeq: &seq, StartedAt: started, ResponseAt: &response, DurationMS: 95000, Status: TurnStatusCompleted}
	if err := store.AppendTurnMetadata(ctx, "s1", want); err != nil { t.Fatal(err) }
	got, err := store.LoadTurnMetadata(ctx, "s1")
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) { t.Fatalf("got %#v, err %v", got, err) }
}
```

- [ ] **Step 2: Run the focused storage tests and confirm the new API is missing**

Run: `go test ./internal/session -run 'TestJSONLStoreTurnMetadataRoundTrip|TestJSONLStoreTurnMetadataSkipsCorruptLines' -count=1`

Expected: FAIL because `TurnMetadata`, `AppendTurnMetadata`, and `LoadTurnMetadata` do not exist yet.

- [ ] **Step 3: Implement the sidecar model and JSONL operations**

Define `TurnStatus` values `completed`, `failed`, and `stopped`, plus:

```go
type TurnMetadata struct {
	TurnID       string     `json:"turn_id"`
	AssistantSeq *int64     `json:"assistant_seq,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	ResponseAt   *time.Time `json:"response_at,omitempty"`
	DurationMS   int64      `json:"duration_ms"`
	Status       TurnStatus `json:"status"`
}

type TurnMetadataStore interface {
	AppendTurnMetadata(context.Context, string, TurnMetadata) error
	LoadTurnMetadata(context.Context, string) ([]TurnMetadata, error)
}
```

Implement append-only `turns.jsonl` writes with `O_APPEND|O_CREATE|O_WRONLY`, create the session directory when needed, and make reads skip blank or malformed lines while still returning filesystem and context errors. Export `TurnMetadataPath` for tests and diagnostics. Do not add these records to `Store.LoadResolvedHistory`.

- [ ] **Step 4: Refactor transcript append to expose assigned sequence numbers without changing `Store`**

Add an optional method on `JSONLStore`:

```go
func (s *JSONLStore) AppendWithSequences(ctx context.Context, sessionID string, msgs ...message.Message) (firstSeq, lastSeq int64, err error)
```

Make `Append` call the same internal writer and discard the range. Preserve current session creation and validation behavior. Add `LoadResolvedRecords` returning `[]Record` using the existing parent/fork resolution rules; make `LoadResolvedHistory` project those records to messages. This lets session restore associate `assistant_seq` exactly while keeping all existing callers source-compatible.

- [ ] **Step 5: Run storage verification**

Run: `go test ./internal/session -count=1`

Expected: PASS, including existing fork/list/attachment-adjacent session tests and the new sidecar corruption test.

### Task 2: Record timing at the loop commit boundary

**Files:**
- Create: `internal/loop/turn_timing.go`
- Modify: `internal/loop/runner.go:20-55,150-260,450-500`
- Modify: `internal/loop/streamma_mode.go:196-325`
- Test: `internal/loop/turn_timing_test.go`

- [ ] **Step 1: Add timing lifecycle tests**

Use a fake model/store and a deterministic `nowFn` on the Runner. Verify `RunTurnWithTiming` returns `TurnExecution.Metadata` with the supplied `turn_id`, UTC timestamps, `duration_ms` covering the entire model/tool loop, and the final assistant record sequence. Verify the model response still succeeds when the metadata sidecar fails after three persistence attempts, and verify ordinary `RunTurn` remains unchanged for legacy fake stores.

```go
func TestRunTurnWithTimingPersistsAfterAssistantCommit(t *testing.T) {
	// The fake model emits a completed assistant message; the fake store records
	// transcript append and sidecar append, then asserts finalSeq is the assistant seq.
}
```

- [ ] **Step 2: Implement optional timing capabilities**

Define in `internal/loop/turn_timing.go`:

```go
type TurnTiming struct { TurnID string; StartedAt time.Time }
type TurnExecution struct {
	Message            message.Message
	Metadata           session.TurnMetadata
	MetadataPersistErr error
}
type SequencedHistoryStore interface {
	AppendWithSequences(context.Context, string, ...message.Message) (int64, int64, error)
}
type TimedRunner interface {
	RunTurnWithTiming(context.Context, string, string, time.Time) (TurnExecution, error)
	RunRichTurnWithTiming(context.Context, message.Message, string, time.Time) (TurnExecution, error)
}
```

Refactor the internal turn function to accept an optional `*TurnTiming`; keep `RunTurn` and `RunRichTurn` as compatibility wrappers. Change `commitHistory` to return the last assigned sequence, using `SequencedHistoryStore` when available and `-1` for old stores. After a successful commit, set `ResponseAt` from `runner.nowFn`, calculate non-negative millisecond duration from the supplied start time, and append the sidecar record through `session.TurnMetadataStore` with exactly three context-aware attempts. Sidecar errors populate `MetadataPersistErr` and never replace a successful assistant response with a turn error.

Update the `/streamma` commit call to consume the new sequence return and run the same completion metadata path. Do not emit metadata for validation, model, tool, cancellation, or max-round errors because they have no completed final assistant answer.

- [ ] **Step 3: Add metadata loading to the real Runner**

Implement `LoadTurnMetadata(ctx, sessionID)` on `*loop.Runner` by delegating to the optional `session.TurnMetadataStore`; return an empty slice when the backing store has no timing capability. Keep `LoadHistory` and context construction unchanged, so sidecar data is never passed to the model.

- [ ] **Step 4: Run loop verification**

Run: `go test ./internal/loop -count=1`

Expected: PASS, including existing tool-loop, streamma, history rollback, and new timing lifecycle tests.

### Task 3: Pass completed timing results through Bubble Tea

**Files:**
- Modify: `internal/ui/bubble/commands.go:1-40,145-155`
- Modify: `internal/ui/bubble/input.go:286-298`
- Modify: `internal/ui/bubble/types.go:40-80,100-115,268-280,345-380`
- Modify: `internal/ui/bubble/app.go:175-220`
- Modify: `internal/ui/bubble/bubble.go:18-55`

- [ ] **Step 1: Add a UI command test for the timed capability**

Create a fake timed runner that captures `turnID` and `startedAt`, returns a completed `loop.TurnExecution`, and assert the resulting `turnFinishedMsg` contains metadata and a sidecar error without converting either into the model error. Keep existing fake runners on the old path to prove compatibility.

- [ ] **Step 2: Thread a turn ID and submit timestamp through start paths**

Add `turnID string` to `appModel`, generate a non-empty local ID when a turn actually starts, and pass `m.turnID` plus `m.turnStartedAt` to `runTurnCmd` for both immediate and queued turns. `runTurnCmd` should call `loop.TimedRunner` for rich or plain input when implemented, then return:

```go
turnFinishedMsg{err: err, metadata: metadataPointer, metadataErr: execution.MetadataPersistErr, restoreDraft: restoreDraft}
```

Use the old Runner/RichInputRunner methods when the optional capability is absent.

- [ ] **Step 3: Attach metadata only to the finished main assistant entry**

Extend `turnFinishedMsg` with `metadata *session.TurnMetadata` and `metadataErr error`. In the success branch of `appModel.Update`, capture the finalized assistant index before resetting `doneAssistant`, attach the metadata, call `touchTranscriptEntry`, and leave failures/cancellations without a footer. If `metadataErr` is non-nil, add one `error` entry after the answer so the UI reports a persistence problem while preserving the answer and its temporary footer.

Do not change `subagentFinishedMsg`, `subagentTranscriptPreview`, `Activity`, `/tasks`, or tool status records.

- [ ] **Step 4: Run Bubble Tea package tests**

Run: `go test ./internal/ui/bubble -count=1`

Expected: PASS, with existing queue, cancellation, rich clipboard, theme, scroll, and transcript tests unchanged.

### Task 4: Render the exact gray transcript footer

**Files:**
- Modify: `internal/ui/bubble/types.go:40-75`
- Modify: `internal/ui/bubble/transcript.go:15-60,571-620,721-745`
- Create: `internal/ui/bubble/turn_timing.go`
- Test: `internal/ui/bubble/turn_timing_test.go`

- [ ] **Step 1: Write pure formatting and rendering tests**

Assert `formatTurnFooter` returns the exact shape `1m35s · 07:47:47 AM` for a 95-second metadata record, formats the response timestamp with `ResponseAt.Local()` and layout `03:04:05 PM`, and renders no labels. Assert `renderEntryAt` places the footer after assistant content, gives it the same foreground as `contextFreeStyle`, and does not render it for user/tool/error entries. Assert the footer text is absent from the message body passed to the model-history test helper.

```go
got := formatTurnFooter(session.TurnMetadata{DurationMS: 95000, ResponseAt: timePtr(time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC))})
want := "1m35s · " + time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC).Local().Format("03:04:05 PM")
if got != want { t.Fatalf("formatTurnFooter() = %q, want %q", got, want) }
```

- [ ] **Step 2: Add the transcript metadata field and cache key coverage**

Add `turnMetadata *session.TurnMetadata` to `transcriptEntry`. Include all metadata fields in `transcriptRenderCacheKey` so attaching or restoring a footer invalidates cached rendering even when the assistant body is unchanged.

- [ ] **Step 3: Implement footer formatting and rendering**

Format duration by integer seconds using `duration_ms/1000`: below one minute `Ns`, otherwise `NmSs` with unpadded seconds, matching the accepted `1m35s` shape. Format `ResponseAt.Local()` with `03:04:05 PM`. In `renderEntryAt`, append `contextFreeStyle.Render(formatTurnFooter(*entry.turnMetadata))` after the already-rendered assistant body, then let the existing transcript gutter indent it. Keep the footer outside Markdown rendering so it cannot enter context or alter assistant content.

- [ ] **Step 4: Run rendering tests**

Run: `go test ./internal/ui/bubble -run 'TestFormatTurnFooter|TestRenderAssistantTurnFooter|TestTranscriptRenderCacheIncludesTurnMetadata' -count=1`

Expected: PASS with exact visible text and muted color assertions.

### Task 5: Restore persisted footers for the current session

**Files:**
- Modify: `internal/ui/bubble/bubble.go:45-52`
- Modify: `internal/ui/bubble/session_picker.go:48-95`
- Modify: `internal/ui/bubble/subagent_picker.go:180-205` only to keep record-entry helper shared and subagent behavior unchanged
- Modify: `internal/session/jsonl_store.go:231-270`
- Test: `internal/ui/bubble/turn_timing_test.go`

- [ ] **Step 1: Add optional resolved-record loading to session restore**

Define:

```go
type ResolvedRecordLoader interface {
LoadResolvedRecords(context.Context, string) ([]session.Record, error)
}
```

Change `loadSessionHistoryCmd` to accept the existing `SessionStore`. It still calls `runner.LoadHistory` first; when the store implements `ResolvedRecordLoader`, use records for `createdAt`, `Seq`, and message-to-entry construction, otherwise retain the current message-only fallback.

- [ ] **Step 2: Decorate restored assistant entries from sidecar records**

When the store also implements `session.TurnMetadataStore`, load metadata and build `map[int64]session.TurnMetadata` by `AssistantSeq`. For each resolved record, attach the matching metadata to the last assistant entry produced from that record. If the sidecar is missing, empty, or contains corrupt lines, restore the transcript normally without an error. Keep sidecar metadata out of the messages returned to `Runner.LoadHistory`.

- [ ] **Step 3: Test restore and latest-three visibility**

Create a temporary session with four user/assistant pairs and four sidecar records, restore it through `loadSessionHistoryCmd`, and assert all four assistant entries receive their own footer; the viewport's latest three therefore remain visible exactly as ordinary transcript history. Add a test with a missing sidecar and one with a malformed line to assert graceful restore.

### Task 6: Full verification and scope audit

**Files:**
- Modify: only the files listed in Tasks 1-5.

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w internal/session/turns.go internal/session/jsonl_store.go internal/session/store.go internal/session/turns_test.go internal/loop/turn_timing.go internal/loop/runner.go internal/loop/streamma_mode.go internal/loop/turn_timing_test.go internal/ui/bubble/commands.go internal/ui/bubble/input.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/bubble.go internal/ui/bubble/transcript.go internal/ui/bubble/turn_timing.go internal/ui/bubble/turn_timing_test.go internal/ui/bubble/session_picker.go internal/ui/bubble/subagent_picker.go`

- [ ] **Step 2: Run the required test and static checks**

Run:

```bash
go test ./internal/session ./internal/loop ./internal/ui/bubble -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

Expected: all commands exit zero; no metadata appears in prompts or `LoadResolvedHistory`; no Activity/subagent files are changed.

- [ ] **Step 3: Inspect the final diff for unrelated edits**

Run: `git diff --stat && git diff -- internal/session internal/loop internal/ui/bubble`

Confirm only the timing feature files changed, preserve all pre-existing user modifications, and report any inability to validate a real PTY separately from passing unit tests.
