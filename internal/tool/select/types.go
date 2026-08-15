package selecttool

type Mode string

const (
	ModeSingle   Mode = "single"
	ModeMultiple Mode = "multiple"
)

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	Prompt             string   `json:"prompt"`
	Mode               Mode     `json:"mode"`
	Options            []Option `json:"options"`
	InitialSelectedIDs []string `json:"initial_selected_ids,omitempty"`
	MinSelect          int      `json:"min_select"`
	MaxSelect          int      `json:"max_select"`
}

type Request struct {
	ID string `json:"id,omitempty"`

	// Questions is populated for a single question-tool invocation. The legacy
	// fields remain for callers that construct a one-question request directly.
	Questions          []Question `json:"questions,omitempty"`
	Prompt             string     `json:"prompt,omitempty"`
	Mode               Mode       `json:"mode,omitempty"`
	Options            []Option   `json:"options,omitempty"`
	InitialSelectedIDs []string   `json:"initial_selected_ids,omitempty"`
	MinSelect          int        `json:"min_select,omitempty"`
	MaxSelect          int        `json:"max_select,omitempty"`
	// Deprecated compatibility metadata; page position is now owned by the UI.
	BatchIndex int `json:"-"`
	BatchSize  int `json:"-"`
}

func (r Request) questionList() []Question {
	if len(r.Questions) != 0 {
		return append([]Question(nil), r.Questions...)
	}
	return []Question{{Prompt: r.Prompt, Mode: r.Mode, Options: append([]Option(nil), r.Options...), InitialSelectedIDs: append([]string(nil), r.InitialSelectedIDs...), MinSelect: r.MinSelect, MaxSelect: r.MaxSelect}}
}

func (r Request) Clone() Request {
	r.Questions = append([]Question(nil), r.Questions...)
	for i := range r.Questions {
		r.Questions[i].Options = append([]Option(nil), r.Questions[i].Options...)
		r.Questions[i].InitialSelectedIDs = append([]string(nil), r.Questions[i].InitialSelectedIDs...)
	}
	r.Options = append([]Option(nil), r.Options...)
	r.InitialSelectedIDs = append([]string(nil), r.InitialSelectedIDs...)
	return r
}

const CustomOptionID = "custom_option"

type SelectedOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Result struct {
	Cancelled       bool             `json:"cancelled"`
	SelectedOptions []SelectedOption `json:"selected_options"`
	Results         []Result         `json:"results,omitempty"`
}

// QuestionResult is kept as a descriptive alias for one item in a batch.
type QuestionResult = Result

// BatchResult is the public result envelope returned by question.
type BatchResult struct {
	Results []Result `json:"results"`
}

func cloneSelectedOptions(options []SelectedOption) []SelectedOption {
	if options == nil {
		return nil
	}
	return append([]SelectedOption{}, options...)
}

func (r Result) questionResults() []Result {
	if r.Results != nil {
		return append([]Result(nil), r.Results...)
	}
	return []Result{{Cancelled: r.Cancelled, SelectedOptions: cloneSelectedOptions(r.SelectedOptions)}}
}

func (r Result) Clone() Result {
	r.SelectedOptions = cloneSelectedOptions(r.SelectedOptions)
	if r.Results != nil {
		r.Results = append([]Result(nil), r.Results...)
		for i := range r.Results {
			r.Results[i].SelectedOptions = cloneSelectedOptions(r.Results[i].SelectedOptions)
			r.Results[i].Results = nil
		}
	}
	return r
}

func (r BatchResult) Clone() BatchResult {
	r.Results = append([]Result(nil), r.Results...)
	for i := range r.Results {
		r.Results[i] = r.Results[i].Clone()
	}
	return r
}

type EventKind uint8

const (
	EventRequest EventKind = iota + 1
	EventInvalidated
	EventClosed
)

type Event struct {
	Kind      EventKind
	Request   Request
	RequestID string
}
