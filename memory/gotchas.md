# Gotchas

- Preserve the existing `/api/state`, `/events`, and `/healthz` semantics unless a separately approved contract change is required.
- Do not store trace data, filters, selections, errors, credentials, or request bodies in the persisted Dockview layout.
- Narrow-screen fallback must not overwrite the saved desktop layout.
- Linked selection highlights related data; it must not silently filter other panels.
- Do not claim UI completion from tests alone; verify the rendered workflow and record visual evidence.
