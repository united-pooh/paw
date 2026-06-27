package streamma

import "sort"

func Replay(events []Event) ReplaySummary {
	summary := ReplaySummary{Status: RunCompleted}
	states := map[string]*ReplayAgentState{}
	seenInbound := map[string]map[string]bool{}
	eventsByID := map[string]Event{}

	stateFor := func(agentID string) *ReplayAgentState {
		if agentID == "" {
			agentID = "unknown"
		}
		state := states[agentID]
		if state == nil {
			state = &ReplayAgentState{AgentID: agentID}
			states[agentID] = state
		}
		return state
	}
	seenInboundFor := func(agentID string) map[string]bool {
		seen := seenInbound[agentID]
		if seen == nil {
			seen = map[string]bool{}
			seenInbound[agentID] = seen
		}
		return seen
	}

	for _, event := range events {
		copied := cloneEvent(event)
		summary.Events = append(summary.Events, copied)
		if copied.EventID != "" {
			eventsByID[copied.EventID] = copied
		}

		switch copied.Type {
		case EventStepCommitted:
			if copied.Step != nil {
				step := cloneStepPacket(*copied.Step)
				summary.Steps = append(summary.Steps, step)
				agentID := step.AgentID
				if agentID == "" {
					agentID = copied.ProducerAgentID
				}
				state := stateFor(agentID)
				seen := seenInboundFor(agentID)
				for _, inputEventID := range step.Dependencies.InputEvents {
					if seen[inputEventID] {
						continue
					}
					inputEvent, ok := eventsByID[inputEventID]
					if !ok || inputEvent.Step == nil {
						continue
					}
					inputStep := cloneStepPacket(*inputEvent.Step)
					from := inputEvent.ProducerAgentID
					if from == "" {
						from = inputStep.AgentID
					}
					state.Transcript = append(state.Transcript, ReplayTranscriptEntry{
						Kind:          TranscriptInbound,
						AgentID:       agentID,
						From:          from,
						Text:          inputStep.Content.Text,
						StepID:        inputStep.StepID,
						SourceEventID: inputEvent.EventID,
					})
					seen[inputEventID] = true
				}
				state.Transcript = append(state.Transcript, ReplayTranscriptEntry{
					Kind:          TranscriptOwn,
					AgentID:       agentID,
					Text:          step.Content.Text,
					StepID:        step.StepID,
					SourceEventID: copied.EventID,
				})
				state.OwnStepIDs = append(state.OwnStepIDs, step.StepID)
				state.LastEventID = copied.EventID
				state.LastStepID = step.StepID
				state.LastStepIndex = step.StepIndex
				state.LastStepText = step.Content.Text
			}
		case EventUpstreamEOF:
			if copied.TargetAgentID != "" {
				target := stateFor(copied.TargetAgentID)
				target.ReceivedEOF = appendUniqueString(target.ReceivedEOF, copied.ProducerAgentID)
				target.LastEventID = copied.EventID
			}
			if copied.ProducerAgentID != "" {
				producer := stateFor(copied.ProducerAgentID)
				producer.Completed = true
				producer.LastEventID = copied.EventID
			}
		case EventFinalAnswer:
			if copied.Final != nil {
				final := cloneFinalAnswerPacket(*copied.Final)
				summary.Final = &final
				agentID := final.AgentID
				if agentID == "" {
					agentID = copied.ProducerAgentID
				}
				state := stateFor(agentID)
				state.Completed = true
				state.Final = true
				state.LastEventID = copied.EventID
			}
		case EventRunFailed:
			summary.Status = RunFailed
			summary.Error = copied.Error
			summary.FailureEventID = copied.EventID
			summary.FailureSequence = copied.Seq
			if copied.ProducerAgentID != "" {
				state := stateFor(copied.ProducerAgentID)
				state.Failed = true
				state.FailureEventID = copied.EventID
				state.FailureSequence = copied.Seq
				state.LastEventID = copied.EventID
			}
			if copied.TargetAgentID != "" && copied.TargetAgentID != copied.ProducerAgentID {
				state := stateFor(copied.TargetAgentID)
				state.Failed = true
				state.FailureEventID = copied.EventID
				state.FailureSequence = copied.Seq
				state.LastEventID = copied.EventID
			}
		}
	}
	agentIDs := make([]string, 0, len(states))
	for agentID := range states {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		state := *states[agentID]
		if len(state.ReceivedEOF) > 1 {
			sort.Strings(state.ReceivedEOF)
		}
		summary.AgentStates = append(summary.AgentStates, state)
	}
	return summary
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
