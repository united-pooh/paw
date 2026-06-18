package streamma

type TranscriptEntryKind string

const (
	TranscriptInbound TranscriptEntryKind = "inbound"
	TranscriptOwn     TranscriptEntryKind = "own"
)

type TranscriptEntry struct {
	Kind    TranscriptEntryKind
	AgentID string
	From    string
	Text    string
}

type Transcript struct {
	AgentID string
	System  string
	Problem string
	entries []TranscriptEntry
}

func NewTranscript(agent AgentSpec, problem string) *Transcript {
	return &Transcript{
		AgentID: agent.ID,
		System:  agent.SystemPrompt,
		Problem: problem,
	}
}

func (t *Transcript) AppendInbound(from string, step StepPacket) {
	if t == nil {
		return
	}
	t.entries = append(t.entries, TranscriptEntry{
		Kind:    TranscriptInbound,
		AgentID: t.AgentID,
		From:    from,
		Text:    step.Content.Text,
	})
}

func (t *Transcript) AppendOwn(step StepPacket) {
	if t == nil {
		return
	}
	t.entries = append(t.entries, TranscriptEntry{
		Kind:    TranscriptOwn,
		AgentID: t.AgentID,
		Text:    step.Content.Text,
	})
}

func (t *Transcript) Entries() []TranscriptEntry {
	if t == nil {
		return nil
	}
	return append([]TranscriptEntry(nil), t.entries...)
}
