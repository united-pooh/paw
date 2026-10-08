package bubble

import (
	"time"

	"paw/internal/capability/model"
)

type tokenRateBucket struct {
	second int64
	tokens float64
}

type tokenToolTiming struct {
	started time.Time
	ended   time.Time
}

type tokenLiveState struct {
	requestID                 string
	started                   time.Time
	firstText                 time.Time
	finished                  time.Time
	active                    bool
	requestEnded              bool
	total                     float64
	buckets                   [60]tokenRateBucket
	tools                     map[string]tokenToolTiming
	toolsPartial              bool
	turnStarted, turnFinished time.Time
	turnActive                bool
	completedOutput           float64
	completedEstimated        bool
	requestOutput             float64
	requestOutputKnown        bool
	estimatedAtUsage          float64
}

func (s *tokenLiveState) beginTurn(at time.Time) {
	*s = tokenLiveState{turnStarted: at, turnActive: true}
}

func (s *tokenLiveState) start(id string, at time.Time) {
	if id == "" || id == s.requestID {
		return
	}
	if !s.turnActive {
		s.beginTurn(at)
	} else if s.requestID != "" {
		output, estimated := s.outputCount()
		s.completedOutput += output
		s.completedEstimated = s.completedEstimated || estimated
	}
	s.requestID, s.started, s.active = id, at, true
	s.firstText, s.finished, s.total = time.Time{}, time.Time{}, 0
	s.requestEnded = false
	s.requestOutput, s.requestOutputKnown = 0, false
	s.estimatedAtUsage = 0
}

func (s *tokenLiveState) usage(usage model.Usage) {
	if !s.turnActive || usage.RequestID == "" || usage.RequestID != s.requestID || !usage.Knowledge().Output {
		return
	}
	s.requestOutput = float64(usage.Breakdown().Output)
	s.requestOutputKnown = true
	s.estimatedAtUsage = s.total
}

func (s tokenLiveState) outputCount() (float64, bool) {
	if s.requestOutputKnown {
		tail := max(0, s.total-s.estimatedAtUsage)
		return s.requestOutput + tail, tail > 0
	}
	return s.total, true
}

func (s *tokenLiveState) activity(id string, activity model.StreamActivity) {
	if id == "" || id != s.requestID {
		return
	}
	s.text(activity.Text, activity.At)
	s.output(activity.Reasoning, activity.At, false)
	s.output(activity.ToolArguments, activity.At, false)
}

func (s *tokenLiveState) text(text string, at time.Time) {
	s.output(text, at, true)
}

func (s *tokenLiveState) output(text string, at time.Time, visibleText bool) {
	if !s.active || text == "" || at.Before(s.started) {
		return
	}
	if visibleText && s.firstText.IsZero() {
		s.firstText = at
	}
	var tokens float64
	for _, r := range text {
		if r < 128 {
			tokens += .25
		} else {
			tokens++
		}
	}
	s.total += tokens
	second := at.Unix()
	index := int((second%60 + 60) % 60)
	if s.buckets[index].second != second {
		s.buckets[index] = tokenRateBucket{second: second}
	}
	s.buckets[index].tokens += tokens
}

func (s *tokenLiveState) finish(at time.Time) {
	if !s.active {
		return
	}
	s.active = false
	s.finished = at
}

func (s *tokenLiveState) requestEnd(id string, at time.Time) {
	if id == "" || id != s.requestID || s.requestEnded || at.Before(s.started) {
		return
	}
	s.active, s.requestEnded, s.finished = false, true, at
}

func (s *tokenLiveState) resetTools() { s.tools = nil; s.toolsPartial = false }

func (s *tokenLiveState) endTurn(at time.Time) {
	s.finish(at)
	if s.turnActive && !at.Before(s.turnStarted) {
		s.turnFinished, s.turnActive = at, false
	}
	for id, tool := range s.tools {
		if tool.ended.IsZero() {
			tool.ended = at
			s.tools[id] = tool
		}
	}
}

func (s *tokenLiveState) toolStart(id string, at time.Time) {
	if id == "" {
		return
	}
	if s.tools == nil {
		s.tools = make(map[string]tokenToolTiming)
	}
	if _, exists := s.tools[id]; !exists {
		if len(s.tools) >= 4096 {
			s.toolsPartial = true
			return
		}
		s.tools[id] = tokenToolTiming{started: at}
	}
}

func (s *tokenLiveState) toolEnd(id string, at time.Time) {
	tool, ok := s.tools[id]
	if !ok || !tool.ended.IsZero() || at.Before(tool.started) {
		return
	}
	tool.ended = at
	s.tools[id] = tool
}

func (s tokenLiveState) view(now time.Time) tokenRateView {
	v := tokenRateView{Active: s.active, Observed: s.total > 0, RequestID: s.requestID, Samples: make([]float64, 60), ToolCalls: len(s.tools), ToolCallsPartial: s.toolsPartial}
	output, estimated := s.outputCount()
	v.TurnTokens = s.completedOutput + output
	v.TurnEstimated = s.completedEstimated || estimated
	v.TurnObserved = v.TurnTokens > 0 || s.requestOutputKnown && !s.completedEstimated
	v.TurnActive = s.turnActive
	turnEnd := now
	if !s.turnFinished.IsZero() {
		turnEnd = s.turnFinished
	}
	if !s.turnStarted.IsZero() && turnEnd.After(s.turnStarted) {
		v.TurnElapsed = turnEnd.Sub(s.turnStarted)
		v.TurnAverage = v.TurnTokens / v.TurnElapsed.Seconds()
	}
	for i := range v.Samples {
		second := now.Unix() - int64(59-i)
		bucket := s.buckets[int((second%60+60)%60)]
		if bucket.second == second {
			v.Samples[i] = bucket.tokens
		}
	}
	if !s.firstText.IsZero() {
		v.FirstText, v.FirstTextKnown = s.firstText.Sub(s.started), true
	}
	end := now
	if !s.finished.IsZero() {
		end = s.finished
	}
	if seconds := end.Sub(s.started).Seconds(); seconds > 0 {
		v.Average = s.total / seconds
	}
	if s.active {
		v.Current = (v.Samples[57] + v.Samples[58] + v.Samples[59]) / 3
	}
	for _, tool := range s.tools {
		end := tool.ended
		if end.IsZero() {
			v.ToolActive = true
			end = now
		}
		if end.After(tool.started) {
			v.ToolDuration += end.Sub(tool.started)
		}
	}
	return v
}
