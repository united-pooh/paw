# macOS Keychain and Diagnostics Implementation Plan

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a native macOS Keychain credential backend without changing Windows behavior, and preserve complete long diagnostics by wrapping them to the modal's terminal-cell width.

**Architecture:** Keep `OSCredentialStore` platform-neutral and select mutually exclusive platform implementations with Go build tags. The Darwin implementation uses a small CGO bridge around Security.framework Generic Password APIs; the UI reuses the existing grapheme- and ANSI-safe terminal-cell wrapper before fixed-panel clipping.

**Tech Stack:** Go 1.26, CGO, macOS CoreFoundation/Security frameworks, Bubble Tea/Lip Gloss, existing `wrapStyledCellText` geometry primitives.

---

## File map

- Create `internal/config/credentials_darwin.go`: Darwin/CGO bridge and platform credential operations.
- Create `internal/config/credentials_darwin_test.go`: status mapping, input validation, and opt-in real-Keychain lifecycle test.
- Modify `internal/config/credentials_other.go`: narrow the fallback build tag so it does not collide with Darwin/CGO.
- Modify `internal/ui/bubble/layout.go`: centralize modal width and expose its body width.
- Modify `internal/ui/bubble/config_center.go`: wrap diagnostic lines before fixed-panel clipping.
- Modify `internal/ui/bubble/config_center_test.go`: reproduce the blocked migration and assert the full diagnostic tail remains visible.
- Modify `docs/configuration-v2.md`: document macOS Keychain and no-CGO env fallback behavior.

### Task 1: Lock down the Darwin platform boundary

**Files:**
- Create: `internal/config/credentials_darwin_test.go`
- Modify: `internal/config/credentials_other.go:1`

- [x] **Step 1: Add failing Darwin status and validation tests**

Add Darwin/CGO-only tests that require these exact mappings and never touch Keychain for validation failures:

```go
//go:build darwin && cgo

package config

import (
    "errors"
    "os"
    "strings"
    "testing"
    "time"
)

func TestDarwinCredentialStatusMapping(t *testing.T) {
    if err := darwinCredentialError("read", darwinErrSecSuccess); err != nil {
        t.Fatalf("success: %v", err)
    }
    if err := darwinCredentialError("read", darwinErrSecItemNotFound); !errors.Is(err, ErrCredentialNotFound) {
        t.Fatalf("not found: %v", err)
    }
    if err := darwinCredentialError("read", darwinErrSecNotAvailable); !errors.Is(err, ErrCredentialStoreUnavailable) {
        t.Fatalf("unavailable: %v", err)
    }
    err := darwinCredentialError("write", darwinErrSecAuthFailed)
    if err == nil || !strings.Contains(err.Error(), "write credential in macOS Keychain") || !strings.Contains(err.Error(), "-25293") {
        t.Fatalf("auth failure: %v", err)
    }
}

func TestDarwinCredentialRejectsEmptyInputWithoutKeychainAccess(t *testing.T) {
    if _, err := platformCredentialGet(""); !errors.Is(err, ErrCredentialNotFound) {
        t.Fatalf("get empty: %v", err)
    }
    if err := platformCredentialSet("", "secret"); !errors.Is(err, ErrCredentialNotFound) {
        t.Fatalf("set empty ID: %v", err)
    }
    if err := platformCredentialSet("provider/test", ""); !errors.Is(err, ErrCredentialNotFound) {
        t.Fatalf("set empty secret: %v", err)
    }
    if err := platformCredentialDelete(""); !errors.Is(err, ErrCredentialNotFound) {
        t.Fatalf("delete empty: %v", err)
    }
}

func TestDarwinCredentialLifecycle(t *testing.T) {
    if os.Getenv("PAW_TEST_MACOS_KEYCHAIN") != "1" {
        t.Skip("set PAW_TEST_MACOS_KEYCHAIN=1 to exercise the login keychain")
    }
    id := "test/codex-" + time.Now().UTC().Format("20060102T150405.000000000")
    t.Cleanup(func() { _ = platformCredentialDelete(id) })
    if err := platformCredentialSet(id, "first-secret"); err != nil { t.Fatal(err) }
    if got, err := platformCredentialGet(id); err != nil || got != "first-secret" { t.Fatalf("first get=%q err=%v", got, err) }
    if err := platformCredentialSet(id, "second-secret"); err != nil { t.Fatal(err) }
    if got, err := platformCredentialGet(id); err != nil || got != "second-secret" { t.Fatalf("updated get=%q err=%v", got, err) }
    if err := platformCredentialDelete(id); err != nil { t.Fatal(err) }
    if _, err := platformCredentialGet(id); !errors.Is(err, ErrCredentialNotFound) { t.Fatalf("get after delete: %v", err) }
}
```

- [x] **Step 2: Run the focused test and confirm it fails before implementation**

Run: `go test ./internal/config -run 'TestDarwinCredential' -count=1`

Expected: build failure because `darwinCredentialError` and the Darwin status constants do not exist.

- [x] **Step 3: Narrow the generic fallback build condition**

Change the first line of `credentials_other.go` to:

```go
//go:build !windows && (!darwin || !cgo)
```

Expected: Darwin with CGO now requires its dedicated backend; Linux and Darwin without CGO still compile the unavailable fallback.

### Task 2: Implement the native Security.framework backend

**Files:**
- Create: `internal/config/credentials_darwin.go`
- Test: `internal/config/credentials_darwin_test.go`

- [x] **Step 1: Add the minimal Darwin/CGO implementation**

Create a `//go:build darwin && cgo` file with:

```go
const (
    darwinErrSecSuccess      int32 = 0
    darwinErrSecNotAvailable int32 = -25291
    darwinErrSecAuthFailed   int32 = -25293
    darwinErrSecItemNotFound int32 = -25300
)

func platformCredentialGet(id string) (string, error)
func platformCredentialSet(id, secret string) error
func platformCredentialDelete(id string) error
func darwinCredentialError(operation string, status int32) error
```

The CGO preamble must link `CoreFoundation` and `Security` and define four focused helpers:

```c
int32_t paw_credential_copy(const void *account_bytes, size_t account_len, void **secret_bytes, size_t *secret_len);
int32_t paw_credential_set(const void *account_bytes, size_t account_len, const void *secret_bytes, size_t secret_len);
int32_t paw_credential_delete(const void *account_bytes, size_t account_len);
char *paw_security_error_message(int32_t status);
```

Each helper builds a Generic Password query with `kSecClassGenericPassword`, service `Paw`, and account equal to the credential ID. `copy` uses `SecItemCopyMatching`; `set` uses `SecItemUpdate` and falls back to `SecItemAdd` only for `errSecItemNotFound`; `delete` uses `SecItemDelete`. All `CFStringRef`, `CFDataRef`, dictionaries, result objects, and malloc buffers are released on every return path.

Go allocates input byte buffers with `C.CBytes`, frees them with `C.free`, copies returned secret bytes with `C.GoBytes`, and never includes the secret in an error. `darwinCredentialError` maps item-not-found and unavailable to the existing sentinel errors; all other failures use `SecCopyErrorMessageString` and include the operation plus numeric OSStatus.

- [x] **Step 2: Format and run focused tests**

Run: `gofmt -w internal/config/credentials_darwin.go internal/config/credentials_darwin_test.go`

Run: `go test ./internal/config -run 'TestDarwinCredential' -count=1`

Expected: mapping and validation tests pass; lifecycle test skips unless explicitly enabled.

- [x] **Step 3: Exercise a temporary real-Keychain lifecycle**

Run: `PAW_TEST_MACOS_KEYCHAIN=1 go test ./internal/config -run '^TestDarwinCredentialLifecycle$' -count=1 -v`

Expected: PASS after create, read, update, delete, and post-delete not-found; cleanup removes the timestamped test entry even on failure.

- [x] **Step 4: Verify Darwin without CGO uses the fallback**

Run: `CGO_ENABLED=0 go test ./internal/config -run '^TestCredentialResolutionPrefersKeyringThenOrderedEnvironment$' -count=1`

Expected: PASS, proving the fallback implementation still compiles and env resolution works.

### Task 3: Preserve complete Diagnostics text

**Files:**
- Modify: `internal/ui/bubble/layout.go:340-351`
- Modify: `internal/ui/bubble/config_center.go:900-906`
- Modify: `internal/ui/bubble/config_center_test.go`

- [x] **Step 1: Add a failing blocked-migration render test**

Create a temporary v1 configuration containing a fake plaintext key, open the manager with `FakeCredentialStore{Unavailable:true}`, render the automatically opened Diagnostics page at width 100 and height 30, strip ANSI, and assert:

```go
if !strings.Contains(rendered, "configure an environment variable and retry") {
    t.Fatalf("diagnostic tail was clipped:\n%s", rendered)
}
for index, line := range strings.Split(rendered, "\n") {
    if got := lipgloss.Width(line); got > 80 {
        t.Fatalf("line %d width=%d, want <=80: %q", index+1, got, line)
    }
}
```

Run: `go test ./internal/ui/bubble -run '^TestConfigCenterDiagnosticsWrapLongMigrationError$' -count=1`

Expected: FAIL because the long error tail is clipped before the fixed panel is rendered.

- [x] **Step 2: Centralize modal width calculation**

Add these methods in `layout.go`, then have `renderModalPanel` call them:

```go
func (m appModel) modalPanelWidth() int {
    availableWidth := maxInt(1, m.currentLayout().contentWidth-4)
    panelWidth := minInt(80, availableWidth)
    if availableWidth >= 32 { panelWidth = maxInt(32, panelWidth) }
    return panelWidth
}

func (m appModel) modalPanelBodyWidth() int {
    panelWidth := m.modalPanelWidth()
    return maxInt(1, panelWidth-wizardPanelStyle.GetHorizontalBorderSize()-wizardPanelStyle.GetHorizontalPadding())
}
```

- [x] **Step 3: Wrap diagnostics before fixed-panel clipping**

In the Diagnostics branch of `renderConfigCenterBox`, append each logical line through:

```go
bodyWidth := m.modalPanelBodyWidth()
lines = append(lines, wrapStyledCellText(
    fmt.Sprintf("path: %s\nrevision: %d  ready: %v", m.configCenterController.ConfigPath(), snapshot.Revision, snapshot.Ready),
    bodyWidth,
)...)
for _, diagnostic := range snapshot.Diagnostics {
    lines = append(lines, wrapStyledCellText(
        fmt.Sprintf("[%s] %s %s", diagnostic.Severity, diagnostic.Field, diagnostic.Message),
        bodyWidth,
    )...)
}
```

- [x] **Step 4: Format and run UI tests**

Run: `gofmt -w internal/ui/bubble/layout.go internal/ui/bubble/config_center.go internal/ui/bubble/config_center_test.go`

Run: `go test ./internal/ui/bubble -run 'TestConfigCenterDiagnosticsWrapLongMigrationError|TestCredentialEditorNeverRendersSecret' -count=1`

Expected: PASS, with full diagnostic text visible and secret masking unchanged.

### Task 4: Cross-platform and regression verification

**Files:**
- Verify all files changed above.
- Modify: `docs/configuration-v2.md:136`

- [x] **Step 1: Document the platform behavior**

State that macOS uses Security.framework Keychain, macOS builds with `CGO_ENABLED=0` use env fallback, Windows keeps Credential Manager, and Linux without Secret Service remains env-only.

- [x] **Step 2: Run targeted packages**

Run: `go test ./internal/config ./internal/ui/bubble -count=1`

Expected: PASS.

- [x] **Step 3: Cross-compile Windows tests without running them**

Run: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/paw-config-windows-amd64.test.exe ./internal/config`

Run: `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/paw-config-windows-arm64.test.exe ./internal/config`

Expected: both commands exit 0, proving the unchanged Windows backend is selected and compiles for both architectures.

- [x] **Step 4: Run repository-wide verification**

Run: `go test ./...`

Run: `go vet ./...`

Run: `git diff --check`

Expected: all commands exit 0. If an unrelated pre-existing baseline failure appears, record the exact package and demonstrate that all targeted commands still pass.

- [x] **Step 5: Inspect the final diff and secret safety**

Run: `git diff --stat && git diff -- internal/config/credentials_darwin.go internal/config/credentials_darwin_test.go internal/config/credentials_other.go internal/ui/bubble/layout.go internal/ui/bubble/config_center.go internal/ui/bubble/config_center_test.go`

Expected: only the planned platform backend, build tag, modal geometry, diagnostics wrapping, and tests are present; no real credential values or user configuration files appear.
