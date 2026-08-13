# Token Tracer docking workspace visual evidence

- Changed files: `internal/tokentracer/dashboard/**`, `internal/tokentracer/dashboard_embed.go`, `internal/tokentracer/server.go`, `internal/tokentracer/testdata/dashboardfixture/main.go`
- Route / URL: `http://127.0.0.1:18999/`
- Desktop viewport: `1440x1000`
- Desktop artifact: `.agent/visual/token-tracer-desktop.png`
- Narrow viewport: `760x900`
- Narrow artifact: `.agent/visual/token-tracer-narrow.png`
- Observed result: Five single-instance panels render real snapshot data; the desktop workspace supports dock/tab/resize/maximize/float/close/restore, linked selections remain highlights rather than filters, the narrow layout preserves the desktop layout, and the soft ambient hierarchy has no clipped primary controls or high-contrast wire-grid effect.
- Theme: Paper & Linen light palette (per `pdf-ai-da/DESIGN.md`) — linen canvas `#F6F4EE`, paper surfaces `#FFFDF8`, hairline `#DDDCD5`, clay accents `#9E452F/#C86B4D`, graphite text `#30332F`, sage cache-read `#3F6F4D`, peach selection wash `rgba(220,132,99,0.18)`; pixel samples confirm `#F3F3EC` topbar and `#F6F4EE` canvas in both artifacts.
