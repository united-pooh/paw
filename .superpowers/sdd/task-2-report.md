# Task 2 Report: Select dock focus model and stable result construction

## Status

Implemented task 2 using TDD. No custom text input and no task 4 renderer redesign were implemented.

## Implementation

- Added the three-kind dock focus model: answer, custom action, and chat action.
- Added unified focus-position navigation for move/home/end.
- Initialized answer focus from the initial single-select ID when present; otherwise the first answer remains focused.
- Added stable `SelectedOptions` construction in request-option order, preserving both ID and readable label.
- Added selected-count handling that accounts for a future trimmed custom label without implementing text input.
- Made answer toggling conditional on answer focus.
- Added focused activation dispatch: single answers complete, multiple answers toggle, chat completes with cancellation, and custom currently does nothing.
- Changed Enter handling to use focused activation.
- Cancellation now always returns a non-nil empty `SelectedOptions` slice.
- Retained the legacy `highlighted` renderer bridge because task 4 owns rendering migration; focus changes keep it synchronized for existing rendering/tests.

## Modified files

- `internal/ui/bubble/selection_dock.go`
- `internal/ui/bubble/selection_dock_test.go`
- `internal/ui/bubble/utils.go` (authorized minimal compile migration only)
- `.superpowers/sdd/task-2-report.md`

## RED

Command:

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(NavigatesAnswersAndFixedActions|BuildsStableReadableOptions|ChatUsesCancellationResult|ToggleAndSubmit|SingleSubmit)' -v
```

Result: build failed as expected. The failures identified the old `SelectedIDs` result literals and missing task-2 symbols/behavior, including `selectionDock.focus`, `selectionFocusAnswer`, and `selectionFocusChat`.

## GREEN

Required command:

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(NavigatesAnswersAndFixedActions|BuildsStableReadableOptions|ChatUsesCancellationResult|ToggleAndSubmit|StableOrder|SingleSubmit)' -v
```

Result: PASS; all six selected tests passed.

Additional dock/broker regression command:

```bash
go test ./internal/ui/bubble -run 'Test(NewSelectionDock|SelectionDock|SelectionBroker)' -v
```

Result: PASS; all 11 selected dock/broker tests passed, including key handling, cancellation, tiny-terminal, and long-list coverage.

Validation:

```bash
git diff --check
```

Result: PASS.

## `utils.go` exception

As explicitly authorized, only the two length reads in `selectToolResultTarget` were migrated from `result.SelectedIDs` to `result.SelectedOptions`. Summary wording, detail rendering, and display tests were not redesigned or migrated.

A full `go test ./internal/ui/bubble` consequently still fails in two pre-existing display tests (`TestCompleteSelectToolCallBodySummarizesResult` and `TestSelectToolDisplayTargetAndResultSummary`) because their JSON fixtures still use the removed `selected_ids` protocol. Updating those fixtures/summary tests is outside this task's permitted minimal exception.

## Self-review

- Result order is deterministic because it follows request option order rather than map iteration.
- Single-select results include both ID and label.
- Cancellation distinguishes an intentional empty answer list from nil.
- Custom input behavior was not implemented; only the task-2 state/result scaffolding exists.
- Existing rendering remains functional through the temporary synchronized `highlighted` field, avoiding task-4 rendering changes.
- No reset/checkout cleanup commands were used.

## Concerns

- The temporary `highlighted` compatibility field should be removed when task 4 migrates rendering to `focus`.
- The full bubble package remains red solely due to untouched old-protocol display test fixtures, as described above.

## Review-finding fix

### Status

All task 2 review findings were addressed without implementing task 3 or task 4.

### Changes

- Changed key-layer Enter handling so a focused multiple-choice answer calls `submit()` instead of `activateFocused()`; Space remains the only key that toggles a multiple-choice answer.
- Preserved immediate Enter completion for single-choice answers and `activateFocused()` dispatch for Custom/Chat action focus.
- Added key-layer broker/dock regressions proving Space toggles without completion, Enter submits the existing selection without toggling it, and Enter enforces `MinSelect` without completion.
- Added the closest reachable max regression: because normal toggling prevents constructing an over-max selection, the test proves a second Space at `MaxSelect` is rejected and does not complete.
- Strengthened the legacy navigation test to assert `selectionFocusChat` after `end()` in addition to the temporary `highlighted` bridge.
- Removed the duplicate stable-order test while retaining the equivalent readable stable-order coverage.

### RED

Command:

```bash
gofmt -w internal/ui/bubble/selection_dock_test.go && go test ./internal/ui/bubble -run 'TestSelectionDockMultiple(KeysSpaceTogglesAndEnterSubmits|EnterValidatesMinimumWithoutToggling|SpaceEnforcesMaximumWithoutCompleting)' -v
```

Result: expected failure. The Enter-submit test left the dock open after deselecting `logs`, and the minimum-validation test showed Enter toggled the focused answer instead of validating the existing selection. The max-reachable Space behavior already passed.

### GREEN

Focused review tests:

```bash
go test ./internal/ui/bubble -run 'TestSelectionDockMultiple(KeysSpaceTogglesAndEnterSubmits|EnterValidatesMinimumWithoutToggling|SpaceEnforcesMaximumWithoutCompleting)' -v
```

Result: PASS; all 3 tests passed.

Task 2 focused tests:

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(NavigatesAnswersAndFixedActions|BuildsStableReadableOptions|ChatUsesCancellationResult|ToggleAndSubmit|SingleSubmit|MultipleKeysSpaceTogglesAndEnterSubmits|MultipleEnterValidatesMinimumWithoutToggling|MultipleSpaceEnforcesMaximumWithoutCompleting)' -v
```

Result: PASS; all 8 tests passed.

Related dock/broker regression:

```bash
go test ./internal/ui/bubble -run 'Test(NewSelectionDock|SelectionDock|SelectionBroker)' -v
```

Result: PASS; all 13 selected tests passed.

Validation:

```bash
git diff --check
```

Result: PASS.

### Modified files

- `internal/ui/bubble/selection_dock.go`
- `internal/ui/bubble/selection_dock_test.go`
- `.superpowers/sdd/task-2-report.md` (report append only)

### Self-review

- Multiple-answer Enter now validates and submits the current set without mutating it.
- Space still cannot complete a request and remains constrained by `MaxSelect`.
- Single-answer and fixed-action Enter paths retain their previous semantics.
- Tests exercise the public Bubble Tea key-update path and broker completion rather than only dock helper methods.
- No task 3 custom input or task 4 renderer migration was added.
