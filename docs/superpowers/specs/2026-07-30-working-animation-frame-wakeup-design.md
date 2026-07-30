# Working Animation Frame Wakeup Design

## Background

The Bubble Tea UI currently starts a `cursorFrameTick` chain during initialization and lets that chain stop when `needsUIAnimationFrames` reports no active animation. This is intentional for the idle state: avoiding periodic full-frame redraws prevents Ghostty IME preedit from being pulled back to the textarea's committed cursor position.

The frame chain is not reliably restarted when model work begins. As a result, the context usage animation, spinner, equalizer, and token ripple can remain frozen while the model is waiting for its first token or running tools. Incoming model deltas happen to redraw the UI, making the animations appear to update only when output arrives.

## Goals

- Start UI animation frames immediately when model work starts, without waiting for model output.
- Keep the working animation moving throughout model requests, streaming output, tool calls, and subagent work.
- When work ends, preserve the token ripple's current phase and speed.
- Let the ripple head and full tail continue to the right until they have completely left the context usage meter.
- Stop periodic Bubble Tea redraws once all non-cursor animations have completed.
- Preserve the existing idle behavior that avoids continuous redraws and protects Ghostty IME cursor positioning.

## Non-goals

- Changing the visual design, speed, glyphs, colors, or amplitude of the spinner, equalizer, context meter, or ripple.
- Replacing Bubble Tea commands with a permanent ticker or background rendering loop.
- Changing the independently timed real terminal cursor animation.
- Refactoring unrelated model, tool, or subagent lifecycle code.

## Chosen Approach

Use a unified, demand-driven frame wakeup mechanism.

The model will track whether a UI animation frame command is already scheduled. A helper will schedule `cursorFrameTick` only when no frame is currently in flight. State transitions that begin work or start a finite animation will call this helper. Once a `cursorFrameMsg` arrives, the scheduled flag is cleared, animation state is advanced, and the existing animation predicate determines whether another frame is required.

This retains the existing self-terminating animation loop while making it restartable.

## Animation Scheduling

### Scheduling state

Add a boolean field to `appModel` representing whether a `cursorFrameTick` command is currently scheduled.

The invariant is:

- At most one animation tick chain is scheduled by the application model.
- Receiving `cursorFrameMsg` consumes the scheduled frame and clears the flag before deciding whether to schedule the next one.
- Any transition that creates animation demand may request a frame through the shared helper.

### Wakeup helper

A helper on `appModel` will return a `tea.Cmd` when a new frame must be scheduled and `nil` when a frame is already pending or no animation is needed. Callers will add the returned command to their existing command batch.

The helper centralizes de-duplication so individual work entry points do not need to understand the frame lifecycle.

### Work lifecycle

The frame loop must be awake whenever `isWorkRunning`, `isGenerating`, or another existing finite animation condition is true.

Wakeups will be attached to state transitions rather than model output events. This includes:

- Starting a normal chat turn.
- Starting a queued turn.
- Starting a StreamMA or subagent turn through the same model-work lifecycle.
- Any other existing path that changes the query guard from idle to model-running.
- Starting a context meter transition or token-ripple exit when no frame is pending.

While work remains active, each processed frame schedules its successor. Model deltas may still cause additional Bubble Tea renders, but they are not responsible for animation progress.

## Token Ripple Exit

### Continuity requirement

The token ripple uses an absolute-time phase. Work completion must not reset that phase, pause it, or start a new ripple. The rendered position immediately after completion must therefore be the natural next position after the last working frame.

### Exit deadline

At work completion, record an exit deadline derived from the current ripple phase. The deadline must cover the remaining movement required for the currently visible ripple head and its complete tail to leave the right edge of the context meter.

During this exit interval:

- `tokenRippleActive` remains true even though model work has ended.
- Rendering continues to use the same absolute-time phase and existing speed.
- No new ripple cycle begins after the active ripple has exited.
- `needsUIAnimationFrames` continues returning true.

When the deadline is reached, the ripple becomes inactive and the frame loop may stop if no other finite animation remains.

The existing `tokenRippleHideAt` state remains the owner of this finite post-work lifetime, but its calculation and tests must explicitly guarantee full-tail exit rather than merely preserving an arbitrary remainder of the cycle.

## Data Flow

1. A user action or queued operation starts model work.
2. The model state changes to working and requests an animation-frame wakeup.
3. `cursorFrameTick` emits `cursorFrameMsg` at the configured frame interval.
4. `Update` clears the pending-frame marker and advances spinner, context meter, equalizer, activity, tool progress, and transcript refresh state.
5. `needsUIAnimationFrames` evaluates the updated state.
6. If animation demand remains, the shared helper schedules exactly one successor frame.
7. When work finishes, the ripple exit deadline is calculated from the current phase without changing phase or velocity.
8. Frames continue until the ripple tail and all other finite transitions have completed.
9. With no remaining animation demand, no successor tick is scheduled and the UI returns to event-driven idle rendering.

## Error and Cancellation Behavior

Successful completion, model errors, and expected cancellation all use the same animation shutdown behavior:

- Working state is cleared through the existing lifecycle.
- If the UI was previously working, the current ripple begins its continuous exit.
- A frame is ensured so the exit can progress even if no more model events arrive.

An operation that fails before producing output still gets the same smooth exit. No background ticker or goroutine survives after the finite animation conditions become false.

## Testing

Add focused tests around frame scheduling and ripple continuity:

1. **Initial idle state**: initialization may schedule its initial frame, but after idle animation demand ends, no permanent successor is produced.
2. **Model work wakeup**: transitioning from idle to model-running schedules a frame before any assistant or thinking delta arrives.
3. **Waiting for first token**: repeated frame messages continue scheduling successors while model work remains active.
4. **Tool and subagent work**: frames continue while the query guard reports model work even when `isGenerating` is false.
5. **De-duplication**: multiple wakeup requests before the pending frame arrives produce only one tick command.
6. **Ripple phase continuity**: the ripple position immediately after work completion advances from the prior position without resetting or pausing.
7. **Full-tail exit**: frame scheduling remains active until the entire ripple tail has passed the meter's right edge.
8. **Exit completion**: after the ripple deadline and other transitions complete, no successor frame is scheduled.
9. **Cancellation and errors**: both paths wake and complete the same finite ripple exit.

Tests should use controlled timestamps and direct model updates where practical, avoiding wall-clock sleeps.

## Compatibility and Scope

The change is isolated to the Bubble Tea UI animation lifecycle. It does not alter runner streaming protocols, model events, context accounting, or terminal cursor output. The existing `needsUIAnimationFrames` predicate remains the source of truth for whether redraws should continue; the new mechanism only ensures that demand can restart the frame chain and that duplicate chains are not created.
