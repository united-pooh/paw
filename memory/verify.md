# Verification

## Token Tracer high-density workspace

Definition of done and detailed checks live in sections 14 and 15 of `docs/superpowers/specs/2026-08-13-token-tracer-docking-workspace-design.md`.

Required command baseline:

```bash
npm --prefix internal/tokentracer/dashboard test
npm --prefix internal/tokentracer/dashboard run build
go build ./...
go test ./...
```

UI completion also requires fresh desktop and narrow-screen browser screenshots plus structured evidence under `.agent/visual/`.
