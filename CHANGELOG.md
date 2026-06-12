# Changelog

## 2026-06-12

- Added Bubble slash commands for `/export`, `/setting`, `/subagent`, and `/tasks`, and updated `/help` to show parameter hints for commands that accept arguments.
- Added persisted runtime state under `.ccagent/`, including `.ccagent/settings.json` defaults, transcript exports in `.ccagent/exports/`, and subagent transcripts under `.ccagent/sessions/<sessionID>/`.
- Changed `/model` so it supports `status`, `custom`, and `deepseek` shortcuts in addition to the interactive provider wizard.
- Changed the interactive UI so the input area shows a context-usage meter by default instead of the old `Input`/`Waiting`/`Terminal` title labels.
- Fixed turn-scoped cache-hit display so turns without usage data fall back to `0.0%`, prevented background system notifications from splitting an active assistant response, and tightened exported transcript permissions to `0600`.
