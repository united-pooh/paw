# Input Dock Border Layout — Visual Evidence

- **Changed files:**
  - `internal/ui/bubble/status_line.go`
  - `internal/ui/bubble/layout.go`
  - `internal/ui/bubble/queue_render.go`
  - `internal/ui/bubble/status_line_test.go`
  - `internal/ui/bubble/fixed_layout_test.go`
  - `internal/ui/bubble/goal_visual_test.go`
  - `internal/ui/bubble/input_mode_test.go`
  - `internal/ui/bubble/input_queue_render_test.go`
  - `internal/ui/bubble/worktree_test.go`
- **Route / URL:** temporary local capture page generated from the real `appModel.View()` output
- **Viewport:** browser `1600 × 520`; rendered TUI frame `100 × 22` terminal cells
- **Artifact:** `input-dock-layout.png`
- **Observed result:** The row directly above the input is entirely occupied by the context progress bar. The bottom border places `chat` against the left side, `42k / 128k  ⓒ42%` in the middle, and `paw  dev` against the right side. The three regions remain separated without overwriting one another.
- **Source:** The HTML fixture was generated from the real `appModel.View()` output using a temporary Go test overlay; no fixture-only Go source or HTML file was added to the repository.
