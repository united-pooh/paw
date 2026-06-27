# GoCode Host Adapter

This file adapts the multi-agent pipeline skill for the `codex-agent-go` host.
It is intentionally small: the generic pipeline rules remain in `SKILL.md` and
the detailed stage contracts remain under `agents/`, `references/`, and
`templates/`.

## Discovery

`codex-agent-go` discovers project-local skills from:

- `.codex/skills/<name>/SKILL.md`
- `.claude/skills/<name>/SKILL.md`
- `$CODEX_HOME/skills/<name>/SKILL.md`
- `~/.codex/skills/<name>/SKILL.md`
- `~/.claude/skills/<name>/SKILL.md`

This project installs the pipeline at
`.codex/skills/multi-agent-pipeline/SKILL.md`.

## Invocation

In the Bubble Tea TUI:

- Use `/skills` to confirm this skill is visible.
- Type `$multi-agent-pipeline` and accept the candidate with Tab or Enter.
- The input box writes a Codex-style reference such as
  `[$multi-agent-pipeline](.../.codex/skills/multi-agent-pipeline/SKILL.md)`.

For a submitted turn, `loop.Runner` loads the referenced `SKILL.md` into the
current system prompt only for that turn. The skill context is not committed to
conversation history and does not leak into later turns unless the user invokes
it again.

## Subagents And Stages

GoCode has two relevant execution paths:

- `/subagent` and the subagent tool create bounded worker sessions.
- `/streamma` and `/streamma-trace` map a small stage DAG onto real subagent
  sessions.

When this skill is selected on a `/streamma` turn, the selected skill context is
forwarded into every StreamMA worker system prompt. When a normal subagent prompt
explicitly mentions this skill, the worker resolves it through the same
project-local registry.

The orchestrator remains responsible for merge, final-output collection,
grading aggregation, `.pipeline-last-run-summary.json`, and cleanup. Subagents
must not commit, push, or overwrite unrelated user edits.

## Workspace Contract

Use the normal pipeline workspace names:

- `.pipeline-workspace/` for in-progress stage artifacts
- `.pipeline-last-run-summary.json` for accepted run summaries

`.pipeline-workspace/` is local scratch state and is ignored by this repository.
If a run is rejected or paused, keep the workspace for inspection. If a run is
accepted, remove the workspace after writing the summary.

## Validation

For the skill runtime itself, run from the installed skill directory:

```text
npm test
```

For the Go host integration, run focused tests from the repository root:

```text
go test ./internal/skill ./internal/loop ./internal/ui/bubble -run 'Skill|Dollar|SkillsCommand|CompletionBackspace|StreamMAWorkers'
```
