# Chat Queue TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the chat queue from transcript-only feedback into a compact, visibly distinct, editable and reorderable Bubble Tea queue panel without changing the existing serial turn execution model.

**Architecture:** Keep `CommandQueue` as the single FIFO execution source, but store stable-ID rich queue items instead of parallel string/draft slices. Add a queue interaction state machine to `appModel` with inactive, selecting, and editing modes; route queue key handling before history navigation and textarea handling. Render a compact one-line queue summary in normal input mode and expand it only while selecting, while keeping transcript entries as historical facts rather than the queue control surface.

**Tech Stack:** Go, Bubble Tea, Bubbles `textarea`, Lip Gloss, existing `internal/ui/bubble` test helpers, `go test`.

## Global Constraints

- Preserve the existing serial execution flow: `turnFinishedMsg → FinishModel() → DequeueDraft() → StartModel() → runTurnCmd()`.
- Do not change `QueryGuard`, current-turn supplement semantics, rich input/image propagation, or model cancellation semantics.
- Queue ordering is positional FIFO for this feature; Alt/Command J/K move items within the queue and do not interrupt the active turn.
- Normal mode uses a compact one-line queue summary; selecting mode expands the list; editing mode uses a distinct yellow `EDIT` visual state.
- `Esc` in edit mode cancels the edit and restores the original draft at its original queue position.
- `Enter` in edit mode saves the current draft to the queue tail; `:wq` is also recognized as an explicit save command when submitted with Enter.
- Both `Alt+J/K` and `Command+J/K` must invoke the same move operations; the application must tolerate terminals that do not deliver Command key events.
- Queue items containing image/input-token metadata must remain intact through enqueue, selection, edit cancel, edit save, reorder, and dequeue.
- Queue panel rendering must not depend on transcript scrolling or transcript cache state.
- Do not add implementation placeholders, unbounded queue-panel height, or a second background queue worker.

---

## File Map

- Modify: `internal/ui/bubble/command_queue.go` — replace parallel slices with stable-ID rich queue items and add inspection, removal, movement, insertion, and tail-save operations.
- Modify: `internal/ui/bubble/command_queue_test.go` — unit-test queue identity, cloning, FIFO, indexed removal, movement, insertion, clear, and metadata preservation.
- Modify: `internal/ui/bubble/types.go` — add queue interaction mode, selected item state, and edit state to `appModel`; define queue item types/constants in the existing Bubble Tea package.
- Modify: `internal/ui/bubble/input.go` — add queue selection/edit key routing, Down-entry gating, edit save/cancel behavior, queue submission updates, and transcript queue labeling.
- Modify: `internal/ui/bubble/input_token.go` — reuse existing draft cloning/setter behavior and, if needed, add cursor/input-source helpers needed to distinguish fresh input, history input, and queue edit input.
- Modify: `internal/ui/bubble/commands.go` — adapt `startNextQueuedTurn()` to dequeue the new queue item while retaining its draft and existing failure requeue behavior.
- Modify: `internal/ui/bubble/layout.go` — reserve one compact queue-summary line in normal mode and a bounded expanded queue panel in selecting/editing modes while prioritizing input height on small terminals.
- Create: `internal/ui/bubble/queue_render.go` — render normal summary, expanded selecting panel, edit-mode status, item summaries, image summaries, truncation, and key hints using the existing `StyleSet`.
- Modify: `internal/ui/bubble/styles.go` — add queue summary, selected-item, selecting label, edit label, edit border, and queue hint styles using theme colors already available to the TUI.
- Modify: `internal/ui/bubble/bubble_test.go` — test model-level key routing, queue entry, selection, movement, edit lifecycle, transcript behavior, and queued-turn regression cases.
- Modify: `internal/ui/bubble/fixed_layout_test.go` — test queue panel height accounting, compact summary height, expanded panel bounds, and small-terminal input priority.
- Create: `internal/ui/bubble/queue_render_test.go` — test rendered text, count/position, truncation, state labels, image summary, and bounded output.
- Modify: `docs/design-chat-queue-tui.md` — update the approved design with the concrete deletion/clear affordances and final API names if implementation details differ.

---

### Task 1: Replace the parallel queue storage with stable-ID rich items

**Files:**
- Modify: `internal/ui/bubble/command_queue.go`
- Modify: `internal/ui/bubble/command_queue_test.go`

**Interfaces:**
- Consumes: existing `inputDraft`, `cloneInputDraft`, and `trimInputDraft`.
- Produces: `queuedChatItem`, `CommandQueue.Items`, `CommandQueue.EnqueueDraft`, `CommandQueue.DequeueDraft`, `CommandQueue.RemoveAt`, `CommandQueue.Remove`, `CommandQueue.Move`, `CommandQueue.InsertAt`, `CommandQueue.Len`, and `CommandQueue.Clear` for the UI tasks.

- [ ] **Step 1: Define the queue item and public queue operations**

Use one item slice and a monotonic ID counter:

```go
type queuedChatItem struct {
    ID        string
    Draft     inputDraft
    CreatedAt time.Time
}

type CommandQueue struct {
    items  []queuedChatItem
    nextID uint64
}
```

Implement these signatures:

```go
func (q *CommandQueue) Items() []queuedChatItem
func (q *CommandQueue) Enqueue(input string) (string, bool)
func (q *CommandQueue) EnqueueDraft(draft inputDraft) (string, bool)
func (q *CommandQueue) Dequeue() (string, bool)
func (q *CommandQueue) DequeueDraft() (queuedChatItem, bool)
func (q *CommandQueue) RemoveAt(index int) (queuedChatItem, bool)
func (q *CommandQueue) Remove(id string) (queuedChatItem, bool)
func (q *CommandQueue) Move(id string, delta int) bool
func (q *CommandQueue) InsertAt(index int, item queuedChatItem) bool
func (q *CommandQueue) Len() int
func (q *CommandQueue) Clear()
```

`EnqueueDraft` must clone and trim the draft, reject whitespace-only text, assign an ID such as `queue-1`, and preserve image token metadata. `Items()` must return cloned drafts so callers cannot mutate queue storage accidentally. `Move` must clamp only through valid adjacent swaps: a move beyond either boundary returns `false` and leaves the slice unchanged. `InsertAt` must clamp an index into `[0,len(items)]` and clone the inserted item.

- [ ] **Step 2: Write failing queue behavior tests**

Add tests with concrete expectations:

```go
func TestCommandQueueAssignsIDsAndPreservesRichDraft(t *testing.T) {
    image := message.ImagePart{MIMEType: "image/png", Data: []byte{1, 2}}
    id, ok := q.EnqueueDraft(inputDraft{
        Text: "inspect this",
        Tokens: []inputToken{{Kind: inputTokenImage, Start: 0, End: 13, Image: &image}},
    })
    if !ok || id == "" { t.Fatalf("enqueue failed: %q", id) }
    items := q.Items()
    if len(items) != 1 || items[0].ID != id || len(items[0].Draft.Tokens) != 1 {
        t.Fatalf("unexpected queue snapshot: %#v", items)
    }
    items[0].Draft.Tokens[0].Image.Data[0] = 9
    if q.Items()[0].Draft.Tokens[0].Image.Data[0] != 1 {
        t.Fatal("Items returned mutable queue storage")
    }
}

func TestCommandQueueMoveRemoveAndRestore(t *testing.T) {
    first, _ := q.Enqueue("first")
    second, _ := q.Enqueue("second")
    third, _ := q.Enqueue("third")
    if !q.Move(third, -2) || q.Items()[0].ID != third { t.Fatal("move to front failed") }
    removed, ok := q.Remove(second)
    if !ok || removed.Draft.Text != "second" { t.Fatal("remove failed") }
    if !q.InsertAt(1, removed) || q.Items()[1].ID != second { t.Fatal("restore failed") }
    if q.Items()[2].ID != first { t.Fatal("unexpected order") }
}
```

Also test empty input rejection, FIFO dequeue, boundary moves, invalid IDs, `Clear`, and `Dequeue()` compatibility.

- [ ] **Step 3: Run the focused tests and verify they fail**

Run:

```bash
go test ./internal/ui/bubble -run 'TestCommandQueue' -count=1
```

Expected: FAIL because the current queue has parallel slices, boolean enqueue results, and no stable item operations.

- [ ] **Step 4: Implement the minimal queue storage and operations**

Remove the parallel `items []string`/`drafts []inputDraft` representation. Keep `Dequeue()` as a compatibility wrapper returning `item.Draft.Text`, and update callers later to consume `queuedChatItem`. Ensure all returned drafts and inserted items are cloned.

- [ ] **Step 5: Run focused and package tests**

Run:

```bash
go test ./internal/ui/bubble -run 'TestCommandQueue' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: queue tests pass; compile failures from call sites are resolved in the next task before the package suite is finally green.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/command_queue.go internal/ui/bubble/command_queue_test.go
git commit -m "refactor: make chat queue items addressable"
```

---

### Task 2: Add queue interaction state to the Bubble Tea model

**Files:**
- Modify: `internal/ui/bubble/types.go`
- Modify: `internal/ui/bubble/input_token.go`
- Modify: `internal/ui/bubble/app.go`

**Interfaces:**
- Consumes: queue APIs from Task 1 and existing `currentInputDraft`, `setInputDraft`, `resetHistoryNavigation`, and `historyIndex`.
- Produces: `queueInteractionMode`, `queueEditState`, `queueMode`, `queueSelectedID`, `queueEdit`, and helpers for selected index/source gating.

- [ ] **Step 1: Define the interaction state and input source**

Add:

```go
type queueInteractionMode uint8

const (
    queueModeInactive queueInteractionMode = iota
    queueModeSelecting
    queueModeEditing
)

type inputSource uint8

const (
    inputSourceFresh inputSource = iota
    inputSourceHistory
    inputSourceQueueEdit
)

type queueEditState struct {
    item       queuedChatItem
    originalAt int
}
```

Add to `appModel`:

```go
queueMode       queueInteractionMode
queueSelectedID  string
queueEdit        *queueEditState
inputSource     inputSource
```

Initialize `historyIndex` and `queueMode` consistently in `newModel`.

- [ ] **Step 2: Add state helper signatures**

Implement:

```go
func (m appModel) queueSelectedIndex() int
func (m appModel) queueSelectedItem() (queuedChatItem, bool)
func (m *appModel) setQueueSelection(id string)
func (m *appModel) normalizeQueueSelection()
func (m appModel) inputCursorAtEnd() bool
func (m appModel) canEnterQueueSelection() bool
func (m *appModel) leaveQueueSelection()
func (m *appModel) clearQueueEditState()
```

`canEnterQueueSelection` must require a non-empty queue, `queueModeInactive`, cursor at the end, no active history item (`historyIndex == -1` and `historyDownLock == false`), no completion/modal/terminal mode, and either an empty input or a fresh input. It must not require the model to be running; the queue remains inspectable while idle.

- [ ] **Step 3: Write failing state tests**

Add model-level tests for:

- an empty queue rejecting selection;
- a fresh input at cursor end accepting selection;
- a history input rejecting selection;
- a cursor before the end rejecting selection;
- selection IDs remaining valid after queue reorder/removal.

- [ ] **Step 4: Implement state initialization and normalization**

When `setInputDraft` is called by history navigation, set `inputSourceHistory`; when normal input changes, reset it to `inputSourceFresh`; when queue editing begins, set `inputSourceQueueEdit`. After any queue mutation, preserve the selected ID when possible and otherwise select the nearest remaining item.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/ui/bubble -run 'TestQueue.*State|Test.*QueueSelection' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/input_token.go internal/ui/bubble/app.go internal/ui/bubble/bubble_test.go
 git commit -m "feat: add queue interaction state"
```

---

### Task 3: Route keys for selection, movement, deletion, and edit entry

**Files:**
- Modify: `internal/ui/bubble/input.go`
- Modify: `internal/ui/bubble/bubble_test.go`

**Interfaces:**
- Consumes: Task 2 state helpers and Task 1 `Items`, `Remove`, and `Move` operations.
- Produces: `handleQueueKey`, `enterQueueSelection`, `moveSelectedQueueItem`, and `beginQueueEdit` integrated into `appModel.Update`/input routing.

- [ ] **Step 1: Define key routing behavior in tests**

Add tests that send `tea.KeyMsg` values through the same model update path used by the TUI:

```go
func TestDownEntersQueueSelectionForFreshInputAtEnd(t *testing.T) { /* queue has two items; assert queueModeSelecting */ }
func TestDownKeepsHistoryNavigationForHistoryInput(t *testing.T) { /* assert historyIndex changes, queue mode stays inactive */ }
func TestQueueSelectionMovesAndEscapes(t *testing.T) { /* up/down changes ID; esc leaves queue unchanged */ }
func TestQueueSelectionMovesWithAltAndCommandAliases(t *testing.T) { /* alt+k, alt+j, cmd+k, cmd+j */ }
func TestQueueSelectionDeletesSelectedItem(t *testing.T) { /* d removes only selected item */ }
func TestQueueSelectionEntersEditWithI(t *testing.T) { /* selected item removed and draft loaded */ }
```

Use the key strings Bubble Tea exposes (`alt+k`, `alt+j`, `cmd+k`, `cmd+j`) and route both aliases to the same delta function.

- [ ] **Step 2: Run the new tests and verify failure**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(Down|QueueSelection)' -count=1
```

Expected: FAIL because `Down` currently reaches history/textarea logic and no queue mode handler exists.

- [ ] **Step 3: Implement queue key handling before completion/history/textarea**

Add:

```go
func (m appModel) handleQueueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool)
```

In the `tea.KeyMsg` branch of `appModel.Update`, after other modal/picker handlers but before completion and transcript/history handling:

1. If `queueMode == queueModeSelecting`, consume every key through `handleQueueKey`.
2. If `queueMode == queueModeEditing`, defer to the edit handler from Task 4.
3. If inactive and `msg.String() == "down"` and `canEnterQueueSelection()` is true, enter selection and return without passing the key to history navigation.

Selection behavior:

- On entry, select the last queue item so the most recently queued message is immediately available.
- `up`/`down` move within bounds without wraparound.
- `alt+k`/`cmd+k` call `Move(selectedID, -1)`; `alt+j`/`cmd+j` call `Move(selectedID, +1)`.
- `d` removes the selected item and selects the nearest remaining item; if empty, leave selection mode.
- `esc` leaves selection mode and focuses textarea.
- `i` removes the selected item, stores its original index, loads its draft, focuses textarea, and enters edit mode.

Call `relayout`, `syncInputMode`, and cursor anchor updates after each visual state change.

- [ ] **Step 4: Implement transcript queue labeling without duplicate status entries**

Change `queueChatInput` to capture the returned queue ID and add one user transcript entry titled `you (queued #N)` or equivalent stable queue marker. Remove the extra `queued`/`queued for next turn` transcript entry. The queue panel remains the live source of count/order; transcript is historical only.

- [ ] **Step 5: Run focused and regression tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(Down|QueueSelection|QueueChatInput|StartNextQueuedTurn)' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: all focused tests pass; existing queue/FIFO tests remain green.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/input.go internal/ui/bubble/bubble_test.go
 git commit -m "feat: add queue selection and reordering controls"
```

---

### Task 4: Implement Queue Edit mode with cancel restoration and tail save

**Files:**
- Modify: `internal/ui/bubble/input.go`
- Modify: `internal/ui/bubble/input_token.go`
- Modify: `internal/ui/bubble/bubble_test.go`

**Interfaces:**
- Consumes: `queueEditState`, `setInputDraft`, `currentInputDraft`, and `CommandQueue.InsertAt`/`EnqueueDraft`.
- Produces: `beginQueueEdit`, `cancelQueueEdit`, `saveQueueEdit`, and edit-mode key routing.

- [ ] **Step 1: Write failing edit lifecycle tests**

Add tests with exact assertions:

```go
func TestQueueEditRemovesSelectedItemAndLoadsRichDraft(t *testing.T) { /* assert text, image token, mode editing, queue length decremented */ }
func TestQueueEditEscRestoresOriginalDraftAndPosition(t *testing.T) { /* modify textarea, esc, assert original text/ID/index */ }
func TestQueueEditEnterSavesModifiedDraftToTail(t *testing.T) { /* assert edited text is final item and mode inactive */ }
func TestQueueEditWqSavesCurrentDraftToTail(t *testing.T) { /* input :wq command path saves, not literal :wq */ }
func TestQueueEditEmptyEnterDoesNotEnqueue(t *testing.T) { /* assert item is not recreated */ }
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/ui/bubble -run 'TestQueueEdit' -count=1
```

Expected: FAIL because edit mode and restoration state are not implemented.

- [ ] **Step 3: Implement `beginQueueEdit`**

Remove the selected item by ID/index, store the complete item and original index in `queueEditState`, set the input draft through `setInputDraft`, clear completion and paste-fold state, set `inputSourceQueueEdit`, set `queueModeEditing`, focus the textarea, and relayout. Do not write a transcript entry for entering edit mode.

- [ ] **Step 4: Implement edit-mode submit interception**

Add:

```go
func (m appModel) handleQueueEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool)
func (m *appModel) cancelQueueEdit()
func (m *appModel) saveQueueEdit()
```

While editing:

- `esc` calls `cancelQueueEdit`, inserts the saved item at `originalAt`, clears edit state, clears the input, resets history navigation, and returns to inactive mode.
- `enter` calls `saveQueueEdit`; if the current draft text is non-empty, enqueue it at the tail and exit. If empty, exit without adding an item.
- A draft whose trimmed text is exactly `:wq` is treated as the explicit save command and must not enqueue literal `:wq`; otherwise Enter saves the current content to the tail.
- `ctrl+j`, `alt+enter`, and `shift+enter` retain existing textarea newline behavior and must not save.
- Other keys pass to the existing token-aware textarea updater.

After cancel/save, call `syncInputMode`, `relayout`, `applyCursorAnimation`, and focus the textarea. Preserve rich tokens on both restore and save.

- [ ] **Step 5: Run focused tests and full Bubble Tea package tests**

Run:

```bash
go test ./internal/ui/bubble -run 'TestQueueEdit' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/input.go internal/ui/bubble/input_token.go internal/ui/bubble/bubble_test.go
 git commit -m "feat: edit queued messages safely"
```

---

### Task 5: Adapt queued turn execution to stable queue items

**Files:**
- Modify: `internal/ui/bubble/commands.go`
- Modify: `internal/ui/bubble/input.go`
- Modify: `internal/ui/bubble/bubble_test.go`

**Interfaces:**
- Consumes: `CommandQueue.DequeueDraft() (queuedChatItem, bool)`.
- Produces: unchanged `startNextQueuedTurn() tea.Cmd` execution behavior with queue item identity available for transcript/status updates.

- [ ] **Step 1: Add regression tests before changing execution**

Test that:

```go
func TestStartNextQueuedTurnConsumesQueueHeadAndKeepsRichDraft(t *testing.T) { /* fake runner sees first draft and image metadata */ }
func TestStartNextQueuedTurnRequeuesItemIfGuardStartFails(t *testing.T) { /* same ID and order remain */ }
func TestTurnFinishedStartsNextQueuedItemInOrder(t *testing.T) { /* existing FIFO behavior remains */ }
```

- [ ] **Step 2: Run the regression tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(StartNextQueuedTurn|TurnFinished)' -count=1
```

Expected: compile failure or behavior mismatch until call sites use the new return type.

- [ ] **Step 3: Update `startNextQueuedTurn` minimally**

Change only the dequeue variable from a draft to an item:

```go
item, ok := m.chatQueue.DequeueDraft()
if !ok { return nil }
draft := item.Draft
if !m.queryGuard.StartModel() {
    _ = m.chatQueue.InsertAt(0, item)
    return nil
}
```

Retain `latestTurnUserEntry(draft.Text)`, `runTurnCmd`, turn IDs, guard transitions, animation scheduling, and all existing error/cancellation behavior. If the item is requeued after `StartModel` failure, restore it at the head rather than creating a new ID.

- [ ] **Step 4: Run all queue and package tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(CommandQueue|Queue|StartNextQueuedTurn|TurnFinished)' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bubble/commands.go internal/ui/bubble/input.go internal/ui/bubble/bubble_test.go
 git commit -m "refactor: preserve queue items through turn execution"
```

---

### Task 6: Add compact/expanded queue rendering and theme styles

**Files:**
- Create: `internal/ui/bubble/queue_render.go`
- Create: `internal/ui/bubble/queue_render_test.go`
- Modify: `internal/ui/bubble/styles.go`

**Interfaces:**
- Consumes: `CommandQueue.Items`, `appModel.queueMode`, `queueSelectedID`, `queueEdit`, existing `StyleSet`, display-width helpers, and `inputToken` image metadata.
- Produces: `renderQueuePanel`, `renderQueueSummary`, `renderQueueItemSummary`, and style fields used by `layout.go`.

- [ ] **Step 1: Define rendering contracts and write failing tests**

Add tests for these exact outputs/constraints:

```go
func TestRenderQueueSummaryShowsCountAndHead(t *testing.T) {
    got := renderQueueSummary(items, 48)
    if !strings.Contains(got, "QUEUE · 3") || !strings.Contains(got, "first") { t.Fatal(got) }
}

func TestRenderQueuePanelShowsSelectionPositionAndState(t *testing.T) {
    got := m.renderQueuePanel(48, 6)
    for _, want := range []string{"QUEUE · 3 · 2/3", "SELECTING", "› 2 · second", "↑/↓", "i 编辑"} {
        if !strings.Contains(got, want) { t.Fatalf("missing %q in %q", want, got) }
    }
}

func TestRenderQueuePanelIsBoundedAndTruncatesLongDraft(t *testing.T) { /* assert line count <= height and no line exceeds width */ }
func TestRenderQueueItemSummaryIncludesImageCount(t *testing.T) { /* assert [image ×N] marker */ }
func TestRenderQueueEditLabelUsesEditVisualState(t *testing.T) { /* assert EDIT and item number */ }
```

- [ ] **Step 2: Run rendering tests to verify failure**

Run:

```bash
go test ./internal/ui/bubble -run 'TestRenderQueue' -count=1
```

Expected: FAIL because no queue renderer or styles exist.

- [ ] **Step 3: Add queue-specific styles to `StyleSet`**

Add fields for:

```go
QueueSummary
QueueCount
QueueSelected
QueueSelectingLabel
QueueEditingLabel
QueueEditBorder
QueueHint
QueueItemMuted
```

Initialize them from theme colors: blue/highlight styles for selecting, yellow/orange styles for editing, muted styles for hints and unselected items. Keep existing style initialization and theme switching intact.

- [ ] **Step 4: Implement width-safe queue rendering**

Implement:

```go
func (m appModel) renderQueuePanel(width, height int) string
func (m appModel) renderQueueSummary(width int) string
func renderQueueItemSummary(item queuedChatItem, width int) string
```

Normal summary:

```text
QUEUE · 3 · 检查项目结构                         ↓ 选择
```

Selecting panel:

```text
QUEUE · 3 · 2/3                         SELECTING
  1 · first
› 2 · second
  3 · third
↑/↓ 选择 · i 编辑 · alt/command+k/j 调整 · esc 退出
```

Editing state must show `EDIT · queue item #N` and the edit hint; the queue list must exclude the temporarily removed item. Use `truncateDisplayWidth`, `terminalCellWidth`, and `fitStyledRect` so CJK, images, and ANSI styling do not overflow. Render no queue output when `Len() == 0` in inactive mode; selection mode must never render more than its supplied height.

- [ ] **Step 5: Run renderer tests and package tests**

Run:

```bash
go test ./internal/ui/bubble -run 'TestRenderQueue' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/queue_render.go internal/ui/bubble/queue_render_test.go internal/ui/bubble/styles.go
 git commit -m "feat: render compact queue states"
```

---

### Task 7: Integrate queue panel into layout and viewport sizing

**Files:**
- Modify: `internal/ui/bubble/layout.go`
- Modify: `internal/ui/bubble/fixed_layout_test.go`
- Modify: `internal/ui/bubble/bubble_test.go`

**Interfaces:**
- Consumes: `renderQueueSummary`, `renderQueuePanel`, `queueMode`, and existing `computeTUILayout`/`currentLayout` behavior.
- Produces: input-region rendering with compact summary in inactive mode and bounded expanded panel in selecting/editing modes.

- [ ] **Step 1: Add failing layout tests**

Test concrete layout invariants:

```go
func TestQueueSummaryAddsOneInputRegionLine(t *testing.T) { /* inactive non-empty queue increases input dock by exactly one */ }
func TestQueueSelectionExpandsWithinMaximum(t *testing.T) { /* selecting panel is capped and transcript retains at least one line when possible */ }
func TestSmallTerminalPrioritizesInputOverQueueRows(t *testing.T) { /* queue rows are truncated before input becomes zero */ }
func TestQueuePanelDoesNotChangeTranscriptContent(t *testing.T) { /* panel is not added to transcript viewport */ }
```

- [ ] **Step 2: Run layout tests to verify failure**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(Queue.*Layout|SmallTerminal|QueuePanel)' -count=1
```

Expected: FAIL because current layout only accounts for textarea/selection dock height.

- [ ] **Step 3: Add queue height calculation**

Add helpers in `layout.go`:

```go
func (m appModel) queuePanelHeight(contentWidth int) int
func (m appModel) requestedInputDockHeight() int
```

Rules:

- inactive + queue non-empty: textarea height plus one summary line;
- selecting: textarea height plus `1 + visibleItems + 1` for header/items/hint, capped at a fixed queue maximum such as 8 total panel lines;
- editing: textarea height plus one edit-status line, with remaining queue summary/list kept compact;
- empty queue: no queue height;
- calculate the request before `computeTUILayout`, then use `computeTUILayoutWithInputLimit` to preserve the existing transcript/input priority.

Do not reuse `selectionDock` state for queue; queue must be an independent panel.

- [ ] **Step 4: Integrate rendering into `renderInputBoxForLayout`**

Render order must be:

```text
queue summary/panel or edit status
textarea
input hint/status
```

The panel must remain inside the fixed outer frame and must call `fitStyledRect` through the existing input-box path. Update `currentLayout` and `relayout` so every queue mutation calls the same sizing path as text changes and window resize events.

- [ ] **Step 5: Run layout, renderer, and full UI tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Test(Queue|FixedLayout|RenderQueue)' -count=1
go test ./internal/ui/bubble -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/layout.go internal/ui/bubble/fixed_layout_test.go internal/ui/bubble/bubble_test.go
 git commit -m "feat: integrate queue panel into input layout"
```

---

### Task 8: Add queue clear/delete affordances and update the approved design documentation

**Files:**
- Modify: `internal/ui/bubble/input.go`
- Modify: `internal/ui/bubble/command_registry.go`
- Modify: `internal/ui/bubble/bubble_test.go`
- Modify: `docs/design-chat-queue-tui.md`

**Interfaces:**
- Consumes: selection-mode delete handling and queue snapshot APIs from earlier tasks.
- Produces: user-visible single-item deletion (`d`) and queue clear (`c`) behavior documented alongside the approved v3 design.

- [ ] **Step 1: Write failing deletion/clear tests**

Add:

```go
func TestQueueSelectionDDeletesOnlySelectedItem(t *testing.T) { /* assert neighboring IDs remain */ }
func TestQueueSelectionC ClearsQueueAndLeavesSelection(t *testing.T) { /* assert queue empty and mode inactive */ }
func TestClearCommandClearsQueueWithoutChangingActiveTurn(t *testing.T) { /* query guard/model work remains unchanged */ }
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/ui/bubble -run 'TestQueue.*(Delete|Clear)|TestClearCommand' -count=1
```

Expected: FAIL until clear/delete key behavior is present and tested.

- [ ] **Step 3: Implement deletion and clear behavior**

In selecting mode:

- `d` removes the selected item by stable ID, displays the nearest remaining selection, and exits selection mode when the queue becomes empty.
- `c` clears all pending items, exits selection mode, and returns focus to textarea.

Keep existing `/clear` behavior, which already clears transcript and queue, and add a focused regression assertion that it does not release or alter an active `QueryGuard` turn.

- [ ] **Step 4: Update the design document**

Document the final concrete controls:

```text
↓             enter selection when gating conditions are met
↑/↓           select queue item
i             edit selected item
d             delete selected item
c             clear pending queue
Alt/Command+K move toward queue head
Alt/Command+J move toward queue tail
Esc           leave selection / cancel edit and restore
Enter         save edit to queue tail
:wq + Enter   explicit edit save
```

Record the final API names and the fact that deletion/clear are queue-panel actions, not transcript mutations.

- [ ] **Step 5: Run focused and full tests**

Run:

```bash
go test ./internal/ui/bubble -run 'TestQueue|TestClearCommand' -count=1
go test ./...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/input.go internal/ui/bubble/command_registry.go internal/ui/bubble/bubble_test.go docs/design-chat-queue-tui.md
git commit -m "feat: add queue deletion and clear controls"
```

---

### Task 9: Perform visual, terminal-key, and regression verification

**Files:**
- Verify: `internal/ui/bubble/queue_render.go`
- Verify: `internal/ui/bubble/layout.go`
- Verify: `internal/ui/bubble/input.go`
- Verify: `internal/ui/bubble/bubble_test.go`
- Verify: `docs/design-chat-queue-tui.md`

**Interfaces:**
- Consumes: all completed queue implementation tasks.
- Produces: verified behavior and a clean final regression state.

- [ ] **Step 1: Run formatting and static checks**

Run:

```bash
gofmt -w internal/ui/bubble/command_queue.go internal/ui/bubble/command_queue_test.go internal/ui/bubble/types.go internal/ui/bubble/input.go internal/ui/bubble/input_token.go internal/ui/bubble/commands.go internal/ui/bubble/queue_render.go internal/ui/bubble/queue_render_test.go internal/ui/bubble/layout.go internal/ui/bubble/styles.go internal/ui/bubble/bubble_test.go internal/ui/bubble/fixed_layout_test.go
git diff --check
go vet ./internal/ui/bubble
```

Expected: no formatting, whitespace, or vet errors.

- [ ] **Step 2: Run the complete test suite**

Run:

```bash
go test ./...
```

Expected: all existing and new packages pass, including queue FIFO, rich input, QueryGuard, layout, and transcript tests.

- [ ] **Step 3: Verify key strings in the target terminals**

Run the TUI in the supported local terminal(s), enqueue at least three messages, and verify:

```text
↓ enters selection only at a fresh/end-of-input position
↑/↓ changes selected item
Alt+K/Alt+J move the selected item
Command+K/Command+J move the selected item when the terminal delivers cmd events
 i enters edit and removes the selected item from the visible queue
Esc restores the original item at its original position
Enter saves the edit to the queue tail
:wq + Enter saves the edit to the queue tail
```

If a terminal does not deliver a Command event, record that the existing Alt binding remains the functional path; do not add terminal-specific escape-sequence parsing in this feature.

- [ ] **Step 4: Verify visual invariants manually**

Check the approved v3 states:

```text
inactive:  QUEUE · N · head summary · ↓ 选择
selecting: QUEUE · N · i/N + expanded list + blue focus
editing:   EDIT · queue item #N + yellow input state
```

Also verify CJK truncation, image token summaries, small terminal behavior, transcript scrolling, active-turn queue consumption, and queue count after save/delete/clear.

- [ ] **Step 5: Commit verification-only adjustments if needed**

If formatting or test-only fixes are required:

```bash
git add internal/ui/bubble docs/design-chat-queue-tui.md
git commit -m "test: verify chat queue interaction states"
```

If no adjustments are needed, leave the implementation commits unchanged.

---

## Final Acceptance Checklist

- [ ] Queue item storage uses one cloned rich item slice and stable IDs.
- [ ] Normal mode displays one compact queue summary line with count and head preview.
- [ ] `Down` enters selection only for fresh/end-of-input conditions and does not break history navigation.
- [ ] Selecting mode expands the queue and clearly shows selection position/state.
- [ ] `i` removes the selected item and loads its complete draft into the editor.
- [ ] Edit mode is visually distinct and cannot accidentally start a normal chat turn.
- [ ] `Esc` restores the original item and original position.
- [ ] `Enter` and `:wq + Enter` save edited content to the queue tail.
- [ ] `Alt+J/K` and `Command+J/K` move items in the same direction.
- [ ] `d` deletes one item and `c` clears pending items.
- [ ] Queue rendering is width-safe, height-bounded, and independent from transcript scrolling.
- [ ] Rich image/input-token metadata survives every queue lifecycle operation.
- [ ] Existing serial FIFO execution and `QueryGuard` behavior remain unchanged.
- [ ] `go test ./...` passes.
