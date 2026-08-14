# Config Center model-tab merge visual evidence

- Changed files: `internal/ui/bubble/config_center.go`, `internal/ui/bubble/config_center_general.go`, `internal/ui/bubble/bubble_test.go`, `internal/ui/bubble/config_center_test.go`, `internal/ui/bubble/config_center_general_test.go`, `internal/ui/bubble/config_center_fullscreen_test.go`, `docs/superpowers/specs/2026-08-11-provider-model-auto-discovery-design.md`, `docs/superpowers/specs/2026-08-14-config-center-flat-search-ui-design.md`, `docs/superpowers/specs/2026-08-14-config-center-model-tab-merge-design.md`
- Route / URL: TUI `/config` → `模型`; capture wrapper loaded the rendered ANSI frame from `file:///tmp/paw-config-center-models.html`
- Terminal viewport: `100 × 30` cells
- Screenshot viewport: `1100 × 640` pixels
- Artifact: `.agent/visual/config-center-model-merge.png`
- Observed result: the top navigation contains exactly `通用 / 服务商 / 模型 / 凭据 / 诊断`; there is no separate `当前模型` tab. The active `local/two` model is first and marked `当前`, configured and discovered sources remain visible, `+ 添加模型` stays at the bottom, and the footer advertises `Enter 设为当前` plus `Space 管理`. The image is non-empty (`45,415` bytes, `1100 × 640`, 672 sampled unique colors) and preserves the selected-tab highlight without clipped rows or footer text.
