package goal

import (
	"fmt"
	"strings"

	"paw/internal/message"
)

func experimentProgress(history []message.Message, excluded map[string]bool) string {
	var progress strings.Builder
	calls := map[string]string{}
	for _, entry := range history {
		for _, call := range goalHistoryToolCalls(entry) {
			calls[call.ID] = call.Name
		}
		for _, result := range goalHistoryToolResults(entry) {
			name := calls[result.ToolUseID]
			if name != "" && name != "update_todo" && name != "goal_set_metric" && name != "goal_record_metric" && !excluded[result.ToolUseID] {
				fmt.Fprintf(&progress, "%q\x00", result.ToolUseID)
			}
		}
	}
	return progress.String()
}

func measurementFresh(id string, history []message.Message) bool {
	calls := map[string]string{}
	seen := false
	for _, entry := range history {
		for _, call := range goalHistoryToolCalls(entry) {
			calls[call.ID] = call.Name
		}
		for _, result := range goalHistoryToolResults(entry) {
			if result.ToolUseID == id {
				seen = true
				continue
			}
			if !seen {
				continue
			}
			switch calls[result.ToolUseID] {
			case "goal_record_metric", "goal_set_metric", "update_todo", "Read", "Grep", "Glob", "LS":
			default:
				return false
			}
		}
	}
	return seen
}

func goalPrompt(goal Goal, supplement string) string {
	observation := Observation{Metric: goal.Metric}
	if len(goal.Measurements) > 0 {
		observation.Measurement = &goal.Measurements[len(goal.Measurements)-1]
	}
	prompt := "当前 Goal：" + goal.Objective + "\n" + continuationPrompt(observation).Content
	if supplement != "" {
		prompt += "\n用户补充（继续同一个目标，不改变冻结验收）：\n" + supplement
	}
	return prompt
}
